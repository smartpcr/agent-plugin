namespace Cordis;

/// <summary>
/// The dependency-injection container. Child contexts are chained (never copied), so isolate
/// realms and intercept configs layer the same way a JavaScript prototype chain does.
/// </summary>
public sealed class Context : IAsyncDisposable
{
    public Context()
    {
        SyncRoot = new object();
        Root = this;
        Parent = null;
        IsolateMap = new ChainMap<IsolateKey>();
        InterceptMap = new ChainMap<object>();
        Internal = new InternalEvents();
        Loggers = new LoggerService();
        Fiber = new Fiber(this);
        Reflect = new ServiceStore(this);
        Registry = new RegistryService(this);
        Events = new EventsService(this);
        Reflect.Provide(this, LoggerService.ServiceName, Loggers);
    }

    private Context(Context parent, Fiber fiber, ChainMap<IsolateKey>? isolate, ChainMap<object>? intercept)
    {
        SyncRoot = parent.SyncRoot;
        Root = parent.Root;
        Parent = parent;
        IsolateMap = isolate ?? parent.IsolateMap;
        InterceptMap = intercept ?? parent.InterceptMap;
        Internal = parent.Internal;
        Loggers = parent.Loggers;
        Fiber = fiber;
        Reflect = parent.Reflect;
        Registry = parent.Registry;
        Events = parent.Events;
    }

    internal object SyncRoot { get; }

    internal ChainMap<IsolateKey> IsolateMap { get; }

    internal ChainMap<object> InterceptMap { get; }

    public Context Root { get; }

    public Context? Parent { get; }

    public Fiber Fiber { get; }

    public ServiceStore Reflect { get; }

    public RegistryService Registry { get; }

    public EventsService Events { get; }

    public LoggerService Loggers { get; }

    public InternalEvents Internal { get; }

    public Logger Logger => field ??= Loggers.For(this);

    public override string ToString() => $"Context <{Fiber.Name}>";

    // ------------------------------------------------------------- scoping

    internal Context ExtendForFiber(Fiber fiber) => new(this, fiber, null, null);

    /// <summary>A child context where <paramref name="name"/> resolves in a fresh realm.</summary>
    public Context Isolate(string name, IsolateKey? key = null)
    {
        ChainMap<IsolateKey> map = IsolateMap.Extend();
        map.Set(name, key ?? new IsolateKey(name));
        return new Context(this, Fiber, map, null);
    }

    public Context Isolate<TService>(IsolateKey? key = null) => Isolate(ServiceNames.For<TService>(), key);

    /// <summary>A child context carrying an extra config layer for <paramref name="name"/>.</summary>
    public Context Intercept(string name, object config)
    {
        ChainMap<object> map = InterceptMap.Extend();
        map.Set(name, config);
        return new Context(this, Fiber, null, map);
    }

    public Context Intercept<TService>(object config) => Intercept(ServiceNames.For<TService>(), config);

    /// <summary>The highest-priority config for <paramref name="name"/>; see <see cref="InterceptChain{TConfig}"/>.</summary>
    public TConfig? GetInterceptConfig<TConfig>(string name) where TConfig : class
        => InterceptChain<TConfig>(name).FirstOrDefault();

    /// <summary>
    /// All configs that apply to <paramref name="name"/>, highest priority first: context
    /// intercepts nearest first, then the metadata of the <c>[Inject]</c> declaration that admits
    /// the access. Analogue of cordis' <c>Service.resolveIntercept</c>; merge them as needed.
    /// </summary>
    public IEnumerable<TConfig> InterceptChain<TConfig>(string name)
    {
        foreach (TConfig? config in InterceptMap.Chain(name).OfType<TConfig>()) yield return config;
        if (Reflect.Locate(this, name)?.Fiber?.Inject.GetValueOrDefault(name) is TConfig metadata) yield return metadata;
    }

    internal IsolateKey KeyFor(string name)
    {
        if (IsolateMap.TryGet(name, out IsolateKey? key)) return key;
        lock (SyncRoot)
        {
            if (!Root.IsolateMap.TryGet(name, out key))
            {
                key = new IsolateKey(name);
                Root.IsolateMap.Set(name, key);
            }
        }
        return key;
    }

    // ------------------------------------------------------------- services

    public EffectHandle Provide(string name, object? value = null, Func<bool>? check = null)
        => Reflect.Provide(this, name, value, check);

    public EffectHandle Provide<TService>(TService value, Func<bool>? check = null) where TService : class
        => Reflect.Provide(this, ServiceNames.For<TService>(), value, check);

    public void Set(string name, object? value) => Reflect.Set(this, name, value);

    public void Set<TService>(TService value) where TService : class
        => Reflect.Set(this, ServiceNames.For<TService>(), value);

    public object? Get(string name, bool required = true) => Reflect.Resolve(this, name, required);

    public TService Get<TService>() where TService : class
        => (TService)Reflect.Resolve(this, ServiceNames.For<TService>(), true)!;

    public TService? TryGet<TService>() where TService : class
        => Reflect.Resolve(this, ServiceNames.For<TService>(), false) as TService;

    public bool Has(string name) => Reflect.GetImpl(this, name) is not null;

    // ---------------------------------------------------------- components

