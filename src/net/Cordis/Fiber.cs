using System.Runtime.CompilerServices;
using System.Runtime.ExceptionServices;
using System.Text;

namespace Cordis;

public enum FiberState
{
    Pending,
    Loading,
    Active,
    Failed,
    Disposed,
    Unloading,
}

/// <summary>
/// A per-fiber update hook. Receives the resolved config and a <c>next</c> continuation;
/// calling <c>next</c> lets the update proceed (eventually restarting the fiber), not calling it
/// accepts the update in place. Analogue of a non-global <c>internal/update</c> listener.
/// </summary>
public delegate Task UpdateHandler(object? resolvedConfig, Func<Task> next);

/// <summary>
/// One live instantiation of a component. Owns the effects the component created, tracks the
/// identity of every injected provider as an "epoch", and reloads/unloads itself whenever
/// that epoch changes. Transitions are serialised by the inertia lock.
/// </summary>
public sealed class Fiber : IAsyncDisposable
{
    internal const string InactiveEpoch = "\u0000inactive";

    private readonly Dictionary<string, ServiceImpl> _candidates = new(StringComparer.Ordinal);
    private readonly List<EffectHandle> _effects = [];
    private readonly List<Task> _loadTasks = [];
    private readonly List<UpdateHook> _updateHooks = [];
    private readonly EffectHandle? _anchor;
    private Dictionary<string, object?> _inject;
    private string _epoch = InactiveEpoch;
    private int _revision;
    private Task? _inertia;
    private TaskCompletionSource? _release;

    /// <summary>Root fiber: always active, owns nothing but the top-level effects.</summary>
    internal Fiber(Context root)
    {
        Gate = root.SyncRoot;
        Parent = root;
        Ctx = root;
        Uid = 0;
        _inject = new Dictionary<string, object?>(StringComparer.Ordinal);
        ExtraInject = [];
        _epoch = "0";
        State = FiberState.Active;
        Committed = new Dictionary<string, ServiceImpl>(StringComparer.Ordinal);
    }

    internal Fiber(
        Context parent,
        ComponentRuntime runtime,
        object? config,
        Dictionary<string, object?> inject,
        IReadOnlyList<string> extraInject)
    {
        Gate = parent.SyncRoot;
        Parent = parent;
        Runtime = runtime;
        Config = config;
        ExtraInject = extraInject;
        _inject = inject;
        Uid = parent.Registry.NextUid();
        Ctx = parent.ExtendForFiber(this);

        parent.Internal.RaisePluginChanged(this);

        lock (Gate)
        {
            foreach (string name in _inject.Keys) CheckImpl(name);
        }

        _anchor = parent.Fiber.EffectSync(() =>
        {
            runtime.Attach(this);
            try
            {
                lock (Gate) Refresh();
            }
            catch (Exception error)
            {
                Ctx.Logger.Error(error);
                lock (Gate)
                {
                    Error = error;
                    UpdateState(static () => null);
                }
            }

            return async () =>
            {
                lock (Gate) Uid = null;
                parent.Internal.RaisePluginChanged(this);

                lock (Gate)
                {
                    ComponentRuntime current = Runtime!;
                    current.Detach(this);
                    if (current.FiberCount == 0) parent.Registry.Unregister(current);
                    SetEpoch(InactiveEpoch);
                }

                // `inertia` never faults: reload and unload both log their own work errors.
                while (true)
                {
                    Task? inertia;
                    lock (Gate) inertia = _inertia;
                    if (inertia is null) break;
                    try { await inertia.ConfigureAwait(false); }
                    catch { /* reported by the transition itself */ }
                }

                // SetEpoch above returns early for a fiber that was not active, so the cached
                // state is not refreshed. Re-derive it now that Uid is null.
                lock (Gate) UpdateState(static () => null);
            };
        }, "ctx.Use()");
    }

    internal object Gate { get; }

    /// <summary>Incrementing identity; <c>null</c> once disposed.</summary>
    public int? Uid { get; private set; }

    /// <summary>The context this fiber was created from.</summary>
    public Context Parent { get; }

    /// <summary>The context handed to the component body.</summary>
    public Context Ctx { get; }

