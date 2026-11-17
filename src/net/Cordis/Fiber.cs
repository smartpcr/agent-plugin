// -----------------------------------------------------------------------
// <copyright file="Fiber.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

using System.Runtime.CompilerServices;
using System.Runtime.ExceptionServices;
using System.Text;

/// <summary>
/// One live instantiation of a component. Owns the effects the component created, tracks the
/// identity of every injected provider as an "epoch", and reloads/unloads itself whenever
/// that epoch changes. Transitions are serialised by the inertia lock.
/// </summary>
public sealed class Fiber : IAsyncDisposable
{
    internal const string InactiveEpoch = "\u0000inactive";

    private readonly Dictionary<string, ServiceImpl> candidates = new(StringComparer.Ordinal);
    private readonly List<EffectHandle> effects = [];
    private readonly List<Task> loadTasks = [];
    private readonly List<UpdateHook> updateHooks = [];
    private readonly EffectHandle? anchor;
    private Dictionary<string, object?> inject;
    private string epoch = InactiveEpoch;
    private int revision;
    private Task? inertia;
    private TaskCompletionSource? release;

    /// <summary>Initializes a new instance of the <see cref="Fiber"/> class. Root fiber: always active, owns nothing but the top-level effects.</summary>
    internal Fiber(Context root)
    {
        this.Gate = root.SyncRoot;
        this.Parent = root;
        this.Ctx = root;
        this.Uid = 0;
        this.inject = new Dictionary<string, object?>(StringComparer.Ordinal);
        this.ExtraInject = [];
        this.epoch = "0";
        this.State = FiberState.Active;
        this.Committed = new Dictionary<string, ServiceImpl>(StringComparer.Ordinal);
    }

    internal Fiber(
        Context parent,
        ComponentRuntime runtime,
        object? config,
        Dictionary<string, object?> inject,
        IReadOnlyList<string> extraInject)
    {
        this.Gate = parent.SyncRoot;
        this.Parent = parent;
        this.Runtime = runtime;
        this.Config = config;
        this.ExtraInject = extraInject;
        this.inject = inject;
        this.Uid = parent.Registry.NextUid();
        this.Ctx = parent.ExtendForFiber(this);

        parent.Internal.RaisePluginChanged(this);

        this.anchor = parent.Fiber.EffectSync(
            () =>
            {
                try
                {
                    // Joining the runtime (where provider notifications reach the fiber) and
                    // reading the providers happen under one lock, so a provider that activates
                    // in between cannot be missed.
                    lock (this.Gate)
                    {
                        runtime.Attach(this);
                        foreach (var name in this.inject.Keys)
                        {
                            this.CheckImpl(name);
                        }

                        this.Refresh();
                    }
                }
                catch (Exception error)
                {
                    this.Ctx.Logger.Error(error);
                    lock (this.Gate)
                    {
                        this.Error = error;
                        this.UpdateState(static () => null);
                    }
                }

                return async () =>
                {
                    lock (this.Gate)
                    {
                        this.Uid = null;
                    }

                    parent.Internal.RaisePluginChanged(this);

                    lock (this.Gate)
                    {
                        var current = this.Runtime!;
                        current.Detach(this);
                        if (current.FiberCount == 0)
                        {
                            parent.Registry.Unregister(current);
                        }

                        this.SetEpoch(InactiveEpoch);
                    }

                    // `inertia` never faults: reload and unload both log their own work errors.
                    while (true)
                    {
                        Task? inertia;
                        lock (this.Gate)
                        {
                            inertia = this.inertia;
                        }

                        if (inertia is null)
                        {
                            break;
                        }

                        try
                        {
                            await inertia.ConfigureAwait(false);
                        }
                        catch
                        { /* reported by the transition itself */
                        }
                    }

                    // SetEpoch above returns early for a fiber that was not active, so the cached
                    // state is not refreshed. Re-derive it now that Uid is null.
                    lock (this.Gate)
                    {
                        this.UpdateState(static () => null);
                    }
                };
            },
            "ctx.Use()");
    }

    /// <summary>
    /// Gets the fiber's incrementing identity, assigned by the registry (the root fiber is 0);
    /// <c>null</c> once the fiber is disposed.
    /// </summary>
    public int? Uid { get; private set; }

