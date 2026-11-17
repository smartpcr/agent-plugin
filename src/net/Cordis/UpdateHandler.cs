// -----------------------------------------------------------------------
// <copyright file="UpdateHandler.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// A per-fiber update hook. Receives the resolved config and a <c>next</c> continuation;
/// calling <c>next</c> lets the update proceed (eventually restarting the fiber), not calling it
/// accepts the update in place. Analogue of a non-global <c>internal/update</c> listener.
/// Register one with <see cref="Context.OnUpdate(UpdateHandler, bool)"/>.
/// </summary>
/// <param name="resolvedConfig">The new config after validation by the component's schema, or the raw config when the component has no schema.</param>
/// <param name="next">
/// Continues to the next registered hook, or restarts the fiber when no hooks remain. Call it at
/// most once; a second call throws <see cref="InvalidOperationException"/>.
/// </param>
/// <returns>A task that completes when the hook has handled the update (including any awaited <paramref name="next"/>).</returns>
public delegate Task UpdateHandler(object? resolvedConfig, Func<Task> next);
