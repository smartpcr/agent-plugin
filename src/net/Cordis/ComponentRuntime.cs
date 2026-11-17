// -----------------------------------------------------------------------
// <copyright file="ComponentRuntime.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// A component registration. One runtime per component identity (type, instance or delegate);
/// one <see cref="Fiber"/> per use.
/// </summary>
/// <param name="key">The registry key: the type, the component instance, or the delegate.</param>
/// <param name="name">The display name of the component.</param>
/// <param name="apply">Loads the component into a context with its validated config and returns the created instance, if any.</param>
/// <param name="schema">The schema that validates the config on every load, or <see langword="null"/> to pass config through unchanged.</param>
public sealed class ComponentRuntime(
    object key,
    string name,
    Func<Context, object?, ValueTask<object?>> apply,
    IConfigSchema? schema)
{
    private readonly List<Fiber> fibers = [];

    /// <summary>
    /// Gets the registry key: the type, the component instance, or the delegate.
    /// </summary>
    public object Key { get; } = key;

    /// <summary>
    /// Gets the display name of the component.
    /// </summary>
    public string Name { get; } = name;

    /// <summary>
    /// Gets the schema that validates the config on every load, or <see langword="null"/> if the component has none.
    /// </summary>
    public IConfigSchema? Schema { get; } = schema;

    /// <summary>
    /// Gets a snapshot of the fibers currently attached to this runtime, one per use.
    /// </summary>
    public IReadOnlyList<Fiber> Fibers
    {
        get
        {
            lock (this.fibers)
            {
                return [.. this.fibers];
            }
        }
    }

    /// <summary>
    /// Gets the number of fibers currently attached to this runtime.
    /// </summary>
    public int FiberCount
    {
        get
        {
            lock (this.fibers)
            {
                return this.fibers.Count;
            }
        }
    }

    internal Func<Context, object?, ValueTask<object?>> Apply { get; } = apply;

    internal void Attach(Fiber fiber)
    {
        lock (this.fibers)
        {
            this.fibers.Add(fiber);
        }
    }

    internal void Detach(Fiber fiber)
    {
        lock (this.fibers)
        {
            this.fibers.Remove(fiber);
        }
    }
}
