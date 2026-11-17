// -----------------------------------------------------------------------
// <copyright file="RegistryService.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// The component registry of a context tree. Keeps one <see cref="ComponentRuntime"/> per component
/// identity (a type, an <see cref="IComponent"/> instance or a delegate) and creates one
/// <see cref="Fiber"/> per use. Analogue of cordis' <c>Registry</c>.
/// </summary>
/// <param name="root">The root context whose lock guards the registry.</param>
public sealed class RegistryService(Context root)
{
    private readonly Dictionary<object, ComponentRuntime> runtimes = new(ReferenceEqualityComparer.Instance);
    private readonly Dictionary<object, ComponentSource> aliases = new(ReferenceEqualityComparer.Instance);
    private int counter;

    /// <summary>
    /// Gets the number of registered component runtimes.
    /// </summary>
    public int Count
    {
        get
        {
            lock (root.SyncRoot)
            {
                return this.runtimes.Count;
            }
        }
    }

    /// <summary>
    /// Gets a snapshot of the registered component runtimes.
    /// </summary>
    public IReadOnlyList<ComponentRuntime> Runtimes
    {
        get
        {
            lock (root.SyncRoot)
            {
                return [.. this.runtimes.Values];
            }
        }
    }

    /// <summary>
    /// Determines whether a runtime is registered for a component, following any
    /// <see cref="Replace(object, object)"/> forwards.
    /// </summary>
    /// <param name="key">The registry key: a <see cref="Type"/>, an <see cref="IComponent"/> instance, or a delegate.</param>
    /// <returns><see langword="true"/> if a runtime is registered for <paramref name="key"/>; otherwise, <see langword="false"/>.</returns>
    public bool Has(object key)
    {
        lock (root.SyncRoot)
        {
            return this.runtimes.ContainsKey(this.ForwardKey(key));
        }
    }

    /// <summary>
    /// Gets the runtime registered for a component, following any
    /// <see cref="Replace(object, object)"/> forwards.
    /// </summary>
    /// <param name="key">The registry key: a <see cref="Type"/>, an <see cref="IComponent"/> instance, or a delegate.</param>
    /// <returns>The registered runtime, or <see langword="null"/> if the component is not registered.</returns>
    public ComponentRuntime? Get(object key)
    {
        lock (root.SyncRoot)
        {
            return this.runtimes.GetValueOrDefault(this.ForwardKey(key));
        }
    }

    /// <summary>
    /// Create a fiber for a component. Analogue of <c>registry.use()</c>. If the component was
    /// replaced, the use is forwarded to the current implementation; its runtime is created on
    /// first use and shared by every later use.
    /// </summary>
    /// <param name="ctx">The parent context that owns the new fiber; its fiber must be active.</param>
    /// <param name="source">The component to use.</param>
    /// <param name="config">The configuration passed to the component, validated by its schema on every load.</param>
    /// <param name="inject">Extra service names to inject, in addition to those the component declares.</param>
    /// <returns>The new fiber; await it to observe the load.</returns>
    /// <exception cref="InactiveFiberException">The fiber of <paramref name="ctx"/> is inactive.</exception>
    public Fiber Use(Context ctx, ComponentSource source, object? config = null, IEnumerable<string>? inject = null)
    {
        ctx.Fiber.AssertActive();
        ComponentRuntime runtime;
        lock (root.SyncRoot)
        {
            source = this.Forward(source);
            runtime = this.GetOrCreate(source);
        }

        var extra = inject?.ToArray() ?? [];
        return new Fiber(ctx, runtime, config, MergeInject(source, extra), extra);
    }

    /// <summary>
    /// Swap the implementation behind every fiber of <paramref name="oldComponent"/> in place.
    /// Each fiber keeps its identity and config, takes the new component's dependencies and
    /// schema, and reloads. Later uses of the old component are forwarded to the new one.
    /// Analogue of <c>registry.replace()</c>.
    /// </summary>
    /// <param name="oldComponent">A <see cref="Type"/>, an <see cref="IComponent"/> instance, or any registry key.</param>
    /// <param name="newComponent">A <see cref="Type"/>, an <see cref="IComponent"/> instance, or a <see cref="ComponentSource"/>.</param>
    /// <returns>The fibers that were moved; await each to observe the reload.</returns>
    public IReadOnlyList<Fiber> Replace(object oldComponent, object newComponent)
    {
        ArgumentNullException.ThrowIfNull(oldComponent);
        var next = ComponentSource.From(newComponent);
        var oldKey = oldComponent is ComponentSource source ? source.Key : oldComponent;

        lock (root.SyncRoot)
        {
            oldKey = this.ForwardKey(oldKey);

            // `next` is an implementation of its own again
            this.aliases.Remove(next.Key);
            var runtime = this.runtimes.GetValueOrDefault(oldKey);
            var target = this.runtimes.GetValueOrDefault(next.Key);
            if (!ReferenceEquals(oldKey, next.Key))
            {
                this.runtimes.Remove(oldKey);
                this.aliases[oldKey] = next;
            }

            var fibers = runtime?.Fibers ?? [];
            if (fibers.Count == 0)
            {
                return fibers;
            }

            if (target is null)
            {
                target = next.CreateRuntime();
                this.runtimes[next.Key] = target;
            }

            foreach (var fiber in fibers)
            {
                fiber.Replace(target, MergeInject(next, fiber.ExtraInject));
            }

            return fibers;
        }
    }

    /// <summary>
    /// Unload every fiber of a component and drop its runtime. Analogue of the former
    /// <c>registry.delete()</c>. Does nothing if the component is not registered.
    /// </summary>
    /// <param name="key">The registry key: a <see cref="Type"/>, an <see cref="IComponent"/> instance, or a delegate.</param>
    /// <returns>A task that completes when every fiber of the component has been disposed.</returns>
    public async Task RemoveAsync(object key)
    {
        ComponentRuntime? runtime;
        lock (root.SyncRoot)
        {
            key = this.ForwardKey(key);
            if (!this.runtimes.Remove(key, out runtime))
            {
                return;
            }
        }

        foreach (var fiber in runtime.Fibers)
        {
            await fiber.DisposeAsync().ConfigureAwait(false);
        }
    }

    internal int NextUid() => Interlocked.Increment(ref this.counter);

    /// <summary>Drops a runtime whose last fiber was disposed, unless it was already replaced.</summary>
    internal void Unregister(ComponentRuntime runtime)
    {
        lock (root.SyncRoot)
        {
            if (this.runtimes.TryGetValue(runtime.Key, out var current) && ReferenceEquals(current, runtime))
            {
                this.runtimes.Remove(runtime.Key);
            }
        }
    }

    private static Dictionary<string, object?> MergeInject(ComponentSource source, IReadOnlyList<string> extra)
    {
        var dependencies = source.ResolveInject();
        foreach (var name in extra)
        {
            dependencies.TryAdd(name, null);
        }

        return dependencies;
    }

    /// <summary>Follows <see cref="Replace(object, object)"/> forwards to the current implementation.</summary>
    private object ForwardKey(object key)
    {
        while (this.aliases.TryGetValue(key, out var next))
        {
            key = next.Key;
        }

        return key;
    }

    private ComponentSource Forward(ComponentSource source)
    {
        while (this.aliases.TryGetValue(source.Key, out var next))
        {
            source = next;
        }

        return source;
    }

    private ComponentRuntime GetOrCreate(ComponentSource source)
    {
        if (this.runtimes.TryGetValue(source.Key, out var runtime))
        {
            return runtime;
        }

        runtime = source.CreateRuntime();
        this.runtimes[source.Key] = runtime;
        return runtime;
    }
}