    /// <summary>The component behind this fiber. Changes on <see cref="RegistryService.Replace"/>.</summary>
    public ComponentRuntime? Runtime { get; private set; }

    /// <summary>
    /// The config as it was handed in (not validated). It is resolved through the component's
    /// schema on every load, so validation failures surface as a failed load.
    /// </summary>
    public object? Config { get; private set; }

    public FiberState State { get; private set; } = FiberState.Pending;

    public Exception? Error { get; private set; }

    public IReadOnlyDictionary<string, object?> Inject => _inject;

    /// <summary>Dependencies added at the use site (on top of the component's own declaration).</summary>
    internal IReadOnlyList<string> ExtraInject { get; }

    /// <summary>The bindings this fiber loaded against; <c>null</c> while unloaded.</summary>
    internal Dictionary<string, ServiceImpl>? Committed { get; private set; }

    /// <summary>Bindings this fiber provides. Read under <see cref="Gate"/>.</summary>
    internal HashSet<ServiceImpl> ProvisionsUnsafe { get; } = [];

    public object? Instance { get; private set; }

    public string Name
    {
        get
        {
            Fiber fiber = this;
            while (fiber.Runtime is null)
            {
                Fiber parent = fiber.Parent.Fiber;
                if (ReferenceEquals(parent, fiber)) return "root";
                fiber = parent;
            }
            return fiber.Runtime.Name;
        }
    }

    public override string ToString() => $"Fiber <{Name}> ({State})";

    // ---------------------------------------------------------------- effects

    public void AssertActive()
    {
        if (Uid is null) throw new InactiveFiberException();
    }

    /// <summary>Acquire a resource; the returned cleanup runs when the fiber unloads.</summary>
    public EffectHandle Effect(Func<Action?> execute, string label = "anonymous")
    {
        return Run(label, handle =>
        {
            Action? cleanup = execute();
            if (cleanup is not null) handle.Collect(() => { cleanup(); return default; });
            return null;
        });
    }

    /// <summary>Async acquisition. The fiber stays <see cref="FiberState.Loading"/> until it completes.</summary>
    public EffectHandle EffectAsync(Func<ValueTask<Disposer?>> execute, string label = "anonymous")
    {
        return Run(label, Body);

        async Task Body(EffectHandle handle) => handle.Collect(await execute().ConfigureAwait(false));
    }

    /// <summary>
    /// Generator form: yield one disposer per acquired resource. The analogue of cordis'
    /// <c>function*</c> effects. Pass an iterator local function. Once the effect is disposed,
    /// the iterator is not advanced again: the pending step is collected and the iterator is
    /// closed, so its <c>finally</c> blocks run.
    /// </summary>
    public EffectHandle EffectMany(Func<IAsyncEnumerable<Disposer>> execute, string label = "anonymous")
    {
        return Run(label, Body);

        async Task Body(EffectHandle handle)
        {
            IAsyncEnumerator<Disposer> iterator = execute().GetAsyncEnumerator();
            await using (iterator.ConfigureAwait(false))
            {
                while (handle.IsAlive && await iterator.MoveNextAsync().ConfigureAwait(false))
                {
                    handle.Collect(iterator.Current);
                }
            }
        }
    }

    public EffectHandle Use(IDisposable resource, string? label = null)
        => EffectSync(() => () => { resource.Dispose(); return default; }, label ?? $"use({resource.GetType().Name})");

    public EffectHandle UseAsync(IAsyncDisposable resource, string? label = null)
        => EffectSync(() => resource.DisposeAsync, label ?? $"use({resource.GetType().Name})");

    internal EffectHandle EffectSync(Func<Disposer?> execute, string label)
        => Run(label, handle => { handle.Collect(execute()); return null; });

    /// <summary>Snapshot of the effects currently owned by this fiber.</summary>
    public IReadOnlyList<EffectHandle> Effects
    {
        get { lock (Gate) return [.. _effects]; }
    }