    /// <summary>Gets the context this fiber was created from (the root context for the root fiber).</summary>
    public Context Parent { get; }

    /// <summary>Gets the context handed to the component body; it is owned by this fiber.</summary>
    public Context Ctx { get; }

    /// <summary>
    /// Gets the component behind this fiber, or <c>null</c> for the root fiber. Changes on
    /// <see cref="RegistryService.Replace(object, object)"/>.
    /// </summary>
    public ComponentRuntime? Runtime { get; private set; }

    /// <summary>
    /// Gets the config as it was handed in (not validated). It is resolved through the component's
    /// schema on every load, so validation failures surface as a failed load.
    /// </summary>
    public object? Config { get; private set; }

    /// <summary>
    /// Gets the current lifecycle state. Changes are reported through
    /// <see cref="InternalEvents.StatusChanged"/>.
    /// </summary>
    public FiberState State { get; private set; } = FiberState.Pending;

    /// <summary>
    /// Gets the exception thrown by the last failed load, or <c>null</c>. While it is set the fiber
    /// is <see cref="FiberState.Failed"/> and does not reload on dependency changes;
    /// <see cref="UpdateAsync(object, bool)"/> or a component replacement clears it.
    /// </summary>
    public Exception? Error { get; private set; }

    /// <summary>
    /// Gets the injected service names mapped to their per-injection metadata (the component's
    /// declaration merged with names added at the use site). The fiber loads only while every
    /// name has a usable provider.
    /// </summary>
    public IReadOnlyDictionary<string, object?> Inject => this.inject;

    /// <summary>
    /// Gets the object returned by the component's last load, or <c>null</c> while unloaded. If it
    /// is disposable it is disposed when the fiber unloads.
    /// </summary>
    public object? Instance { get; private set; }

    /// <summary>
    /// Gets the name of this fiber's component; a fiber without a component takes the name of
    /// the nearest ancestor that has one, and the root fiber is named <c>root</c>.
    /// </summary>
    public string Name
    {
        get
        {
            var fiber = this;
            while (fiber.Runtime is null)
            {
                var parent = fiber.Parent.Fiber;
                if (ReferenceEquals(parent, fiber))
                {
                    return "root";
                }

                fiber = parent;
            }

            return fiber.Runtime.Name;
        }
    }

    /// <summary>Gets a snapshot of the effects currently owned by this fiber.</summary>
    public IReadOnlyList<EffectHandle> Effects
    {
        get
        {
            lock (this.Gate)
            {
                return [.. this.effects];
            }
        }
    }

    internal object Gate { get; }

    /// <summary>Gets dependencies added at the use site (on top of the component's own declaration).</summary>
    internal IReadOnlyList<string> ExtraInject { get; }

    /// <summary>Gets the bindings this fiber loaded against; <c>null</c> while unloaded.</summary>
    internal Dictionary<string, ServiceImpl>? Committed { get; private set; }

    /// <summary>Gets bindings this fiber provides. Read under <see cref="Gate"/>.</summary>
    internal HashSet<ServiceImpl> ProvisionsUnsafe { get; } = [];

    /// <inheritdoc/>
    public override string ToString() => $"Fiber <{this.Name}> ({this.State})";

    // ---------------------------------------------------------------- effects

    /// <summary>
    /// Ensures the fiber has not been disposed; called before creating effects and before
    /// restarting or updating the fiber.
    /// </summary>
    /// <exception cref="InactiveFiberException">The fiber has been disposed.</exception>
    public void AssertActive()
    {
        if (this.Uid is null)
        {
            throw new InactiveFiberException();
        }
    }

    /// <summary>
    /// Acquire a resource; the returned cleanup runs when the fiber unloads. If
    /// <paramref name="execute"/> throws, the effect is disposed and the exception propagates.
    /// </summary>
    /// <param name="execute">Acquires the resource and returns its cleanup, or <c>null</c> when there is nothing to clean up.</param>
    /// <param name="label">A label identifying the effect in diagnostics.</param>
    /// <returns>The effect handle; dispose it to run the cleanup early.</returns>
    /// <exception cref="InactiveFiberException">The fiber has been disposed.</exception>
    public EffectHandle Effect(Func<Action?> execute, string label = "anonymous")
    {
        return this.Run(label, handle =>
        {
            var cleanup = execute();
            if (cleanup is not null)
            {
                handle.Collect(() =>
                {
                    cleanup();
                    return default;
                });
            }

            return null;
        });
    }

