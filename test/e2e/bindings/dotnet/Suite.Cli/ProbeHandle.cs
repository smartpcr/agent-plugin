// -----------------------------------------------------------------------
// <copyright file="ProbeHandle.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.E2E;

/// <summary>What a probe provides under a service name: who provided it, and whether its resource is released.</summary>
/// <param name="provider">The id of the probe that provides it.</param>
public sealed class ProbeHandle(string provider)
{
    /// <summary>Gets the id of the probe that provides the service.</summary>
    public string Provider { get; } = provider;

    /// <summary>Gets or sets a value indicating whether the service's resource has been released.</summary>
    public bool Closed { get; set; }

    /// <inheritdoc/>
    public override string ToString() => this.Provider;
}
