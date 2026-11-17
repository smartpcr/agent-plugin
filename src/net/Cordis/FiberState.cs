// -----------------------------------------------------------------------
// <copyright file="FiberState.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// The lifecycle state of a <see cref="Fiber"/>. Changes are reported through
/// <see cref="InternalEvents.StatusChanged"/>; only entering or leaving <see cref="Active"/> is
/// observable by fibers that depend on the services this fiber provides.
/// </summary>
public enum FiberState
{
    /// <summary>
    /// The fiber is alive but not loaded, because at least one injected service has no usable
    /// provider; it loads as soon as every dependency becomes available.
    /// </summary>
    Pending,

    /// <summary>
    /// The component body is running, or async effects it started are still completing.
    /// </summary>
    Loading,

    /// <summary>
    /// The component loaded successfully against its current dependencies.
    /// </summary>
    Active,

    /// <summary>
    /// The last load threw (see <see cref="Fiber.Error"/>); the fiber stays failed until
    /// <see cref="Fiber.UpdateAsync(object, bool)"/> or a component replacement clears the error.
    /// </summary>
    Failed,

    /// <summary>
    /// The fiber has been disposed and will never load again.
    /// </summary>
    Disposed,

    /// <summary>
    /// The fiber is tearing down: it waits for dependents to unload, then disposes its effects.
    /// </summary>
    Unloading,
}
