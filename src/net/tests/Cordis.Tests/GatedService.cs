// -----------------------------------------------------------------------
// <copyright file="GatedService.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

using Cordis;

/// <summary>
/// Test service registered under the <c>gated</c> key whose asynchronous initialization waits for
/// <see cref="Ready"/>, so tests can observe dependents while the service is still pending.
/// </summary>
/// <param name="ctx">The context that provides the service.</param>
[ServiceName("gated")]
public sealed class GatedService(Context ctx) : Service(ctx, "gated"), IAsyncInit
{
    /// <summary>The gate that the test completes to let <see cref="InitAsync"/> finish.</summary>
    public static readonly TaskCompletionSource Ready = new(TaskCreationOptions.RunContinuationsAsynchronously);

    /// <inheritdoc/>
    public async ValueTask InitAsync() => await Ready.Task;
}