    /// <summary>
    /// Async acquisition. When started while the fiber is loading, the fiber stays
    /// <see cref="FiberState.Loading"/> until it completes and its failure fails the load; when
    /// started later, a failure is logged and the effect is disposed.
    /// </summary>
    /// <param name="execute">Acquires the resource and returns its disposer, or <c>null</c> when there is nothing to clean up.</param>
    /// <param name="label">A label identifying the effect in diagnostics.</param>
    /// <returns>The effect handle, returned without waiting for the acquisition; dispose it to run the cleanup early.</returns>
    /// <exception cref="InactiveFiberException">The fiber has been disposed.</exception>
    public EffectHandle EffectAsync(Func<ValueTask<Disposer?>> execute, string label = "anonymous")
    {
        return this.Run(label, Body);

        async Task Body(EffectHandle handle) => handle.Collect(await execute().ConfigureAwait(false));
    }

    /// <summary>
    /// Generator form: yield one disposer per acquired resource. The analogue of cordis'
    /// <c>function*</c> effects. Pass an iterator local function. Once the effect is disposed,
    /// the iterator is not advanced again: the pending step is collected and the iterator is
    /// closed, so its <c>finally</c> blocks run. Loading and failure behave as for
    /// <see cref="EffectAsync(Func{ValueTask{Disposer}}, string)"/>.
    /// </summary>
    /// <param name="execute">Returns an async iterator that yields a disposer for each acquired resource.</param>
    /// <param name="label">A label identifying the effect in diagnostics.</param>
    /// <returns>The effect handle; dispose it to release every collected resource early.</returns>
    /// <exception cref="InactiveFiberException">The fiber has been disposed.</exception>
    public EffectHandle EffectMany(Func<IAsyncEnumerable<Disposer>> execute, string label = "anonymous")
    {
        return this.Run(label, Body);

        async Task Body(EffectHandle handle)
        {
            var iterator = execute().GetAsyncEnumerator();
            await using (iterator.ConfigureAwait(false))
            {
                while (handle.IsAlive && await iterator.MoveNextAsync().ConfigureAwait(false))
                {
                    handle.Collect(iterator.Current);
                }
            }
        }
    }

    /// <summary>
    /// Ties a disposable resource to this fiber: it is disposed when the fiber unloads.
    /// </summary>
    /// <param name="resource">The resource to dispose on unload.</param>
    /// <param name="label">A label identifying the effect in diagnostics; defaults to the resource's type name.</param>
    /// <returns>The effect handle; dispose it to dispose the resource early.</returns>
    /// <exception cref="InactiveFiberException">The fiber has been disposed.</exception>
    public EffectHandle Use(IDisposable resource, string? label = null)
        => this.EffectSync(
            () => () =>
            {
                resource.Dispose();
                return default;
            },
            label ?? $"use({resource.GetType().Name})");

    /// <summary>
    /// Ties an asynchronously disposable resource to this fiber: it is disposed when the fiber unloads.
    /// </summary>
    /// <param name="resource">The resource to dispose on unload.</param>
    /// <param name="label">A label identifying the effect in diagnostics; defaults to the resource's type name.</param>
    /// <returns>The effect handle; dispose it to dispose the resource early.</returns>
    /// <exception cref="InactiveFiberException">The fiber has been disposed.</exception>
    public EffectHandle UseAsync(IAsyncDisposable resource, string? label = null)
        => this.EffectSync(() => resource.DisposeAsync, label ?? $"use({resource.GetType().Name})");

    // ------------------------------------------------------------- public API