    private EffectHandle Run(string label, Func<EffectHandle, Task?> body)
    {
        AssertActive();
        var handle = new EffectHandle(this, label);
        lock (Gate) _effects.Add(handle);

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

        if (task is null || task.IsCompletedSuccessfully) return handle;

        if (task.IsFaulted)
        {
            _ = handle.DisposeAsync();
            ExceptionDispatchInfo.Capture(task.Exception!.InnerException ?? task.Exception).Throw();
        }

        handle.SetPending(task);

        // An async effect started by the component body keeps the fiber LOADING until it settles,
        // and its failure fails the load. Effects started later (from a callback) cannot.
        lock (Gate)
        {
            if (State == FiberState.Loading)
            {
                _loadTasks.Add(task);
                return handle;
            }
        }

        _ = task.ContinueWith(faulted =>
        {
            Ctx.Logger.Error(faulted.Exception!.InnerException ?? faulted.Exception);
            _ = handle.DisposeAsync();
        }, CancellationToken.None, TaskContinuationOptions.OnlyOnFaulted, TaskScheduler.Default);

        return handle;
    }

    internal void Detach(EffectHandle handle)
    {
        lock (Gate) _effects.Remove(handle);
    }

    internal void AttachDisposer(Disposer disposer, string label)
    {
        var handle = new EffectHandle(this, label);
        lock (Gate) _effects.Add(handle);
        handle.Collect(disposer);
    }

    // ---------------------------------------------------------- update hooks

    internal EffectHandle AddUpdateHook(UpdateHandler handler, bool prepend)
    {
        return EffectSync(() =>
        {
            var hook = new UpdateHook(handler);
            lock (Gate)
            {
                if (prepend) _updateHooks.Insert(0, hook); else _updateHooks.Add(hook);
            }
            return () =>
            {
                lock (Gate)
                {
                    hook.Removed = true;
                    _updateHooks.Remove(hook);
                }
                return default;
            };
        }, "ctx.OnUpdate()");
    }

    private Task RunUpdateHooks(object? resolved, Func<Task> terminal)
    {
        UpdateHook[] hooks;
        lock (Gate) hooks = [.. _updateHooks];
        int index = 0;
        return Dispatch();

        Task Dispatch()
        {
            while (index < hooks.Length && hooks[index].Removed) index++;
            if (index >= hooks.Length) return terminal();
            UpdateHook hook = hooks[index++];
            bool called = false;
            return hook.Handler(resolved, () =>
            {
                if (called) throw new InvalidOperationException("next() called multiple times");
                called = true;
                return Dispatch();
            });
        }
    }

    private sealed class UpdateHook(UpdateHandler handler)
    {
        public UpdateHandler Handler { get; } = handler;

        public bool Removed { get; set; }
    }

    // ------------------------------------------------------- dependency graph

    internal void AddProvision(ServiceImpl impl)
    {
        lock (Gate) ProvisionsUnsafe.Add(impl);
    }

    internal void RemoveProvision(ServiceImpl impl)
    {
        lock (Gate) ProvisionsUnsafe.Remove(impl);
    }

    /// <summary>Wakes this fiber's unload if it is waiting for dependents to let go.</summary>
    internal void SignalRelease()
    {
        lock (Gate) _release?.TrySetResult();
    }

    /// <summary>Re-evaluate whether <paramref name="name"/> currently has a usable provider.</summary>
    internal void CheckImpl(string name)
    {
        ServiceImpl? impl = Ctx.Reflect.GetImpl(Ctx, name, strict: true);
        if (impl is null)
        {
            lock (Gate) _candidates.Remove(name);
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
                lock (Gate) _candidates.Remove(name);
                return;
            }
        }

