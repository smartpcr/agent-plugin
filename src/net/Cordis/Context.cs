// -----------------------------------------------------------------------
// <copyright file="Context.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// The dependency-injection container. Child contexts are chained (never copied), so isolate
/// realms and intercept configs layer the same way a JavaScript prototype chain does.
/// </summary>
public sealed class Context : IAsyncDisposable
{
    /// <summary>
    /// Initializes a new instance of the <see cref="Context"/> class. The new context is a root:
    /// it owns a fresh service store, registry, event bus and logger service, its fiber is always
    /// active, and the logger service is provided under <see cref="LoggerService.ServiceName"/>.
    /// </summary>
    public Context()
    {
        this.SyncRoot = new object();
        this.Root = this;
        this.Parent = null;
        this.IsolateMap = new ChainMap<IsolateKey>();
        this.InterceptMap = new ChainMap<object>();
        this.Internal = new InternalEvents();
        this.Loggers = new LoggerService();
        this.Fiber = new Fiber(this);
        this.Reflect = new ServiceStore(this);
        this.Registry = new RegistryService(this);
        this.Events = new EventsService(this);
        this.Reflect.Provide(this, LoggerService.ServiceName, this.Loggers);
    }

    private Context(Context parent, Fiber fiber, ChainMap<IsolateKey>? isolate, ChainMap<object>? intercept)
    {
        this.SyncRoot = parent.SyncRoot;
        this.Root = parent.Root;
        this.Parent = parent;
        this.IsolateMap = isolate ?? parent.IsolateMap;
        this.InterceptMap = intercept ?? parent.InterceptMap;
        this.Internal = parent.Internal;
        this.Loggers = parent.Loggers;
        this.Fiber = fiber;
        this.Reflect = parent.Reflect;
        this.Registry = parent.Registry;
        this.Events = parent.Events;
    }

    /// <summary>
    /// Gets the root context at the top of this context's chain; the root returns itself.
    /// </summary>
    public Context Root { get; }

    /// <summary>
    /// Gets the context this one was derived from (by <see cref="Isolate(string, IsolateKey)"/>,
    /// <see cref="Intercept(string, object)"/> or a component load), or <c>null</c> for the root.
    /// </summary>
    public Context? Parent { get; }

    /// <summary>
    /// Gets the fiber that owns this context. Effects, services and listeners registered through
    /// this context belong to that fiber and are disposed when it unloads.
    /// </summary>
    public Fiber Fiber { get; }

    /// <summary>
    /// Gets the service store shared by every context under the same root.
    /// </summary>
    public ServiceStore Reflect { get; }

    /// <summary>
    /// Gets the component registry shared by every context under the same root.
    /// </summary>
    public RegistryService Registry { get; }

    /// <summary>
    /// Gets the event bus shared by every context under the same root.
    /// </summary>
    public EventsService Events { get; }

    /// <summary>
    /// Gets the logger service shared by every context under the same root.
    /// </summary>
    public LoggerService Loggers { get; }

    /// <summary>
    /// Gets the framework's internal lifecycle events (fiber status, service and config changes),
    /// shared by every context under the same root.
    /// </summary>
    public InternalEvents Internal { get; }

    /// <summary>
    /// Gets a logger named after this context's fiber, created on first access.
    /// </summary>
    public Logger Logger => field ??= this.Loggers.For(this);

    internal object SyncRoot { get; }

    internal ChainMap<IsolateKey> IsolateMap { get; }

    internal ChainMap<object> InterceptMap { get; }

    /// <inheritdoc/>
    public override string ToString() => $"Context <{this.Fiber.Name}>";

    /// <summary>
    /// Creates a child context where <paramref name="name"/> resolves in a private realm: services
    /// provided or resolved under that name through the child (and components used from it) do
    /// not see, and are not seen by, the parent's binding. Other names are unaffected.
    /// </summary>
    /// <param name="name">The service name to isolate.</param>
    /// <param name="key">
    /// The realm to use; pass the same key to several contexts to let them share one private
    /// realm. When <c>null</c>, a fresh realm is created.
    /// </param>
    /// <returns>A child context owned by the same fiber as this context.</returns>
    public Context Isolate(string name, IsolateKey? key = null)
    {
        var map = this.IsolateMap.Extend();
        map.Set(name, key ?? new IsolateKey(name));
        return new Context(this, this.Fiber, map, null);
    }