    /// <summary>
    /// Wait until the fiber settles (no load or unload transition is in flight); rethrows the
    /// load error if it failed. A fiber still waiting for dependencies settles as
    /// <see cref="FiberState.Pending"/>.
    /// </summary>
    /// <returns>A task that completes with this fiber once it has settled, or faults with <see cref="Error"/> if the load failed.</returns>
    public async Task<Fiber> WaitAsync()
    {
        while (true)
        {
            Task? inertia;
            lock (this.Gate)
            {
                inertia = this.inertia;
            }

            if (inertia is null)
            {
                break;
            }

            await inertia.ConfigureAwait(false);
        }

        if (this.Error is not null)
        {
            ExceptionDispatchInfo.Capture(this.Error).Throw();
        }

        return this;
    }

    /// <summary>
    /// Gets an awaiter so that <c>await ctx.Use(...)</c> resolves once the fiber settles. See
    /// <see cref="WaitAsync"/>.
    /// </summary>
    /// <returns>An awaiter for <see cref="WaitAsync"/>.</returns>
    public TaskAwaiter<Fiber> GetAwaiter() => this.WaitAsync().GetAwaiter();

    /// <summary>
    /// Reload the component with its current config. A restart requested while a load is in
    /// flight is never lost: the reload revision is part of the epoch.
    /// </summary>
    /// <returns>A task that completes when the fiber has settled again, or faults with the load error if the reload failed.</returns>
    /// <exception cref="InactiveFiberException">The fiber has been disposed.</exception>
    public async Task RestartAsync()
    {
        this.AssertActive();
        lock (this.Gate)
        {
            this.revision++;
            this.Refresh();
        }

        await this.WaitAsync().ConfigureAwait(false);
    }

    /// <summary>
    /// Replace the config. If the component is loaded, the new config is validated first (an
    /// invalid config throws and changes nothing), then passed through the fiber's update hooks
    /// (<see cref="Context.OnUpdate(UpdateHandler, bool)"/>), which may accept it in place; otherwise the fiber
    /// restarts. Unless <paramref name="noSave"/> is set, <see cref="InternalEvents.Committing"/>
    /// fires with the original config so a loader can persist it. Any previous load error is cleared.
    /// </summary>
    /// <param name="config">The new config, as handed in (before validation).</param>
    /// <param name="noSave">Whether to skip raising <see cref="InternalEvents.Committing"/>.</param>
    /// <returns>A task that completes when the update hooks have accepted the config or the fiber has restarted and settled.</returns>
    /// <exception cref="InactiveFiberException">The fiber has been disposed.</exception>
    /// <exception cref="ConfigValidationException">The fiber is loaded and the component's schema rejects <paramref name="config"/>.</exception>
    public async Task UpdateAsync(object? config, bool noSave = false)
    {
        this.AssertActive();
        bool loaded;
        lock (this.Gate)
        {
            loaded = this.epoch != InactiveEpoch;
        }

        var resolved = loaded ? this.ResolveConfig(config) : null;
        if (!noSave)
        {
            this.Ctx.Internal.RaiseCommitting(this, config);
        }

        lock (this.Gate)
        {
            this.Config = config;
            this.Error = null;
        }

        if (loaded)
        {
            await this.RunUpdateHooks(resolved, this.RestartAsync).ConfigureAwait(false);
        }
        else
        {
            await this.RestartAsync().ConfigureAwait(false);
        }
    }

    /// <summary>
    /// Disposes the fiber. A component fiber is removed from its parent: it unloads (waiting for
    /// dependents and disposing its effects), becomes <see cref="FiberState.Disposed"/>, and its
    /// component is unregistered once no fibers remain. The root fiber disposes all of its effects
    /// in reverse order, tearing down the whole tree; disposal errors are logged, not thrown.
    /// </summary>
    /// <returns>A task that completes when disposal has finished.</returns>
    public async ValueTask DisposeAsync()
    {
        if (this.anchor is not null)
        {
            await this.anchor.DisposeAsync().ConfigureAwait(false);
            return;
        }

        // root fiber: tear the whole tree down
        EffectHandle[] effects;
        lock (this.Gate)
        {
            effects = [.. this.effects];
            this.effects.Clear();
        }

        for (var i = effects.Length - 1; i >= 0; i--)
        {
            try
            {
                await effects[i].DisposeAsync().ConfigureAwait(false);
            }
            catch (Exception error)
            {
                this.Ctx.Logger.Error(error);
            }
        }
    }

