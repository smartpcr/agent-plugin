using System.Reflection;
using System.Runtime.ExceptionServices;

namespace Cordis;

/// <summary>
/// An object-style component. Analogue of cordis' <c>{ name, inject, Config, apply }</c> object.
/// (Upstream renamed "plugin" to "component"; <c>ctx.Plugin()</c> remains as an obsolete alias.)
/// </summary>
public interface IComponent
{
    string? ComponentName => null;

    IEnumerable<string> Inject => [];

    IConfigSchema? ConfigSchema => null;

    ValueTask ApplyAsync(Context ctx);
}

public interface IComponent<in TConfig> : IComponent
{
    ValueTask ApplyAsync(Context ctx, TConfig config);

    ValueTask IComponent.ApplyAsync(Context ctx) => ApplyAsync(ctx, default!);
}

/// <summary>
/// A component registration. One runtime per component identity (type, instance or delegate);
/// one <see cref="Fiber"/> per use.
/// </summary>
public sealed class ComponentRuntime(
    object key,
    string name,
    Func<Context, object?, ValueTask<object?>> apply,
    IConfigSchema? schema)
{
    private readonly List<Fiber> _fibers = [];

    public object Key { get; } = key;

    public string Name { get; } = name;

    public IConfigSchema? Schema { get; } = schema;

    internal Func<Context, object?, ValueTask<object?>> Apply { get; } = apply;

    public IReadOnlyList<Fiber> Fibers
    {
        get { lock (_fibers) return [.. _fibers]; }
    }

    public int FiberCount
    {
        get { lock (_fibers) return _fibers.Count; }
    }

    internal void Attach(Fiber fiber) { lock (_fibers) _fibers.Add(fiber); }

    internal void Detach(Fiber fiber) { lock (_fibers) _fibers.Remove(fiber); }
}

/// <summary>How to build a runtime for one component identity, and what it injects.</summary>
public sealed class ComponentSource
{
    private readonly Func<ComponentRuntime> _create;
    private readonly Func<Dictionary<string, object?>> _inject;

    private ComponentSource(object key, Func<ComponentRuntime> create, Func<Dictionary<string, object?>> inject)
    {
        Key = key;
        _create = create;
        _inject = inject;
    }

    /// <summary>Registry key: the type, the component instance, or the delegate.</summary>
    public object Key { get; }

    internal ComponentRuntime CreateRuntime() => _create();

    internal Dictionary<string, object?> ResolveInject() => _inject();

    public static ComponentSource ForType(Type type)
        => new(type, () => ComponentFactory.ForType(type), () => ComponentFactory.InjectOf(type));

    public static ComponentSource ForInstance(IComponent component)
        => new(component, () => ComponentFactory.ForInstance(component), () =>
        {
            Dictionary<string, object?> inject = ComponentFactory.InjectOf(component.Inject);
            foreach ((string? name, object? value) in ComponentFactory.InjectOf(component.GetType())) inject[name] = value;
            return inject;
        });

    public static ComponentSource ForDelegate(string name, Func<Context, ValueTask> apply, IEnumerable<string> inject)
    {
        string[] names = inject.ToArray();
        return new(apply, () => ComponentFactory.ForDelegate(apply, name, apply), () => ComponentFactory.InjectOf(names));
    }

    internal static ComponentSource From(object component) => component switch
    {
        ComponentSource source => source,
        Type type => ForType(type),
        IComponent instance => ForInstance(instance),
        _ => throw new ArgumentException(
            $"invalid component, expect a Type, an IComponent or a ComponentSource, received {component.GetType().Name}",
            nameof(component)),
    };
}

public sealed class RegistryService(Context root)
{
    private readonly Dictionary<object, ComponentRuntime> _runtimes = new(ReferenceEqualityComparer.Instance);
    private readonly Dictionary<object, ComponentSource> _aliases = new(ReferenceEqualityComparer.Instance);
    private int _counter;

    internal int NextUid() => Interlocked.Increment(ref _counter);

    public int Count
    {
        get { lock (root.SyncRoot) return _runtimes.Count; }
    }

    public IReadOnlyList<ComponentRuntime> Runtimes
    {
        get { lock (root.SyncRoot) return [.. _runtimes.Values]; }
    }

    public bool Has(object key)
    {
        lock (root.SyncRoot) return _runtimes.ContainsKey(ForwardKey(key));
    }