    /// <summary>
    /// Creates a child context where the service <typeparamref name="TService"/> resolves in a
    /// private realm. See <see cref="Isolate(string, IsolateKey)"/>.
    /// </summary>
    /// <typeparam name="TService">The service type whose name (from <see cref="ServiceNames.For{T}"/>) is isolated.</typeparam>
    /// <param name="key">The realm to use, or <c>null</c> to create a fresh one.</param>
    /// <returns>A child context owned by the same fiber as this context.</returns>
    public Context Isolate<TService>(IsolateKey? key = null) => this.Isolate(ServiceNames.For<TService>(), key);

    /// <summary>
    /// Creates a child context carrying an extra config layer for <paramref name="name"/>. The
    /// layer takes priority over intercepts set on ancestor contexts; read it back with
    /// <see cref="GetInterceptConfig{TConfig}(string)"/> or <see cref="InterceptChain{TConfig}(string)"/>.
    /// </summary>
    /// <param name="name">The service name the config applies to.</param>
    /// <param name="config">The config object to attach.</param>
    /// <returns>A child context owned by the same fiber as this context.</returns>
    public Context Intercept(string name, object config)
    {
        var map = this.InterceptMap.Extend();
        map.Set(name, config);
        return new Context(this, this.Fiber, null, map);
    }

    /// <summary>
    /// Creates a child context carrying an extra config layer for the service
    /// <typeparamref name="TService"/>. See <see cref="Intercept(string, object)"/>.
    /// </summary>
    /// <typeparam name="TService">The service type whose name (from <see cref="ServiceNames.For{T}"/>) the config applies to.</typeparam>
    /// <param name="config">The config object to attach.</param>
    /// <returns>A child context owned by the same fiber as this context.</returns>
    public Context Intercept<TService>(object config) => this.Intercept(ServiceNames.For<TService>(), config);

    /// <summary>Gets the highest-priority config for <paramref name="name"/>; see <see cref="InterceptChain{TConfig}(string)"/>.</summary>
    /// <typeparam name="TConfig">The config type to look for; layers of other types are skipped.</typeparam>
    /// <param name="name">The service name whose config is requested.</param>
    /// <returns>The first matching config in priority order, or <c>null</c> when none applies.</returns>
    public TConfig? GetInterceptConfig<TConfig>(string name)
        where TConfig : class
        => this.InterceptChain<TConfig>(name).FirstOrDefault();

    /// <summary>
    /// All configs that apply to <paramref name="name"/>, highest priority first: context
    /// intercepts nearest first, then the metadata of the <c>[Inject]</c> declaration that admits
    /// the access. Analogue of cordis' <c>Service.resolveIntercept</c>; merge them as needed.
    /// </summary>
    /// <typeparam name="TConfig">The config type to look for; layers of other types are skipped.</typeparam>
    /// <param name="name">The service name whose configs are requested.</param>
    /// <returns>A lazily evaluated sequence of the matching configs, highest priority first.</returns>
    public IEnumerable<TConfig> InterceptChain<TConfig>(string name)
    {
        foreach (var config in this.InterceptMap.Chain(name).OfType<TConfig>())
        {
            yield return config;
        }

        if (this.Reflect.Locate(this, name)?.Fiber?.Inject.GetValueOrDefault(name) is TConfig metadata)
        {
            yield return metadata;
        }
    }

    // ------------------------------------------------------------- services

    /// <summary>
    /// Provides a service under <paramref name="name"/> in this context's realm, owned by this
    /// context's fiber. Fibers that inject the name are re-evaluated and may load. Disposing the
    /// returned handle (or unloading the owning fiber) withdraws the service, waiting until every
    /// fiber that loaded against it has finished its own cleanup.
    /// </summary>
    /// <param name="name">The service name.</param>
    /// <param name="value">The service instance; <c>null</c> provides a valueless marker binding.</param>
    /// <param name="check">
    /// An optional availability predicate; when it returns <c>false</c> (or throws), injecting
    /// fibers treat the service as unavailable.
    /// </param>
    /// <returns>The effect that owns the provision.</returns>
    /// <exception cref="CordisException">A service with this name is already registered in this realm.</exception>
    /// <exception cref="InactiveFiberException">This context's fiber has been disposed.</exception>
    public EffectHandle Provide(string name, object? value = null, Func<bool>? check = null)
        => this.Reflect.Provide(this, name, value, check);