    /// <summary>Use a component type. Analogue of <c>ctx.use()</c>.</summary>
    public Fiber Use(Type type, object? config = null, IEnumerable<string>? inject = null)
        => Registry.Use(this, ComponentSource.ForType(type), config, inject);

    public Fiber Use<TComponent>(object? config = null) => Use(typeof(TComponent), config);

    public Fiber Use(IComponent component, object? config = null)
        => Registry.Use(this, ComponentSource.ForInstance(component), config);

    public Fiber Use(ComponentSource source, object? config = null, IEnumerable<string>? inject = null)
        => Registry.Use(this, source, config, inject);

    [Obsolete("Use ctx.Use() instead.")]
    public Fiber Plugin(Type type, object? config = null, IEnumerable<string>? inject = null) => Use(type, config, inject);

    [Obsolete("Use ctx.Use() instead.")]
    public Fiber Plugin<TComponent>(object? config = null) => Use<TComponent>(config);

    [Obsolete("Use ctx.Use() instead.")]
    public Fiber Plugin(IComponent component, object? config = null) => Use(component, config);

    /// <summary>Register an inline component body.</summary>
    public Fiber Apply(string name, Action<Context> apply, params string[] inject)
        => ApplyAsync(name, ctx => { apply(ctx); return default; }, inject);

    public Fiber ApplyAsync(string name, Func<Context, ValueTask> apply, params string[] inject)
        => Registry.Use(this, ComponentSource.ForDelegate(name, apply, inject));

    /// <summary>Run a body only while every named service is available. Analogue of <c>ctx.inject()</c>.</summary>
    public Fiber Inject(string[] names, Action<Context> apply)
        => ApplyAsync(apply.Method.Name, ctx => { apply(ctx); return default; }, names);

    public Fiber InjectAsync(string[] names, Func<Context, ValueTask> apply)
        => ApplyAsync(apply.Method.Name, apply, names);

    /// <summary>
    /// Handle config updates of this context's fiber. Call <c>next</c> to let the update proceed
    /// (restarting the fiber); return without calling it to accept the new config in place.
    /// The hook is removed when the fiber unloads. Analogue of <c>ctx.on('internal/update')</c>.
    /// </summary>
    public EffectHandle OnUpdate(UpdateHandler handler, bool prepend = false) => Fiber.AddUpdateHook(handler, prepend);

    /// <summary>Accept config updates in place: the fiber is not restarted.</summary>
    public EffectHandle OnUpdate<TConfig>(Action<TConfig> accept, bool prepend = false)
        => Fiber.AddUpdateHook((config, _) => { accept((TConfig)config!); return Task.CompletedTask; }, prepend);

    // -------------------------------------------------------------- effects

    public EffectHandle Effect(Func<Action?> execute, string label = "anonymous") => Fiber.Effect(execute, label);

    public EffectHandle EffectAsync(Func<ValueTask<Disposer?>> execute, string label = "anonymous")
        => Fiber.EffectAsync(execute, label);

    public EffectHandle EffectMany(Func<IAsyncEnumerable<Disposer>> execute, string label = "anonymous")
        => Fiber.EffectMany(execute, label);

    public EffectHandle Use(IDisposable resource, string? label = null) => Fiber.Use(resource, label);

    public EffectHandle UseAsync(IAsyncDisposable resource, string? label = null) => Fiber.UseAsync(resource, label);

    // --------------------------------------------------------------- events

    public EffectHandle On<TArgs>(EventKey<TArgs> key, Func<Context, TArgs, ValueTask> handler, bool prepend = false)
        => Events.On(this, key, handler, prepend);

    public EffectHandle On<TArgs>(EventKey<TArgs> key, Action<Context, TArgs> handler, bool prepend = false)
        => Events.On(this, key, handler, prepend);

    public EffectHandle On<TArgs, TResult>(BailKey<TArgs, TResult> key, Func<Context, TArgs, ValueTask<TResult?>> handler, bool prepend = false)
        => Events.On(this, key, handler, prepend);

    public EffectHandle On<TArgs>(
        WaterfallKey<TArgs> key,
        Func<Context, TArgs, Func<TArgs, ValueTask<TArgs>>, ValueTask<TArgs>> handler,
        bool prepend = false)
        => Events.On(this, key, handler, prepend);

    public EffectHandle Once<TArgs>(EventKey<TArgs> key, Func<Context, TArgs, ValueTask> handler)
        => Events.Once(this, key, handler);

    public ValueTask EmitAsync<TArgs>(EventKey<TArgs> key, TArgs args) => Events.EmitAsync(key, args, InRealm);

    public ValueTask ParallelAsync<TArgs>(EventKey<TArgs> key, TArgs args) => Events.ParallelAsync(key, args, InRealm);

    public ValueTask<TResult?> BailAsync<TArgs, TResult>(BailKey<TArgs, TResult> key, TArgs args)
        => Events.BailAsync(key, args, InRealm);

    public ValueTask<TArgs> WaterfallAsync<TArgs>(WaterfallKey<TArgs> key, TArgs args, Func<TArgs, ValueTask<TArgs>> terminal)
        => Events.WaterfallAsync(key, args, terminal, InRealm);

    /// <summary>Default dispatch filter: a listener is reachable if it shares this context's root chain.</summary>
    private bool InRealm(Context listener) => ReferenceEquals(listener.Root, Root);

    public ValueTask DisposeAsync() => Fiber.DisposeAsync();
}
