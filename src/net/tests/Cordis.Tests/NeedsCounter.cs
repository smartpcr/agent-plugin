// -----------------------------------------------------------------------
// <copyright file="NeedsCounter.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

using Cordis;

/// <summary>Test component that injects the <c>counter</c> service and records the value it observes on load.</summary>
/// <param name="ctx">The context that provides the <see cref="CounterService"/>.</param>
[Inject("counter")]
public sealed class NeedsCounter(Context ctx)
{
    /// <summary>Gets or sets the number of instances constructed.</summary>
    public static int Loads { get; set; }

    /// <summary>Gets the <see cref="CounterService.Value"/> observed when this instance was constructed.</summary>
    public int Observed { get; } = Loads++ >= 0 ? ctx.Get<CounterService>().Value : 0;
}