        lock (Gate) _candidates[name] = impl;
    }

    /// <summary>
    /// Recompute the epoch: the reload revision followed by the identity of every injected
    /// binding. Must be called under <see cref="Gate"/>.
    /// </summary>
    internal void Refresh()
    {
        if (Uid is null) return;
        StringBuilder builder = new StringBuilder().Append(_revision);
        foreach (string name in _inject.Keys)
        {
            if (!_candidates.TryGetValue(name, out ServiceImpl? impl))
            {
                SetEpoch(InactiveEpoch);
                return;
            }
            builder.Append(':').Append(impl.Uid);
        }
        SetEpoch(builder.ToString());
    }

    private void SetEpoch(string epoch)
    {
        string old = _epoch;
        if (epoch == old) return;
        // a failed fiber only recovers through UpdateAsync() or a replacement, which clear the error
        if (Error is not null) return;
        _epoch = epoch;
        // inertia lock: a transition is already running; it reconciles on completion.
        if (_inertia is not null) return;

        UpdateState(() =>
        {
            if (epoch != InactiveEpoch && old == InactiveEpoch)
            {
                _inertia = ReloadAsync();
                return FiberState.Loading;
            }
            _inertia = UnloadAsync();
            return FiberState.Unloading;
        });
    }

    private FiberState ComputeState()
    {
        if (Uid is null) return FiberState.Disposed;
        if (Error is not null) return FiberState.Failed;
        return _epoch != InactiveEpoch ? FiberState.Active : FiberState.Pending;
    }

    private void UpdateState(Func<FiberState?> transition)
    {
        FiberState old = State;
        State = transition() ?? ComputeState();
        if (old == State) return;

        Ctx.Internal.RaiseStatusChanged(this, old);

        // only (de)activation is observable by dependents
        if (old != FiberState.Active && State != FiberState.Active) return;
        foreach (ServiceImpl? impl in ProvisionsUnsafe.ToArray())
        {
            if (Ctx.Reflect.IsCurrent(impl)) Ctx.Reflect.Notify(impl.Name, impl.Isolate);
        }
    }

    private async Task ReloadAsync()
    {
        await Task.Yield(); // mirror cordis: the component body never runs inside ctx.Use()

        string epoch;
        lock (Gate)
        {
            Committed = new Dictionary<string, ServiceImpl>(_candidates, StringComparer.Ordinal);
            foreach (ServiceImpl impl in Committed.Values)
            {
                if (!ReferenceEquals(impl.Fiber, this)) impl.Dependents.Add(this);
            }
            epoch = _epoch;
        }

        try
        {
            await ExecuteAsync().ConfigureAwait(false);
        }
        catch (Exception error)
        {
            Ctx.Logger.Error(error);
            lock (Gate)
            {
                // A superseded load still needs cleanup, but cannot fail the new target.
                if (_epoch == epoch)
                {
                    Error = error;
                    _epoch = InactiveEpoch;
                }
            }
        }

        lock (Gate)
        {
            UpdateState(() =>
            {
                if (_epoch == epoch)
                {
                    _inertia = null;
                    return null;
                }
                _inertia = UnloadAsync();
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
            lock (Gate)
            {
                if (!ProvisionsUnsafe.Any(impl => impl.Dependents.Count > 0))
                {
                    _release = null;
                    break;
                }
                _release = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
                wait = _release.Task;
            }
            await wait.ConfigureAwait(false);
        }

        EffectHandle[] effects;
        lock (Gate)
        {
            effects = [.. _effects];
            _effects.Clear();
            _loadTasks.Clear();
        }

        for (int i = effects.Length - 1; i >= 0; i--)
        {
            try { await effects[i].DisposeAsync().ConfigureAwait(false); }
            catch (Exception error) { Ctx.Logger.Error(error); }
        }

        lock (Gate)
        {
            // release the providers waiting for this fiber to unload
            foreach (ServiceImpl impl in Committed?.Values ?? Enumerable.Empty<ServiceImpl>())
            {
                if (!impl.Dependents.Remove(this) || impl.Dependents.Count > 0) continue;
                impl.Release?.TrySetResult();
                impl.Fiber.SignalRelease();
            }

            Committed = null;
            Instance = null;
            UpdateState(() =>
            {
                if (_epoch == InactiveEpoch)
                {
                    _inertia = null;
                    return null;
                }
                _inertia = ReloadAsync();
                return FiberState.Loading;
            });
        }
    }

    private object? ResolveConfig(object? config)
        => Runtime?.Schema is { } schema ? schema.Validate(config) : config;

    private async Task ExecuteAsync()
    {
        object? config;
        lock (Gate) config = Config;
        object? instance = await Runtime!.Apply(Ctx, ResolveConfig(config)).ConfigureAwait(false);
        lock (Gate) Instance = instance;

        switch (instance)
        {
            case IAsyncDisposable asyncDisposable:
                AttachDisposer(asyncDisposable.DisposeAsync, $"dispose({instance.GetType().Name})");
                break;
            case IDisposable disposable:
                AttachDisposer(() => { disposable.Dispose(); return default; }, $"dispose({instance.GetType().Name})");
                break;
        }

        if (instance is IAsyncInit init) await init.InitAsync().ConfigureAwait(false);

        // drain async effects registered during the load (they may register more)
        while (true)
        {
            Task[] pending;
            lock (Gate)
            {
                if (_loadTasks.Count == 0) break;
                pending = [.. _loadTasks];
                _loadTasks.Clear();
            }
            await Task.WhenAll(pending).ConfigureAwait(false);
        }
    }

    /// <summary>Swap in a replacement component. See <see cref="RegistryService.Replace"/>.</summary>
    internal void Replace(ComponentRuntime runtime, Dictionary<string, object?> inject)
    {
        lock (Gate)
        {
            if (!ReferenceEquals(runtime, Runtime))
            {
                Runtime!.Detach(this);
                runtime.Attach(this);
                Runtime = runtime;
            }
            _inject = inject;
            Ctx.Internal.RaiseReplaced(this);
            _candidates.Clear();
            foreach (string name in _inject.Keys) CheckImpl(name);
            Error = null;
            _revision++;
            Refresh();
            // an unchanged epoch starts no transition, but a cleared error still changes the state
            if (_inertia is null) UpdateState(static () => null);
        }
    }

    // ------------------------------------------------------------- public API

    /// <summary>Wait until the fiber settles; rethrows the load error if it failed.</summary>
    public async Task<Fiber> WaitAsync()
    {
        while (true)
        {
            Task? inertia;
            lock (Gate) inertia = _inertia;
            if (inertia is null) break;
            await inertia.ConfigureAwait(false);
        }
        if (Error is not null) ExceptionDispatchInfo.Capture(Error).Throw();
        return this;
    }

    /// <summary><c>await ctx.Use(...)</c> resolves once the fiber settles.</summary>
    public TaskAwaiter<Fiber> GetAwaiter() => WaitAsync().GetAwaiter();

    /// <summary>
    /// Reload the component with its current config. A restart requested while a load is in
    /// flight is never lost: the reload revision is part of the epoch.
    /// </summary>
    public async Task RestartAsync()
    {
        AssertActive();
        lock (Gate)
        {
            _revision++;
            Refresh();
        }
        await WaitAsync().ConfigureAwait(false);
    }

    /// <summary>
    /// Replace the config. If the component is loaded, the new config is validated first (an
    /// invalid config throws and changes nothing), then passed through the fiber's update hooks
    /// (<see cref="Context.OnUpdate"/>), which may accept it in place; otherwise the fiber
    /// restarts. Unless <paramref name="noSave"/> is set, <see cref="InternalEvents.Committing"/>
    /// fires with the original config so a loader can persist it.
    /// </summary>
    public async Task UpdateAsync(object? config, bool noSave = false)
    {
        AssertActive();
        bool loaded;
        lock (Gate) loaded = _epoch != InactiveEpoch;
        object? resolved = loaded ? ResolveConfig(config) : null;
        if (!noSave) Ctx.Internal.RaiseCommitting(this, config);
        lock (Gate)
        {
            Config = config;
            Error = null;
        }

        if (loaded) await RunUpdateHooks(resolved, RestartAsync).ConfigureAwait(false);
        else await RestartAsync().ConfigureAwait(false);
    }

    public async ValueTask DisposeAsync()
    {
        if (_anchor is not null)
        {
            await _anchor.DisposeAsync().ConfigureAwait(false);
            return;
        }

        // root fiber: tear the whole tree down
        EffectHandle[] effects;
        lock (Gate)
        {
            effects = [.. _effects];
            _effects.Clear();
        }
        for (int i = effects.Length - 1; i >= 0; i--)
        {
            try { await effects[i].DisposeAsync().ConfigureAwait(false); }
            catch (Exception error) { Ctx.Logger.Error(error); }
        }
    }
}