    internal EffectHandle EffectSync(Func<Disposer?> execute, string label)
        => this.Run(label, handle =>
        {
            handle.Collect(execute());
            return null;
        });

    internal void Detach(EffectHandle handle)
    {
        lock (this.Gate)
        {
            this.effects.Remove(handle);
        }
    }

    internal void AttachDisposer(Disposer disposer, string label)
    {
        var handle = new EffectHandle(this, label);
        lock (this.Gate)
        {
            this.effects.Add(handle);
        }

        handle.Collect(disposer);
    }

    // ---------------------------------------------------------- update hooks
    internal EffectHandle AddUpdateHook(UpdateHandler handler, bool prepend)
    {
        return this.EffectSync(
            () =>
            {
                var hook = new UpdateHook(handler);
                lock (this.Gate)
                {
                    if (prepend)
                    {
                        this.updateHooks.Insert(0, hook);
                    }
                    else
                    {
                        this.updateHooks.Add(hook);
                    }
                }

                return () =>
                {
                    lock (this.Gate)
                    {
                        hook.Removed = true;
                        this.updateHooks.Remove(hook);
                    }

                    return default;
                };
            },
            "ctx.OnUpdate()");
    }

    // ------------------------------------------------------- dependency graph
    internal void AddProvision(ServiceImpl impl)
    {
        lock (this.Gate)
        {
            this.ProvisionsUnsafe.Add(impl);
        }
    }

    internal void RemoveProvision(ServiceImpl impl)
    {
        lock (this.Gate)
        {
            this.ProvisionsUnsafe.Remove(impl);
        }
    }

    /// <summary>Wakes this fiber's unload if it is waiting for dependents to let go.</summary>
    internal void SignalRelease()
    {
        lock (this.Gate)
        {
            this.release?.TrySetResult();
        }
    }

    /// <summary>Re-evaluate whether <paramref name="name"/> currently has a usable provider.</summary>
    internal void CheckImpl(string name)
    {
        var impl = this.Ctx.Reflect.GetImpl(this.Ctx, name, strict: true);
        if (impl is null)
        {
            lock (this.Gate)
            {
                this.candidates.Remove(name);
            }

            return;
        }

        if (impl.Check is not null)
        {
            bool ok;
            try
            {
                ok = impl.Check();
            }
            catch (Exception error)
            {
                impl.Ctx.Logger.Error(error);
                ok = false;
            }

            if (!ok)
            {
                lock (this.Gate)
                {
                    this.candidates.Remove(name);
                }

                return;
            }
        }

        lock (this.Gate)
        {
            this.candidates[name] = impl;
        }
    }

    /// <summary>
    /// Recompute the epoch: the reload revision followed by the identity of every injected
    /// binding. Must be called under <see cref="Gate"/>.
    /// </summary>
    internal void Refresh()
    {
        if (this.Uid is null)
        {
            return;
        }

        var builder = new StringBuilder().Append(this.revision);
        foreach (var name in this.inject.Keys)
        {
            if (!this.candidates.TryGetValue(name, out var impl))
            {
                this.SetEpoch(InactiveEpoch);
                return;
            }

            builder.Append(':').Append(impl.Uid);
        }

        this.SetEpoch(builder.ToString());
    }

    /// <summary>Swap in a replacement component. See <see cref="RegistryService.Replace"/>.</summary>
    internal void Replace(ComponentRuntime runtime, Dictionary<string, object?> inject)
    {
        lock (this.Gate)
        {
            if (!ReferenceEquals(runtime, this.Runtime))
            {
                this.Runtime!.Detach(this);
                runtime.Attach(this);
                this.Runtime = runtime;
            }

            this.inject = inject;
            this.Ctx.Internal.RaiseReplaced(this);
            this.candidates.Clear();
            foreach (var name in this.inject.Keys)
            {
                this.CheckImpl(name);
            }

            this.Error = null;
            this.revision++;
            this.Refresh();

            // an unchanged epoch starts no transition, but a cleared error still changes the state
            if (this.inertia is null)
            {
                this.UpdateState(static () => null);
            }
        }
    }