    /// <summary>
    /// Provides <paramref name="value"/> as the service <typeparamref name="TService"/>.
    /// See <see cref="Provide(string, object, Func{bool})"/>.
    /// </summary>
    /// <typeparam name="TService">The service type; its name comes from <see cref="ServiceNames.For{T}"/>.</typeparam>
    /// <param name="value">The service instance.</param>
    /// <param name="check">An optional availability predicate.</param>
    /// <returns>The effect that owns the provision.</returns>
    /// <exception cref="CordisException">A service with this name is already registered in this realm.</exception>
    /// <exception cref="InactiveFiberException">This context's fiber has been disposed.</exception>
    public EffectHandle Provide<TService>(TService value, Func<bool>? check = null)
        where TService : class
        => this.Reflect.Provide(this, ServiceNames.For<TService>(), value, check);

    /// <summary>
    /// Replaces the value behind a service this context's fiber already provides, and notifies
    /// fibers that inject it.
    /// </summary>
    /// <param name="name">The service name.</param>
    /// <param name="value">The new service value.</param>
    /// <exception cref="CordisException">The service is not provided in this realm, or it is owned by another fiber.</exception>
    public void Set(string name, object? value) => this.Reflect.Set(this, name, value);

    /// <summary>
    /// Replaces the value behind the service <typeparamref name="TService"/> that this context's
    /// fiber already provides. See <see cref="Set(string, object)"/>.
    /// </summary>
    /// <typeparam name="TService">The service type; its name comes from <see cref="ServiceNames.For{T}"/>.</typeparam>
    /// <param name="value">The new service value.</param>
    /// <exception cref="CordisException">The service is not provided in this realm, or it is owned by another fiber.</exception>
    public void Set<TService>(TService value)
        where TService : class
        => this.Reflect.Set(this, ServiceNames.For<TService>(), value);

    /// <summary>
    /// Resolves the service <paramref name="name"/> for this context. The root context can reach
    /// any active provider; a component context reaches only services that a fiber on its chain
    /// declares in its inject list or provides itself. A value implementing
    /// <see cref="IContextScoped"/> is returned bound to this context.
    /// </summary>
    /// <param name="name">The service name.</param>
    /// <param name="required">Whether to throw when the service is currently unavailable instead of returning <c>null</c>.</param>
    /// <returns>The service value, or <c>null</c> when it is unavailable and <paramref name="required"/> is <c>false</c>.</returns>
    /// <exception cref="ServiceNotInjectedException">A component context accesses a name no fiber on its chain declares or provides.</exception>
    /// <exception cref="ServiceUnavailableException">The service is unavailable and <paramref name="required"/> is <c>true</c>.</exception>
    public object? Get(string name, bool required = true) => this.Reflect.Resolve(this, name, required);

    /// <summary>
    /// Resolves the required service <typeparamref name="TService"/>. See <see cref="Get(string, bool)"/>.
    /// </summary>
    /// <typeparam name="TService">The service type; its name comes from <see cref="ServiceNames.For{T}"/>.</typeparam>
    /// <returns>The service value.</returns>
    /// <exception cref="ServiceNotInjectedException">A component context accesses a name no fiber on its chain declares or provides.</exception>
    /// <exception cref="ServiceUnavailableException">The service is currently unavailable.</exception>
    public TService Get<TService>()
        where TService : class
        => (TService)this.Reflect.Resolve(this, ServiceNames.For<TService>(), true)!;

    /// <summary>
    /// Looks a service up optionally and without checking inject declarations: upstream's
    /// <c>ctx.get(name)</c>. Returns the value of the active provider of <paramref name="name"/>
    /// in this context's realm, or <see langword="null"/> when there is none; it never throws.
    /// Use it to probe for a service a component does not depend on. Declare the service in
    /// <c>Inject</c> instead when the component must reload with it.
    /// </summary>
    /// <param name="name">The service name.</param>
    /// <returns>The service value, or <see langword="null"/> when no active provider exists.</returns>
    public object? Lookup(string name) => this.Reflect.Lookup(this, name);

