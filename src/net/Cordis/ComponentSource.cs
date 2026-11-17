// -----------------------------------------------------------------------
// <copyright file="ComponentSource.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>How to build a runtime for one component identity, and what it injects.</summary>
public sealed class ComponentSource
{
    private readonly Func<ComponentRuntime> create;
    private readonly Func<Dictionary<string, object?>> inject;

    private ComponentSource(object key, Func<ComponentRuntime> create, Func<Dictionary<string, object?>> inject)
    {
        this.Key = key;
        this.create = create;
        this.inject = inject;
    }

    /// <summary>Gets the registry key: the type, the component instance, or the delegate.</summary>
    public object Key { get; }

    /// <summary>
    /// Creates a source for a component type. Each load constructs the type through a public
    /// constructor taking <c>(Context, TConfig)</c>, <c>(Context)</c> or no arguments; dependencies come
    /// from its <see cref="InjectAttribute"/> declarations and its config schema from a public static
    /// <c>Schema</c> property or field.
    /// </summary>
    /// <param name="type">The component type; it is also the registry key.</param>
    /// <returns>A source keyed by <paramref name="type"/>.</returns>
    public static ComponentSource ForType(Type type)
        => new(type, () => ComponentFactory.ForType(type), () => ComponentFactory.InjectOf(type));

    /// <summary>
    /// Creates a source for an object-style component. Each load calls its
    /// <see cref="IComponent.ApplyAsync(Context)"/> (or the typed overload when config is supplied);
    /// dependencies are the union of <see cref="IComponent.Inject"/> and the
    /// <see cref="InjectAttribute"/> declarations on its type.
    /// </summary>
    /// <param name="component">The component instance; it is also the registry key.</param>
    /// <returns>A source keyed by <paramref name="component"/>.</returns>
    public static ComponentSource ForInstance(IComponent component)
        => new(component, () => ComponentFactory.ForInstance(component), () =>
        {
            var inject = ComponentFactory.InjectOf(component.Inject);
            foreach ((var name, var value) in ComponentFactory.InjectOf(component.GetType())) inject[name] = value;
            return inject;
        });

    /// <summary>
    /// Creates a source for a delegate component. Each load invokes <paramref name="apply"/>; the
    /// component has no config schema.
    /// </summary>
    /// <param name="name">The display name of the component.</param>
    /// <param name="apply">The delegate that loads the component; it is also the registry key.</param>
    /// <param name="inject">The service names the component depends on.</param>
    /// <returns>A source keyed by <paramref name="apply"/>.</returns>
    public static ComponentSource ForDelegate(string name, Func<Context, ValueTask> apply, IEnumerable<string> inject)
    {
        var names = inject.ToArray();
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

    internal ComponentRuntime CreateRuntime() => this.create();

    internal Dictionary<string, object?> ResolveInject() => this.inject();
}
