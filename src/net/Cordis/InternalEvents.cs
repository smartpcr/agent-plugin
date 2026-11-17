// -----------------------------------------------------------------------
// <copyright file="InternalEvents.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// Synchronous lifecycle notifications used by the framework and by the loader.
/// Analogue of cordis' <c>internal/*</c> events.
/// </summary>
public sealed class InternalEvents
{
    /// <summary>A fiber was created (Uid set) or disposed (Uid null).</summary>
    public event Action<Fiber>? PluginChanged;

    /// <summary>
    /// A fiber changed state. The handler receives the fiber and its previous
    /// <see cref="FiberState"/>; the new state is read from the fiber.
    /// </summary>
    public event Action<Fiber, FiberState>? StatusChanged;

    /// <summary>
    /// A service binding changed. The handler receives the service name and the new value, which
    /// is <see langword="null"/> when the service was removed.
    /// </summary>
    public event Action<string, object?>? ServiceChanged;

    /// <summary>
    /// Raised by <see cref="Fiber.UpdateAsync"/> (unless <c>noSave</c>) with the original,
    /// unvalidated config, so a loader can persist it. Analogue of <c>internal/commit</c>.
    /// </summary>
    public event Action<Fiber, object?>? Committing;

    /// <summary>A fiber was moved to a new component by <see cref="RegistryService.Replace"/>.</summary>
    public event Action<Fiber>? Replaced;

    internal void RaisePluginChanged(Fiber fiber) => this.PluginChanged?.Invoke(fiber);

    internal void RaiseStatusChanged(Fiber fiber, FiberState old) => this.StatusChanged?.Invoke(fiber, old);

    internal void RaiseServiceChanged(string name, object? value) => this.ServiceChanged?.Invoke(name, value);

    internal void RaiseCommitting(Fiber fiber, object? config) => this.Committing?.Invoke(fiber, config);

    internal void RaiseReplaced(Fiber fiber) => this.Replaced?.Invoke(fiber);
}