    /// <summary>
    /// Looks the service <typeparamref name="TService"/> up optionally and without checking
    /// inject declarations. See <see cref="Lookup(string)"/>.
    /// </summary>
    /// <typeparam name="TService">The service type; its name comes from <see cref="ServiceNames.For{T}"/>.</typeparam>
    /// <returns>The service value, or <see langword="null"/> when no active provider exists or it is not a <typeparamref name="TService"/>.</returns>
    public TService? Lookup<TService>()
        where TService : class
        => this.Reflect.Lookup(this, ServiceNames.For<TService>()) as TService;

    /// <summary>
    /// Resolves the service <typeparamref name="TService"/> if it is currently available.
    /// See <see cref="Get(string, bool)"/>.
    /// </summary>
    /// <typeparam name="TService">The service type; its name comes from <see cref="ServiceNames.For{T}"/>.</typeparam>
    /// <returns>The service value, or <c>null</c> when it is unavailable or not a <typeparamref name="TService"/>.</returns>
    /// <exception cref="ServiceNotInjectedException">A component context accesses a name no fiber on its chain declares or provides.</exception>
    public TService? TryGet<TService>()
        where TService : class
        => this.Reflect.Resolve(this, ServiceNames.For<TService>(), false) as TService;

    /// <summary>
    /// Determines whether a provider for <paramref name="name"/> is registered in this context's
    /// realm and its owning fiber is active. Inject declarations are not checked.
    /// </summary>
    /// <param name="name">The service name.</param>
    /// <returns><c>true</c> if an active provider exists; otherwise, <c>false</c>.</returns>
    public bool Has(string name) => this.Reflect.GetImpl(this, name) is not null;

    // ---------------------------------------------------------- components

    /// <summary>
    /// Uses a component type: creates a fiber for it under this context's fiber. The fiber loads
    /// asynchronously once every injected service is available. Analogue of <c>ctx.use()</c>.
    /// </summary>
    /// <param name="type">The component type.</param>
    /// <param name="config">The config handed to the component; it is validated by the component's schema on every load.</param>
    /// <param name="inject">Extra service names to inject, on top of the component's own declaration.</param>
    /// <returns>The new fiber; await it to wait until it settles.</returns>
    /// <exception cref="InactiveFiberException">This context's fiber has been disposed.</exception>
    public Fiber Use(Type type, object? config = null, IEnumerable<string>? inject = null)
        => this.Registry.Use(this, ComponentSource.ForType(type), config, inject);

    /// <summary>
    /// Uses the component type <typeparamref name="TComponent"/>. See <see cref="Use(Type, object, IEnumerable{string})"/>.
    /// </summary>
    /// <typeparam name="TComponent">The component type.</typeparam>
    /// <param name="config">The config handed to the component.</param>
    /// <returns>The new fiber; await it to wait until it settles.</returns>
    /// <exception cref="InactiveFiberException">This context's fiber has been disposed.</exception>
    public Fiber Use<TComponent>(object? config = null) => this.Use(typeof(TComponent), config);

    /// <summary>
    /// Uses a component instance. See <see cref="Use(Type, object, IEnumerable{string})"/>.
    /// </summary>
    /// <param name="component">The component instance.</param>
    /// <param name="config">The config handed to the component.</param>
    /// <returns>The new fiber; await it to wait until it settles.</returns>
    /// <exception cref="InactiveFiberException">This context's fiber has been disposed.</exception>
    public Fiber Use(IComponent component, object? config = null)
        => this.Registry.Use(this, ComponentSource.ForInstance(component), config);

    /// <summary>
    /// Uses a component described by a <see cref="ComponentSource"/>. See <see cref="Use(Type, object, IEnumerable{string})"/>.
    /// </summary>
    /// <param name="source">The component source.</param>
    /// <param name="config">The config handed to the component.</param>
    /// <param name="inject">Extra service names to inject, on top of the component's own declaration.</param>
    /// <returns>The new fiber; await it to wait until it settles.</returns>
    /// <exception cref="InactiveFiberException">This context's fiber has been disposed.</exception>
    public Fiber Use(ComponentSource source, object? config = null, IEnumerable<string>? inject = null)
        => this.Registry.Use(this, source, config, inject);

    /// <summary>
    /// Uses a component type. Obsolete alias of <see cref="Use(Type, object, IEnumerable{string})"/>.
    /// </summary>
    /// <param name="type">The component type.</param>
    /// <param name="config">The config handed to the component.</param>
    /// <param name="inject">Extra service names to inject.</param>
    /// <returns>The new fiber.</returns>
    [Obsolete("Use ctx.Use() instead.")]
    public Fiber Plugin(Type type, object? config = null, IEnumerable<string>? inject = null) => this.Use(type, config, inject);

