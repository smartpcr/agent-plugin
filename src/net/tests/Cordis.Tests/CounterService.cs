// -----------------------------------------------------------------------
// <copyright file="CounterService.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

using Cordis;

/// <summary>Test service registered under the <c>counter</c> key that holds a simple counter.</summary>
/// <param name="ctx">The context that provides the service.</param>
[ServiceName("counter")]
public sealed class CounterService(Context ctx) : Service(ctx, "counter")
{
    /// <summary>Gets the current counter value.</summary>
    public int Value { get; private set; }

    /// <summary>Increments <see cref="Value"/> by one.</summary>
    public void Increase() => this.Value++;
}
