// -----------------------------------------------------------------------
// <copyright file="BailKey{TArgs,TResult}.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// Identifies an event whose first non-null result wins. Handlers: <c>Func&lt;Context, TArgs, ValueTask&lt;TResult?&gt;&gt;</c>.
/// Dispatched by <see cref="EventsService.BailAsync{TArgs, TResult}"/>. Keys are compared by
/// reference identity, so two keys with the same name are distinct events.
/// </summary>
/// <typeparam name="TArgs">The type of the event arguments.</typeparam>
/// <typeparam name="TResult">The type of the result a handler may produce.</typeparam>
/// <param name="name">The event name, used for diagnostics.</param>
public sealed class BailKey<TArgs, TResult>(string name)
{
    /// <summary>Gets the event name, used for diagnostics.</summary>
    public string Name { get; } = name;

    /// <inheritdoc/>
    public override string ToString() => this.Name;
}
