// -----------------------------------------------------------------------
// <copyright file="ServiceNameAttribute.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>Overrides the string key a type is registered/resolved under. Not inherited by derived types.</summary>
/// <param name="name">The service name to use for the type.</param>
[AttributeUsage(AttributeTargets.Class | AttributeTargets.Interface)]
public sealed class ServiceNameAttribute(string name) : Attribute
{
    /// <summary>Gets the service name to use for the type.</summary>
    public string Name { get; } = name;
}
