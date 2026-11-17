namespace Cordis;

/// <summary>
/// One registered binding of a service name in one isolate realm. Mirrors cordis' <c>Impl</c>:
/// each <c>Provide</c> call creates a fresh binding with its own <see cref="Uid"/>, so dependents
/// can tell a re-provided service apart from the one they loaded against.
/// </summary>
public sealed class ServiceImpl
{
    internal ServiceImpl(int uid, string name, Context ctx, object? value, Func<bool>? check, IsolateKey isolate)
    {
        Uid = uid;
        Name = name;
        Ctx = ctx;
        Value = value;
        Check = check;
        Isolate = isolate;
    }

    /// <summary>Identity of this binding. Part of every dependent's epoch.</summary>
    public int Uid { get; }

    public string Name { get; }

    /// <summary>The context that provided this binding.</summary>
    public Context Ctx { get; }

    public Fiber Fiber => Ctx.Fiber;

    public object? Value { get; internal set; }

    public Func<bool>? Check { get; }

    /// <summary>The realm this binding is registered under.</summary>
    public IsolateKey Isolate { get; }

    /// <summary>Fibers that loaded against this binding and have not finished unloading.</summary>
    internal HashSet<Fiber> Dependents { get; } = [];

    /// <summary>Completed when the last dependent has unloaded after this binding was withdrawn.</summary>
    internal TaskCompletionSource? Release { get; set; }
}

/// <summary>Where an access to a service name lands. See <see cref="ServiceStore.Locate"/>.</summary>
/// <param name="Fiber">The fiber whose declaration admits the access; <c>null</c> for an own provision.</param>
/// <param name="Impl">The binding the access reaches; <c>null</c> if the declaring fiber is inactive.</param>
public readonly record struct ServiceLocation(Fiber? Fiber, ServiceImpl? Impl);

/// <summary>
/// The registry of service implementations, keyed by isolate realm. Replaces cordis'
/// <c>ReflectService</c>: instead of a <c>Proxy</c> get-trap, resolution is an explicit
/// walk up the fiber chain that stops at a realm boundary.
/// </summary>
public sealed class ServiceStore(Context root)
{
    private readonly Dictionary<IsolateKey, ServiceImpl> _store = [];
    private int _implCounter;

    /// <summary>
    /// Register a provider owned by the calling fiber. Withdrawing it (or unloading the fiber)
    /// waits until every fiber that loaded against it has finished its own cleanup.
    /// </summary>
    public EffectHandle Provide(Context ctx, string name, object? value = null, Func<bool>? check = null)
    {
        return ctx.Fiber.EffectSync(() =>
        {
            IsolateKey key = ctx.KeyFor(name);
            ServiceImpl impl;
            lock (root.SyncRoot)
            {
                if (_store.TryGetValue(key, out ServiceImpl? existing))
                {
                    throw new CordisException($"service \"{name}\" is already registered at <{existing.Fiber.Name}>");
                }
                impl = new ServiceImpl(++_implCounter, name, ctx, value, check, key);
                _store[key] = impl;
                ctx.Fiber.AddProvision(impl);
                if (ctx.Fiber.State == FiberState.Active) Notify(name, key);
            }

            return async () =>
            {
                var release = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
                lock (root.SyncRoot)
                {
                    if (IsCurrent(impl)) _store.Remove(impl.Isolate);
                    Notify(name, impl.Isolate);
                    impl.Release = release;
                    if (impl.Dependents.Count == 0) release.TrySetResult();
                }

                // A withdrawn binding gains no new dependents; wait only for the existing ones to
                // finish cleanup. Yield even without dependents, so that child cleanup can still
                // reach the inherited service before the provision is removed.
                await Task.Yield();
                await release.Task.ConfigureAwait(false);

                lock (root.SyncRoot)
                {
                    impl.Release = null;
                    ctx.Fiber.RemoveProvision(impl);
                }
            };
        }, $"ctx.Provide(\"{name}\")");
    }

    /// <summary>Swap the value behind an already-provided name (must be the owning fiber).</summary>
    public void Set(Context ctx, string name, object? value)
    {
        lock (root.SyncRoot)
        {
            IsolateKey key = ctx.KeyFor(name);
            if (!_store.TryGetValue(key, out ServiceImpl? impl))
            {
                throw new CordisException($"cannot set service \"{name}\" without Provide");
            }
            if (!ReferenceEquals(impl.Fiber, ctx.Fiber))
            {
                throw new CordisException($"cannot set service \"{name}\" owned by <{impl.Fiber.Name}>");
            }
            impl.Value = value;
            Notify(name, key);
        }
    }

