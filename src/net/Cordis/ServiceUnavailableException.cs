// -----------------------------------------------------------------------
// <copyright file="ServiceUnavailableException.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// Thrown when a required service is admitted for the accessing context but has no active binding.
/// </summary>
/// <param name="name">The service name that was accessed.</param>
public sealed class ServiceUnavailableException(string name)
    : CordisException($"service \"{name}\" is not available in this context");
