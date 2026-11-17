// -----------------------------------------------------------------------
// <copyright file="ServiceStore.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// The registry of service implementations, keyed by isolate realm. Replaces cordis'
/// <c>ReflectService</c>: instead of a <c>Proxy</c> get-trap, resolution is an explicit
/// walk up the fiber chain that stops at a realm boundary.
/// </summary>
/// <param name="root">The root context whose lock guards the store and whose registry is notified of changes.</param>
public sealed class ServiceStore(Context root)
{
    private readonly Dictionary<IsolateKey, ServiceImpl> store = [];
    private int implCounter;

    /// <summary>
    /// Register a provider owned by the calling fiber. Withdrawing it (or unloading the fiber)
    /// waits until every fiber that loaded against it has finished its own cleanup. Each call creates
    /// a fresh <see cref="ServiceImpl"/> in the realm <paramref name="name"/> resolves to for
    /// <paramref name="ctx"/>, and re-evaluates the fibers that inject it.
    /// </summary>
    /// <param name="ctx">The providing context; the binding is owned by its fiber.</param>
    /// <param name="name">The service name.</param>
    /// <param name="value">The service instance.</param>
    /// <param name="check">An optional availability check; when it returns <see langword="false"/> or throws, dependents do not load against the binding.</param>
    /// <returns>A handle that withdraws the binding when disposed.</returns>
    /// <exception cref="CordisException">The name is already registered in the same realm.</exception>
    public EffectHandle Provide(Context ctx, string name, object? value = null, Func<bool>? check = null)
    {
        return ctx.Fiber.EffectSync(
            () =>
            {
                var key = ctx.KeyFor(name);
                ServiceImpl impl;
                lock (root.SyncRoot)
                {
                    if (this.store.TryGetValue(key, out var existing))
                    {
                        throw new CordisException($"service \"{name}\" is already registered at <{existing.Fiber.Name}>");
                    }

                    impl = new ServiceImpl(++this.implCounter, name, ctx, value, check, key);
                    this.store[key] = impl;
                    ctx.Fiber.AddProvision(impl);
                    if (ctx.Fiber.State == FiberState.Active)
                    {
                        this.Notify(name, key);
                    }
                }

                return async () =>
                {
                    var release = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
                    lock (root.SyncRoot)
                    {
                        if (this.IsCurrent(impl))
                        {
                            this.store.Remove(impl.Isolate);
                        }

                        this.Notify(name, impl.Isolate);
                        impl.Release = release;
                        if (impl.Dependents.Count == 0)
                        {
                            release.TrySetResult();
                        }
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
            },
            $"ctx.Provide(\"{name}\")");
    }

    /// <summary>
    /// Swap the value behind an already-provided name (must be the owning fiber). The binding keeps
    /// its <see cref="ServiceImpl.Uid"/>; the fibers that inject the name are re-evaluated.
    /// </summary>
    /// <param name="ctx">A context of the fiber that owns the binding.</param>
    /// <param name="name">The service name.</param>
    /// <param name="value">The new service instance.</param>
    /// <exception cref="CordisException">The name is not provided in this realm, or is owned by another fiber.</exception>
    public void Set(Context ctx, string name, object? value)
    {
        lock (root.SyncRoot)
        {
            var key = ctx.KeyFor(name);
            if (!this.store.TryGetValue(key, out var impl))
            {
                throw new CordisException($"cannot set service \"{name}\" without Provide");
            }

            if (!ReferenceEquals(impl.Fiber, ctx.Fiber))
            {
                throw new CordisException($"cannot set service \"{name}\" owned by <{impl.Fiber.Name}>");
            }

            impl.Value = value;
            this.Notify(name, key);
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
    /// <param name="ctx">The context making the access.</param>
    /// <param name="name">The service name.</param>
    /// <returns>Where the access lands, or <see langword="null"/> if it is not admitted.</returns>
    public ServiceLocation? Locate(Context ctx, string name)
    {
        lock (root.SyncRoot)
        {
            var key = ctx.KeyFor(name);
            var table = ctx.IsolateMap;
            var fiber = ctx.Fiber;
            while (true)
            {
                ServiceImpl? impl = null;
                foreach (var provision in fiber.ProvisionsUnsafe)
                {
                    if (provision.Name == name && ReferenceEquals(provision.Isolate, key))
                    {
                        impl = provision;
                    }
                }

                if (impl is not null)
                {
                    return new ServiceLocation(null, impl);
                }

                while (!ReferenceEquals(table, fiber.Ctx.IsolateMap))
                {
                    if (table is null || table.HasOwn(name))
                    {
                        return null;
                    }

                    table = table.Parent;
                }

                if (fiber.Committed is { } committed && committed.TryGetValue(name, out impl))
                {
                    return new ServiceLocation(fiber, impl);
                }

                if (fiber.Inject.ContainsKey(name))
                {
                    return new ServiceLocation(fiber, null);
                }

                if (fiber.Runtime is null)
                {
                    return null;
                }

                fiber = fiber.Parent.Fiber;
            }
        }
    }

    /// <summary>
    /// The optional, unchecked lookup: upstream's <c>ctx.get(name)</c>. Returns the value of
    /// the active provider of <paramref name="name"/> in <paramref name="ctx"/>'s realm, or
    /// <see langword="null"/> when there is none. It never checks inject declarations and never
    /// throws. A value implementing <see cref="IContextScoped"/> is rebound to
    /// <paramref name="ctx"/>, as <see cref="Resolve"/> does.
    /// </summary>
    /// <param name="ctx">The context making the access.</param>
    /// <param name="name">The service name.</param>
    /// <returns>The service value, or <see langword="null"/> when no active provider exists.</returns>
    public object? Lookup(Context ctx, string name)
        => this.GetImpl(ctx, name) is { } impl ? Scope(ctx, impl.Value) : null;

    /// <summary>
    /// Resolve a service for <paramref name="ctx"/>. A component context reaches only what some
    /// fiber on its chain declared in <c>Inject</c> or provides itself; the root context is
    /// unrestricted. A value implementing <see cref="IContextScoped"/> is rebound to
    /// <paramref name="ctx"/> before it is returned.
    /// </summary>
    /// <param name="ctx">The context making the access.</param>
    /// <param name="name">The service name.</param>
    /// <param name="required">Whether to throw instead of returning <see langword="null"/> when the service is unavailable.</param>
    /// <returns>The service instance, or <see langword="null"/> if it is unavailable and <paramref name="required"/> is <see langword="false"/>.</returns>
    /// <exception cref="ServiceNotInjectedException">A component context accesses a name that no fiber on its chain declares or provides.</exception>
    /// <exception cref="ServiceUnavailableException">The service is required but has no active binding.</exception>
    public object? Resolve(Context ctx, string name, bool required = true)
    {
        if (ctx.Fiber.Runtime is null)
        {
            var impl = this.GetImpl(ctx, name);
            if (impl is not null)
            {
                return Scope(ctx, impl.Value);
            }

            return required ? throw new ServiceUnavailableException(name) : null;
        }

        var location = this.Locate(ctx, name) ?? throw new ServiceNotInjectedException(name);
        if (location.Impl is not null)
        {
            return Scope(ctx, location.Impl.Value);
        }

        return required ? throw new ServiceUnavailableException(name) : null;
    }

    internal ServiceImpl? GetImpl(Context ctx, string name, bool strict = true)
    {
        lock (root.SyncRoot)
        {
            var key = ctx.KeyFor(name);
            if (!this.store.TryGetValue(key, out var impl))
            {
                return null;
            }

            if (strict && impl.Fiber.State != FiberState.Active)
            {
                return null;
            }

            return impl;
        }
    }

    internal bool IsCurrent(ServiceImpl impl)
    {
        lock (root.SyncRoot)
        {
            return this.store.TryGetValue(impl.Isolate, out var current) && ReferenceEquals(current, impl);
        }
    }

    /// <summary>Re-evaluate every fiber that injects <paramref name="name"/> in realm <paramref name="key"/>.</summary>
    internal void Notify(string name, IsolateKey key)
    {
        lock (root.SyncRoot)
        {
            foreach (var runtime in root.Registry.Runtimes)
            {
                foreach (var fiber in runtime.Fibers)
                {
                    if (!fiber.Inject.ContainsKey(name))
                    {
                        continue;
                    }

                    if (!ReferenceEquals(fiber.Ctx.KeyFor(name), key))
                    {
                        continue;
                    }

                    fiber.CheckImpl(name);
                    fiber.Refresh();
                }
            }

            root.Internal.RaiseServiceChanged(name, this.store.TryGetValue(key, out var impl) ? impl.Value : null);
        }
    }

    private static object? Scope(Context ctx, object? value)
        => value is IContextScoped scoped ? scoped.ForContext(ctx) : value;
}