    internal ServiceImpl? GetImpl(Context ctx, string name, bool strict = true)
    {
        lock (root.SyncRoot)
        {
            IsolateKey key = ctx.KeyFor(name);
            if (!_store.TryGetValue(key, out ServiceImpl? impl)) return null;
            if (strict && impl.Fiber.State != FiberState.Active) return null;
            return impl;
        }
    }

    internal bool IsCurrent(ServiceImpl impl)
    {
        lock (root.SyncRoot)
        {
            return _store.TryGetValue(impl.Isolate, out ServiceImpl? current) && ReferenceEquals(current, impl);
        }
    }

    /// <summary>
    /// Resolve an access to <paramref name="name"/> from <paramref name="ctx"/> by walking up its
    /// fibers. Analogue of cordis' <c>locate()</c>. Returns:
    /// <list type="bullet">
    /// <item>an own provision of a fiber on the walk, in the same realm (<c>Fiber</c> is null);</item>
    /// <item>the binding a declaring fiber loaded against (<c>Fiber</c> and <c>Impl</c> set);</item>
    /// <item>a declaring fiber that is inactive (<c>Impl</c> is null);</item>
    /// <item><c>null</c> if nothing declares it, or the walk crosses a context that isolates the name again.</item>
    /// </list>
    /// </summary>
    public ServiceLocation? Locate(Context ctx, string name)
    {
        lock (root.SyncRoot)
        {
            IsolateKey key = ctx.KeyFor(name);
            ChainMap<IsolateKey>? table = ctx.IsolateMap;
            Fiber fiber = ctx.Fiber;
            while (true)
            {
                ServiceImpl? impl = null;
                foreach (ServiceImpl provision in fiber.ProvisionsUnsafe)
                {
                    if (provision.Name == name && ReferenceEquals(provision.Isolate, key)) impl = provision;
                }
                if (impl is not null) return new ServiceLocation(null, impl);

                while (!ReferenceEquals(table, fiber.Ctx.IsolateMap))
                {
                    if (table is null || table.HasOwn(name)) return null;
                    table = table.Parent;
                }

                if (fiber.Committed is { } committed && committed.TryGetValue(name, out impl)) return new ServiceLocation(fiber, impl);
                if (fiber.Inject.ContainsKey(name)) return new ServiceLocation(fiber, null);
                if (fiber.Runtime is null) return null;
                fiber = fiber.Parent.Fiber;
            }
        }
    }

    /// <summary>
    /// Resolve a service for <paramref name="ctx"/>. A component context reaches only what some
    /// fiber on its chain declared in <c>Inject</c> or provides itself; the root context is
    /// unrestricted.
    /// </summary>
    public object? Resolve(Context ctx, string name, bool required = true)
    {
        if (ctx.Fiber.Runtime is null)
        {
            ServiceImpl? impl = GetImpl(ctx, name);
            if (impl is not null) return Scope(ctx, impl.Value);
            return required ? throw new ServiceUnavailableException(name) : null;
        }

        ServiceLocation location = Locate(ctx, name) ?? throw new ServiceNotInjectedException(name);
        if (location.Impl is not null) return Scope(ctx, location.Impl.Value);
        return required ? throw new ServiceUnavailableException(name) : null;
    }

    private static object? Scope(Context ctx, object? value)
        => value is IContextScoped scoped ? scoped.ForContext(ctx) : value;

    /// <summary>Re-evaluate every fiber that injects <paramref name="name"/> in realm <paramref name="key"/>.</summary>
    internal void Notify(string name, IsolateKey key)
    {
        lock (root.SyncRoot)
        {
            foreach (ComponentRuntime runtime in root.Registry.Runtimes)
            {
                foreach (Fiber fiber in runtime.Fibers)
                {
                    if (!fiber.Inject.ContainsKey(name)) continue;
                    if (!ReferenceEquals(fiber.Ctx.KeyFor(name), key)) continue;
                    fiber.CheckImpl(name);
                    fiber.Refresh();
                }
            }

            root.Internal.RaiseServiceChanged(name, _store.TryGetValue(key, out ServiceImpl? impl) ? impl.Value : null);
        }
    }
}