    /// <summary>
    /// Uses the component type <typeparamref name="TComponent"/>. Obsolete alias of <see cref="Use{TComponent}(object)"/>.
    /// </summary>
    /// <typeparam name="TComponent">The component type.</typeparam>
    /// <param name="config">The config handed to the component.</param>
    /// <returns>The new fiber.</returns>
    [Obsolete("Use ctx.Use() instead.")]
    public Fiber Plugin<TComponent>(object? config = null) => this.Use<TComponent>(config);

    /// <summary>
    /// Uses a component instance. Obsolete alias of <see cref="Use(IComponent, object)"/>.
    /// </summary>
    /// <param name="component">The component instance.</param>
    /// <param name="config">The config handed to the component.</param>
    /// <returns>The new fiber.</returns>
    [Obsolete("Use ctx.Use() instead.")]
    public Fiber Plugin(IComponent component, object? config = null) => this.Use(component, config);

    /// <summary>
    /// Registers an inline component body. The body runs with the fiber's context each time the
    /// fiber loads, that is, whenever every service in <paramref name="inject"/> is available.
    /// </summary>
    /// <param name="name">The component name, used in logs and diagnostics.</param>
    /// <param name="apply">The component body.</param>
    /// <param name="inject">The service names the body depends on.</param>
    /// <returns>The new fiber; await it to wait until it settles.</returns>
    /// <exception cref="InactiveFiberException">This context's fiber has been disposed.</exception>
    public Fiber Apply(string name, Action<Context> apply, params string[] inject)
        => this.ApplyAsync(
            name,
            ctx =>
            {
                apply(ctx);
                return default;
            },
            inject);

    /// <summary>
    /// Registers an inline asynchronous component body. The fiber stays
    /// <see cref="FiberState.Loading"/> until the returned task completes, and a failure fails the load.
    /// </summary>
    /// <param name="name">The component name, used in logs and diagnostics.</param>
    /// <param name="apply">The asynchronous component body.</param>
    /// <param name="inject">The service names the body depends on.</param>
    /// <returns>The new fiber; await it to wait until it settles.</returns>
    /// <exception cref="InactiveFiberException">This context's fiber has been disposed.</exception>
    public Fiber ApplyAsync(string name, Func<Context, ValueTask> apply, params string[] inject)
        => this.Registry.Use(this, ComponentSource.ForDelegate(name, apply, inject));

    /// <summary>
    /// Runs a body only while every named service is available. The component is named after the
    /// delegate's method. Analogue of <c>ctx.inject()</c>.
    /// </summary>
    /// <param name="names">The service names the body depends on.</param>
    /// <param name="apply">The body, run each time the fiber loads.</param>
    /// <returns>The new fiber; await it to wait until it settles.</returns>
    /// <exception cref="InactiveFiberException">This context's fiber has been disposed.</exception>
    public Fiber Inject(string[] names, Action<Context> apply)
        => this.ApplyAsync(
            apply.Method.Name,
            ctx =>
            {
                apply(ctx);
                return default;
            },
            names);

    /// <summary>
    /// Runs an asynchronous body only while every named service is available. The component is
    /// named after the delegate's method. See <see cref="Inject(string[], Action{Context})"/>.
    /// </summary>
    /// <param name="names">The service names the body depends on.</param>
    /// <param name="apply">The asynchronous body, run each time the fiber loads.</param>
    /// <returns>The new fiber; await it to wait until it settles.</returns>
    /// <exception cref="InactiveFiberException">This context's fiber has been disposed.</exception>
    public Fiber InjectAsync(string[] names, Func<Context, ValueTask> apply)
        => this.ApplyAsync(apply.Method.Name, apply, names);

    /// <summary>
    /// Handle config updates of this context's fiber. Call <c>next</c> to let the update proceed
    /// (restarting the fiber); return without calling it to accept the new config in place.
    /// The hook is removed when the fiber unloads. Analogue of <c>ctx.on('internal/update')</c>.
    /// </summary>
    /// <param name="handler">The update hook.</param>
    /// <param name="prepend">Whether to run the hook before the hooks already registered instead of after them.</param>
    /// <returns>The effect that owns the hook; dispose it to remove the hook early.</returns>
    /// <exception cref="InactiveFiberException">This context's fiber has been disposed.</exception>
    public EffectHandle OnUpdate(UpdateHandler handler, bool prepend = false) => this.Fiber.AddUpdateHook(handler, prepend);