    private EffectHandle Run(string label, Func<EffectHandle, Task?> body)
    {
        this.AssertActive();
        var handle = new EffectHandle(this, label);
        lock (this.Gate)
        {
            this.effects.Add(handle);
        }

        Task? task;
        try
        {
            task = body(handle);
        }
        catch
        {
            _ = handle.DisposeAsync();
            throw;
        }

        if (task is null || task.IsCompletedSuccessfully)
        {
            return handle;
        }

        if (task.IsFaulted)
        {
            _ = handle.DisposeAsync();
            ExceptionDispatchInfo.Capture(task.Exception!.InnerException ?? task.Exception).Throw();
        }

        handle.SetPending(task);

        // An async effect started by the component body keeps the fiber LOADING until it settles,
        // and its failure fails the load. Effects started later (from a callback) cannot.
        lock (this.Gate)
        {
            if (this.State == FiberState.Loading)
            {
                this.loadTasks.Add(task);
                return handle;
            }
        }

        _ = task.ContinueWith(
            faulted =>
            {
                this.Ctx.Logger.Error(faulted.Exception!.InnerException ?? faulted.Exception);
                _ = handle.DisposeAsync();
            },
            CancellationToken.None,
            TaskContinuationOptions.OnlyOnFaulted,
            TaskScheduler.Default);

        return handle;
    }

    private Task RunUpdateHooks(object? resolved, Func<Task> terminal)
    {
        UpdateHook[] hooks;
        lock (this.Gate)
        {
            hooks = [.. this.updateHooks];
        }

        var index = 0;
        return Dispatch();

        Task Dispatch()
        {
            while (index < hooks.Length && hooks[index].Removed)
            {
                index++;
            }

            if (index >= hooks.Length)
            {
                return terminal();
            }

            var hook = hooks[index++];
            var called = false;
            return hook.Handler(resolved, () =>
            {
                if (called)
                {
                    throw new InvalidOperationException("next() called multiple times");
                }

                called = true;
                return Dispatch();
            });
        }
    }

    private void SetEpoch(string epoch)
    {
        var old = this.epoch;
        if (epoch == old)
        {
            return;
        }

        // a failed fiber only recovers through UpdateAsync() or a replacement, which clear the error
        if (this.Error is not null)
        {
            return;
        }

        this.epoch = epoch;

        // inertia lock: a transition is already running; it reconciles on completion.
        if (this.inertia is not null)
        {
            return;
        }

        this.UpdateState(() =>
        {
            if (epoch != InactiveEpoch && old == InactiveEpoch)
            {
                this.inertia = this.ReloadAsync();
                return FiberState.Loading;
            }

            this.inertia = this.UnloadAsync();
            return FiberState.Unloading;
        });
    }

    private FiberState ComputeState()
    {
        if (this.Uid is null)
        {
            return FiberState.Disposed;
        }

        if (this.Error is not null)
        {
            return FiberState.Failed;
        }

        return this.epoch != InactiveEpoch ? FiberState.Active : FiberState.Pending;
    }

    private void UpdateState(Func<FiberState?> transition)
    {
        var old = this.State;
        this.State = transition() ?? this.ComputeState();
        if (old == this.State)
        {
            return;
        }

        this.Ctx.Internal.RaiseStatusChanged(this, old);

        // only (de)activation is observable by dependents
        if (old != FiberState.Active && this.State != FiberState.Active)
        {
            return;
        }

        foreach (var impl in this.ProvisionsUnsafe.ToArray())
        {
            if (this.Ctx.Reflect.IsCurrent(impl))
            {
                this.Ctx.Reflect.Notify(impl.Name, impl.Isolate);
            }
        }
    }

