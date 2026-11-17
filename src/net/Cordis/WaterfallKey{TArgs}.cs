// -----------------------------------------------------------------------
// <copyright file="WaterfallKey{TArgs}.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// Identifies a middleware chain over a value. Handlers receive <c>next</c> and may transform the
/// value before or after calling it. Dispatched by <see cref="EventsService.WaterfallAsync{TArgs}"/>.
/// Keys are compared by reference identity, so two keys with the same name are distinct events.
/// </summary>
/// <typeparam name="TArgs">The type of the value flowing through the chain.</typeparam>
/// <param name="name">The event name, used for diagnostics.</param>
public sealed class WaterfallKey<TArgs>(string name)
{
    /// <summary>Gets the event name, used for diagnostics.</summary>
    public string Name { get; } = name;

    /// <inheritdoc/>
    public override string ToString() => this.Name;
}
