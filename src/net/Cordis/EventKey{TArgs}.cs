// -----------------------------------------------------------------------
// <copyright file="EventKey{TArgs}.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// Identifies a fire-and-forget or awaited notification event. Handlers: <c>Func&lt;Context, TArgs, ValueTask&gt;</c>.
/// Dispatched sequentially by <see cref="EventsService.EmitAsync{TArgs}"/> or concurrently by
/// <see cref="EventsService.ParallelAsync{TArgs}"/>. Keys are compared by reference identity, so two
/// keys with the same name are distinct events.
/// </summary>
/// <typeparam name="TArgs">The type of the event arguments.</typeparam>
/// <param name="name">The event name, used for diagnostics.</param>
public sealed class EventKey<TArgs>(string name)
{
    /// <summary>Gets the event name, used for diagnostics.</summary>
    public string Name { get; } = name;

    /// <inheritdoc/>
    public override string ToString() => this.Name;
}