    /// <summary>
    /// Accept config updates in place: the fiber is not restarted, and hooks registered after
    /// this one do not run.
    /// </summary>
    /// <typeparam name="TConfig">The resolved config type; the validated config is cast to it.</typeparam>
    /// <param name="accept">Receives the new, validated config.</param>
    /// <param name="prepend">Whether to run the hook before the hooks already registered instead of after them.</param>
    /// <returns>The effect that owns the hook; dispose it to remove the hook early.</returns>
    /// <exception cref="InactiveFiberException">This context's fiber has been disposed.</exception>
    public EffectHandle OnUpdate<TConfig>(Action<TConfig> accept, bool prepend = false)
        => this.Fiber.AddUpdateHook(
            (config, _) =>
            {
                accept((TConfig)config!);
                return Task.CompletedTask;
            },
            prepend);

    // -------------------------------------------------------------- effects

    /// <summary>
    /// Acquires a resource owned by this context's fiber. See <see cref="Fiber.Effect(Func{Action}, string)"/>.
    /// </summary>
    /// <param name="execute">Acquires the resource and returns its cleanup, or <c>null</c> when there is nothing to clean up.</param>
    /// <param name="label">A label identifying the effect in diagnostics.</param>
    /// <returns>The effect handle; dispose it to run the cleanup early.</returns>
    /// <exception cref="InactiveFiberException">This context's fiber has been disposed.</exception>
    public EffectHandle Effect(Func<Action?> execute, string label = "anonymous") => this.Fiber.Effect(execute, label);

    /// <summary>
    /// Acquires a resource asynchronously. See <see cref="Fiber.EffectAsync(Func{ValueTask{Disposer}}, string)"/>.
    /// </summary>
    /// <param name="execute">Acquires the resource and returns its disposer, or <c>null</c> when there is nothing to clean up.</param>
    /// <param name="label">A label identifying the effect in diagnostics.</param>
    /// <returns>The effect handle; dispose it to run the cleanup early.</returns>
    /// <exception cref="InactiveFiberException">This context's fiber has been disposed.</exception>
    public EffectHandle EffectAsync(Func<ValueTask<Disposer?>> execute, string label = "anonymous")
        => this.Fiber.EffectAsync(execute, label);

    /// <summary>
    /// Acquires several resources from an async iterator, one disposer per resource. See
    /// <see cref="Fiber.EffectMany(Func{IAsyncEnumerable{Disposer}}, string)"/>.
    /// </summary>
    /// <param name="execute">Returns an async iterator that yields a disposer for each acquired resource.</param>
    /// <param name="label">A label identifying the effect in diagnostics.</param>
    /// <returns>The effect handle; dispose it to release every collected resource early.</returns>
    /// <exception cref="InactiveFiberException">This context's fiber has been disposed.</exception>
    public EffectHandle EffectMany(Func<IAsyncEnumerable<Disposer>> execute, string label = "anonymous")
        => this.Fiber.EffectMany(execute, label);

    /// <summary>
    /// Ties a disposable resource to this context's fiber: it is disposed when the fiber unloads.
    /// </summary>
    /// <param name="resource">The resource to dispose on unload.</param>
    /// <param name="label">A label identifying the effect in diagnostics; defaults to the resource's type name.</param>
    /// <returns>The effect handle; dispose it to dispose the resource early.</returns>
    /// <exception cref="InactiveFiberException">This context's fiber has been disposed.</exception>
    public EffectHandle Use(IDisposable resource, string? label = null) => this.Fiber.Use(resource, label);

    /// <summary>
    /// Ties an asynchronously disposable resource to this context's fiber: it is disposed when
    /// the fiber unloads.
    /// </summary>
    /// <param name="resource">The resource to dispose on unload.</param>
    /// <param name="label">A label identifying the effect in diagnostics; defaults to the resource's type name.</param>
    /// <returns>The effect handle; dispose it to dispose the resource early.</returns>
    /// <exception cref="InactiveFiberException">This context's fiber has been disposed.</exception>
    public EffectHandle UseAsync(IAsyncDisposable resource, string? label = null) => this.Fiber.UseAsync(resource, label);