    public ComponentRuntime? Get(object key)
    {
        lock (root.SyncRoot) return _runtimes.GetValueOrDefault(ForwardKey(key));
    }

    /// <summary>Follows <see cref="Replace(object, object)"/> forwards to the current implementation.</summary>
    private object ForwardKey(object key)
    {
        while (_aliases.TryGetValue(key, out ComponentSource? next)) key = next.Key;
        return key;
    }

    private ComponentSource Forward(ComponentSource source)
    {
        while (_aliases.TryGetValue(source.Key, out ComponentSource? next)) source = next;
        return source;
    }

    private ComponentRuntime GetOrCreate(ComponentSource source)
    {
        if (_runtimes.TryGetValue(source.Key, out ComponentRuntime? runtime)) return runtime;
        runtime = source.CreateRuntime();
        _runtimes[source.Key] = runtime;
        return runtime;
    }

    /// <summary>Drops a runtime whose last fiber was disposed, unless it was already replaced.</summary>
    internal void Unregister(ComponentRuntime runtime)
    {
        lock (root.SyncRoot)
        {
            if (_runtimes.TryGetValue(runtime.Key, out ComponentRuntime? current) && ReferenceEquals(current, runtime))
            {
                _runtimes.Remove(runtime.Key);
            }
        }
    }

    /// <summary>Create a fiber for a component. Analogue of <c>registry.use()</c>.</summary>
    public Fiber Use(Context ctx, ComponentSource source, object? config = null, IEnumerable<string>? inject = null)
    {
        ctx.Fiber.AssertActive();
        ComponentRuntime runtime;
        lock (root.SyncRoot)
        {
            source = Forward(source);
            runtime = GetOrCreate(source);
        }
        string[] extra = inject?.ToArray() ?? [];
        return new Fiber(ctx, runtime, config, MergeInject(source, extra), extra);
    }

    private static Dictionary<string, object?> MergeInject(ComponentSource source, IReadOnlyList<string> extra)
    {
        Dictionary<string, object?> dependencies = source.ResolveInject();
        foreach (string name in extra) dependencies.TryAdd(name, null);
        return dependencies;
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
        object oldKey = oldComponent is ComponentSource source ? source.Key : oldComponent;

        lock (root.SyncRoot)
        {
            oldKey = ForwardKey(oldKey);
            // `next` is an implementation of its own again
            _aliases.Remove(next.Key);
            ComponentRuntime? runtime = _runtimes.GetValueOrDefault(oldKey);
            ComponentRuntime? target = _runtimes.GetValueOrDefault(next.Key);
            if (!ReferenceEquals(oldKey, next.Key))
            {
                _runtimes.Remove(oldKey);
                _aliases[oldKey] = next;
            }

            IReadOnlyList<Fiber> fibers = runtime?.Fibers ?? [];
            if (fibers.Count == 0) return fibers;
            if (target is null)
            {
                target = next.CreateRuntime();
                _runtimes[next.Key] = target;
            }

            foreach (Fiber fiber in fibers) fiber.Replace(target, MergeInject(next, fiber.ExtraInject));
            return fibers;
        }
    }

    /// <summary>Unload every fiber of a component. Analogue of the former <c>registry.delete()</c>.</summary>
    public async Task RemoveAsync(object key)
    {
        ComponentRuntime? runtime;
        lock (root.SyncRoot)
        {
            key = ForwardKey(key);
            if (!_runtimes.Remove(key, out runtime)) return;
        }
        foreach (Fiber fiber in runtime.Fibers) await fiber.DisposeAsync().ConfigureAwait(false);
    }
}

internal static class ComponentFactory
{
    public static ComponentRuntime ForType(Type type)
    {
        IConfigSchema? schema = FindSchema(type);
        string name = type.Name;
        return new ComponentRuntime(type, name, (ctx, config) =>
        {
            ConstructorInfo ctor = SelectConstructor(type, config is not null);
            ParameterInfo[] parameters = ctor.GetParameters();
            object?[] args = parameters.Length switch
            {
                0 => [],
                1 => [ctx],
                _ => new object?[] { ctx, config ?? DefaultFor(parameters[1]) },
            };
            try
            {
                return new ValueTask<object?>(ctor.Invoke(args));
            }
            catch (TargetInvocationException error) when (error.InnerException is not null)
            {
                ExceptionDispatchInfo.Capture(error.InnerException).Throw();
                throw;
            }
        }, schema);
    }