    private async Task ReloadAsync()
    {
        await Task.Yield(); // mirror cordis: the component body never runs inside ctx.Use()

        string epoch;
        lock (this.Gate)
        {
            this.Committed = new Dictionary<string, ServiceImpl>(this.candidates, StringComparer.Ordinal);
            foreach (var impl in this.Committed.Values)
            {
                if (!ReferenceEquals(impl.Fiber, this))
                {
                    impl.Dependents.Add(this);
                }
            }

            epoch = this.epoch;
        }

        try
        {
            await this.ExecuteAsync().ConfigureAwait(false);
        }
        catch (Exception error)
        {
            this.Ctx.Logger.Error(error);
            lock (this.Gate)
            {
                // A superseded load still needs cleanup, but cannot fail the new target.
                if (this.epoch == epoch)
                {
                    this.Error = error;
                    this.epoch = InactiveEpoch;
                }
            }
        }

        lock (this.Gate)
        {
            this.UpdateState(() =>
            {
                if (this.epoch == epoch)
                {
                    this.inertia = null;
                    return null;
                }

                this.inertia = this.UnloadAsync();
                return FiberState.Unloading;
            });
        }
    }

    private async Task UnloadAsync()
    {
        // Transitions are started from inside `lock (Gate)`. Yielding first guarantees the
        // synchronous prefix ends there, so no await ever runs while the caller holds the lock.
        await Task.Yield();

        // Dependents are notified when this fiber leaves ACTIVE, but may still use its services
        // during their own teardown, so this fiber's effects are disposed only after every
        // dependent has unloaded.
        while (true)
        {
            Task wait;
            lock (this.Gate)
            {
                if (!this.ProvisionsUnsafe.Any(impl => impl.Dependents.Count > 0))
                {
                    this.release = null;
                    break;
                }

                this.release = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
                wait = this.release.Task;
            }

            await wait.ConfigureAwait(false);
        }

        EffectHandle[] effects;
        lock (this.Gate)
        {
            effects = [.. this.effects];
            this.effects.Clear();
            this.loadTasks.Clear();
        }

        for (var i = effects.Length - 1; i >= 0; i--)
        {
            try
            {
                await effects[i].DisposeAsync().ConfigureAwait(false);
            }
            catch (Exception error)
            {
                this.Ctx.Logger.Error(error);
            }
        }

        lock (this.Gate)
        {
            // release the providers waiting for this fiber to unload
            foreach (var impl in this.Committed?.Values ?? Enumerable.Empty<ServiceImpl>())
            {
                if (!impl.Dependents.Remove(this) || impl.Dependents.Count > 0)
                {
                    continue;
                }

                impl.Release?.TrySetResult();
                impl.Fiber.SignalRelease();
            }

            this.Committed = null;
            this.Instance = null;
            this.UpdateState(() =>
            {
                if (this.epoch == InactiveEpoch)
                {
                    this.inertia = null;
                    return null;
                }

                this.inertia = this.ReloadAsync();
                return FiberState.Loading;
            });
        }
    }

    private object? ResolveConfig(object? config)
        => this.Runtime?.Schema is { } schema ? schema.Validate(config) : config;

    private async Task ExecuteAsync()
    {
        object? config;
        lock (this.Gate)
        {
            config = this.Config;
        }

        var instance = await this.Runtime!.Apply(this.Ctx, this.ResolveConfig(config)).ConfigureAwait(false);
        lock (this.Gate)
        {
            this.Instance = instance;
        }

        switch (instance)
        {
            case IAsyncDisposable asyncDisposable:
                this.AttachDisposer(asyncDisposable.DisposeAsync, $"dispose({instance.GetType().Name})");
                break;
            case IDisposable disposable:
                this.AttachDisposer(
                    () =>
                    {
                        disposable.Dispose();
                        return default;
                    },
                    $"dispose({instance.GetType().Name})");
                break;
        }

        if (instance is IAsyncInit init)
        {
            await init.InitAsync().ConfigureAwait(false);
        }

        // drain async effects registered during the load (they may register more)
        while (true)
        {
            Task[] pending;
            lock (this.Gate)
            {
                if (this.loadTasks.Count == 0)
                {
                    break;
                }

                pending = [.. this.loadTasks];
                this.loadTasks.Clear();
            }

            await Task.WhenAll(pending).ConfigureAwait(false);
        }
    }

    private sealed class UpdateHook(UpdateHandler handler)
    {
        public UpdateHandler Handler { get; } = handler;

        public bool Removed { get; set; }
    }
}