    // --------------------------------------------------------------- events

    /// <summary>
    /// Subscribes an asynchronous listener to an event. The listener is owned by this context's
    /// fiber and is removed when the fiber unloads.
    /// </summary>
    /// <typeparam name="TArgs">The event argument type.</typeparam>
    /// <param name="key">The event to listen to.</param>
    /// <param name="handler">The listener; receives the listener's own context and the event arguments.</param>
    /// <param name="prepend">Whether to run the listener before the listeners already registered instead of after them.</param>
    /// <returns>The effect that owns the listener; dispose it to unsubscribe.</returns>
    /// <exception cref="InactiveFiberException">This context's fiber has been disposed.</exception>
    public EffectHandle On<TArgs>(EventKey<TArgs> key, Func<Context, TArgs, ValueTask> handler, bool prepend = false)
        => this.Events.On(this, key, handler, prepend);

    /// <summary>
    /// Subscribes a synchronous listener to an event. The listener is owned by this context's
    /// fiber and is removed when the fiber unloads.
    /// </summary>
    /// <typeparam name="TArgs">The event argument type.</typeparam>
    /// <param name="key">The event to listen to.</param>
    /// <param name="handler">The listener; receives the listener's own context and the event arguments.</param>
    /// <param name="prepend">Whether to run the listener before the listeners already registered instead of after them.</param>
    /// <returns>The effect that owns the listener; dispose it to unsubscribe.</returns>
    /// <exception cref="InactiveFiberException">This context's fiber has been disposed.</exception>
    public EffectHandle On<TArgs>(EventKey<TArgs> key, Action<Context, TArgs> handler, bool prepend = false)
        => this.Events.On(this, key, handler, prepend);

    /// <summary>
    /// Subscribes a listener to a bail event, dispatched by <see cref="BailAsync{TArgs, TResult}(BailKey{TArgs, TResult}, TArgs)"/>.
    /// The listener is owned by this context's fiber and is removed when the fiber unloads.
    /// </summary>
    /// <typeparam name="TArgs">The event argument type.</typeparam>
    /// <typeparam name="TResult">The result type.</typeparam>
    /// <param name="key">The event to listen to.</param>
    /// <param name="handler">The listener; returns a non-null result to stop dispatch, or <c>null</c> to pass.</param>
    /// <param name="prepend">Whether to run the listener before the listeners already registered instead of after them.</param>
    /// <returns>The effect that owns the listener; dispose it to unsubscribe.</returns>
    /// <exception cref="InactiveFiberException">This context's fiber has been disposed.</exception>
    public EffectHandle On<TArgs, TResult>(BailKey<TArgs, TResult> key, Func<Context, TArgs, ValueTask<TResult?>> handler, bool prepend = false)
        => this.Events.On(this, key, handler, prepend);

    /// <summary>
    /// Subscribes a middleware listener to a waterfall event, dispatched by
    /// <see cref="WaterfallAsync{TArgs}(WaterfallKey{TArgs}, TArgs, Func{TArgs, ValueTask{TArgs}})"/>.
    /// The listener is owned by this context's fiber and is removed when the fiber unloads.
    /// </summary>
    /// <typeparam name="TArgs">The value type passed down the chain.</typeparam>
    /// <param name="key">The event to listen to.</param>
    /// <param name="handler">
    /// The middleware; receives its context, the current value and a <c>next</c> continuation it
    /// may call at most once, and returns the (possibly transformed) value.
    /// </param>
    /// <param name="prepend">Whether to run the listener before the listeners already registered instead of after them.</param>
    /// <returns>The effect that owns the listener; dispose it to unsubscribe.</returns>
    /// <exception cref="InactiveFiberException">This context's fiber has been disposed.</exception>
    public EffectHandle On<TArgs>(
        WaterfallKey<TArgs> key,
        Func<Context, TArgs, Func<TArgs, ValueTask<TArgs>>, ValueTask<TArgs>> handler,
        bool prepend = false)
        => this.Events.On(this, key, handler, prepend);

