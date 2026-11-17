// -----------------------------------------------------------------------
// <copyright file="ServiceNotInjectedException.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// Thrown when a component context accesses a service that no fiber on its chain declares in
/// <c>Inject</c> or provides itself.
/// </summary>
/// <param name="name">The service name that was accessed.</param>
public sealed class ServiceNotInjectedException(string name)
    : CordisException($"cannot access service \"{name}\" without declaring it in inject");
