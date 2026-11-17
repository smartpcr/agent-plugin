// -----------------------------------------------------------------------
// <copyright file="ProbeResolver.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.E2E;

using Cordis.Loading;

/// <summary>Resolves the component names a loader entry may use.</summary>
internal sealed class ProbeResolver : IComponentResolver
{
    public Type? Resolve(string name) => name == "probe" ? typeof(ProbeComponent) : null;
}