    /// <summary>
    /// Subscribes a listener that unsubscribes itself before handling the first event it receives.
    /// </summary>
    /// <typeparam name="TArgs">The event argument type.</typeparam>
    /// <param name="key">The event to listen to.</param>
    /// <param name="handler">The listener; receives the listener's own context and the event arguments.</param>
    /// <returns>The effect that owns the listener; dispose it to unsubscribe before the event fires.</returns>
    /// <exception cref="InactiveFiberException">This context's fiber has been disposed.</exception>
    public EffectHandle Once<TArgs>(EventKey<TArgs> key, Func<Context, TArgs, ValueTask> handler)
        => this.Events.Once(this, key, handler);

    /// <summary>
    /// Emits an event to the listeners under this context's root, one at a time in registration
    /// order. The first listener failure stops dispatch and propagates.
    /// </summary>
    /// <typeparam name="TArgs">The event argument type.</typeparam>
    /// <param name="key">The event to emit.</param>
    /// <param name="args">The event arguments.</param>
    /// <returns>A task that completes when every listener has run.</returns>
    public ValueTask EmitAsync<TArgs>(EventKey<TArgs> key, TArgs args) => this.Events.EmitAsync(key, args, this.InRealm);

    /// <summary>
    /// Emits an event to the listeners under this context's root concurrently.
    /// </summary>
    /// <typeparam name="TArgs">The event argument type.</typeparam>
    /// <param name="key">The event to emit.</param>
    /// <param name="args">The event arguments.</param>
    /// <returns>A task that completes when every listener has finished.</returns>
    /// <exception cref="AggregateError">One or more listeners failed; their exceptions are aggregated.</exception>
    public ValueTask ParallelAsync<TArgs>(EventKey<TArgs> key, TArgs args) => this.Events.ParallelAsync(key, args, this.InRealm);

    /// <summary>
    /// Emits a bail event: listeners under this context's root run in order until one returns a
    /// non-null result.
    /// </summary>
    /// <typeparam name="TArgs">The event argument type.</typeparam>
    /// <typeparam name="TResult">The result type.</typeparam>
    /// <param name="key">The event to emit.</param>
    /// <param name="args">The event arguments.</param>
    /// <returns>A task whose result is the first non-null listener result, or <c>default</c> when no listener returned one.</returns>
    public ValueTask<TResult?> BailAsync<TArgs, TResult>(BailKey<TArgs, TResult> key, TArgs args)
        => this.Events.BailAsync(key, args, this.InRealm);

    /// <summary>
    /// Emits a waterfall event: the value passes through the listeners under this context's root
    /// as a middleware chain that ends in <paramref name="terminal"/>. Listeners removed during
    /// dispatch are skipped.
    /// </summary>
    /// <typeparam name="TArgs">The value type passed down the chain.</typeparam>
    /// <param name="key">The event to emit.</param>
    /// <param name="args">The initial value.</param>
    /// <param name="terminal">The final step, invoked when the last listener calls <c>next</c> (or immediately when there are no listeners).</param>
    /// <returns>A task whose result is the value returned by the chain.</returns>
    /// <exception cref="InvalidOperationException">A listener called <c>next</c> more than once.</exception>
    public ValueTask<TArgs> WaterfallAsync<TArgs>(WaterfallKey<TArgs> key, TArgs args, Func<TArgs, ValueTask<TArgs>> terminal)
        => this.Events.WaterfallAsync(key, args, terminal, this.InRealm);

    /// <summary>
    /// Disposes this context's fiber. For the root context this tears down the whole tree; for a
    /// component context it disposes the component's fiber, unloading it and its effects.
    /// </summary>
    /// <returns>A task that completes when the fiber has been disposed.</returns>
    public ValueTask DisposeAsync() => this.Fiber.DisposeAsync();

    // ------------------------------------------------------------- scoping
    internal Context ExtendForFiber(Fiber fiber) => new(this, fiber, null, null);

    internal IsolateKey KeyFor(string name)
    {
        if (this.IsolateMap.TryGet(name, out var key))
        {
            return key;
        }

        lock (this.SyncRoot)
        {
            if (!this.Root.IsolateMap.TryGet(name, out key))
            {
                key = new IsolateKey(name);
                this.Root.IsolateMap.Set(name, key);
            }
        }

        return key;
    }

    /// <summary>Default dispatch filter: a listener is reachable if it shares this context's root chain.</summary>
    private bool InRealm(Context listener) => ReferenceEquals(listener.Root, this.Root);
}
