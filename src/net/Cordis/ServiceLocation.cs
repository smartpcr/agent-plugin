// -----------------------------------------------------------------------
// <copyright file="ServiceLocation.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>Where an access to a service name lands. See <see cref="ServiceStore.Locate"/>.</summary>
/// <param name="Fiber">The fiber whose declaration admits the access; <c>null</c> for an own provision.</param>
/// <param name="Impl">The binding the access reaches; <c>null</c> if the declaring fiber is inactive.</param>
public readonly record struct ServiceLocation(Fiber? Fiber, ServiceImpl? Impl);