    public static ComponentRuntime ForInstance(IComponent component)
    {
        string name = component.ComponentName ?? component.GetType().Name;
        return new ComponentRuntime(component, name, async (ctx, config) =>
        {
            await InvokeAsync(component, ctx, config).ConfigureAwait(false);
            return null;
        }, component.ConfigSchema);
    }

    public static ComponentRuntime ForDelegate(object key, string name, Func<Context, ValueTask> apply)
        => new(key, name, async (ctx, _) =>
        {
            await apply(ctx).ConfigureAwait(false);
            return null;
        }, null);

    private static ValueTask InvokeAsync(IComponent component, Context ctx, object? config)
    {
        Type? closed = component.GetType().GetInterfaces()
            .FirstOrDefault(i => i.IsGenericType && i.GetGenericTypeDefinition() == typeof(IComponent<>));
        if (closed is null || config is null) return component.ApplyAsync(ctx);

        MethodInfo method = closed.GetMethod(nameof(IComponent<object>.ApplyAsync), [typeof(Context), closed.GetGenericArguments()[0]])!;
        try
        {
            return (ValueTask)method.Invoke(component, [ctx, config])!;
        }
        catch (TargetInvocationException error) when (error.InnerException is not null)
        {
            ExceptionDispatchInfo.Capture(error.InnerException).Throw();
            throw;
        }
    }

    /// <summary>Reads <c>[Inject]</c> attributes declared on a component type (inherited).</summary>
    public static Dictionary<string, object?> InjectOf(Type type)
    {
        var result = new Dictionary<string, object?>(StringComparer.Ordinal);
        foreach (InjectAttribute attribute in type.GetCustomAttributes<InjectAttribute>(inherit: true))
        {
            result[attribute.Name] = attribute.Config;
        }
        return result;
    }

    public static Dictionary<string, object?> InjectOf(IEnumerable<string> names)
    {
        var result = new Dictionary<string, object?>(StringComparer.Ordinal);
        foreach (string name in names) result[name] = null;
        return result;
    }

    /// <summary>Looks for <c>public static IConfigSchema Schema { get; }</c> on the component type.</summary>
    internal static IConfigSchema? FindSchema(Type type)
    {
        PropertyInfo? property = type.GetProperty("Schema", BindingFlags.Public | BindingFlags.Static | BindingFlags.FlattenHierarchy);
        if (property is not null && typeof(IConfigSchema).IsAssignableFrom(property.PropertyType))
        {
            return (IConfigSchema?)property.GetValue(null);
        }
        FieldInfo? field = type.GetField("Schema", BindingFlags.Public | BindingFlags.Static | BindingFlags.FlattenHierarchy);
        if (field is not null && typeof(IConfigSchema).IsAssignableFrom(field.FieldType))
        {
            return (IConfigSchema?)field.GetValue(null);
        }
        return null;
    }

    private static ConstructorInfo SelectConstructor(Type type, bool hasConfig)
    {
        ConstructorInfo[] ctors = type.GetConstructors(BindingFlags.Public | BindingFlags.Instance);
        ConstructorInfo? withConfig = ctors.FirstOrDefault(c =>
            c.GetParameters().Length == 2 && c.GetParameters()[0].ParameterType == typeof(Context));
        ConstructorInfo? ctxOnly = ctors.FirstOrDefault(c =>
            c.GetParameters().Length == 1 && c.GetParameters()[0].ParameterType == typeof(Context));

        ConstructorInfo? selected = hasConfig ? withConfig ?? ctxOnly : ctxOnly ?? withConfig;
        selected ??= ctors.FirstOrDefault(c => c.GetParameters().Length == 0);

        return selected ?? throw new CordisException(
            $"component type {type.Name} needs a constructor taking (Context) or (Context, TConfig)");
    }

    private static object? DefaultFor(ParameterInfo parameter)
    {
        if (parameter.HasDefaultValue) return parameter.DefaultValue;
        Type type = parameter.ParameterType;
        if (type.IsValueType) return Activator.CreateInstance(type);
        if (type.GetConstructor(Type.EmptyTypes) is not null) return Activator.CreateInstance(type);
        return null;
    }
}
