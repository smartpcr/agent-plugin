// -----------------------------------------------------------------------
// <copyright file="InjectAttribute.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// Declares a dependency of a plugin type. Analogue of <c>static inject = [...]</c>. The
/// component's context may access the declared service, and its fiber reloads when the binding
/// behind the name changes.
/// </summary>
[AttributeUsage(AttributeTargets.Class, AllowMultiple = true, Inherited = true)]
public sealed class InjectAttribute : Attribute
{
    /// <summary>Initializes a new instance of the <see cref="InjectAttribute"/> class.</summary>
    /// <param name="name">The name of the service to inject.</param>
    public InjectAttribute(string name) => this.Name = name;

    /// <summary>Initializes a new instance of the <see cref="InjectAttribute"/> class.</summary>
    /// <param name="serviceType">The service type; its name is taken from <see cref="ServiceNames.For(Type)"/>.</param>
    public InjectAttribute(Type serviceType) => this.Name = ServiceNames.For(serviceType);

    /// <summary>Gets the name of the service to inject.</summary>
    public string Name { get; }

    /// <summary>Gets or sets optional per-dependency interception config pushed onto the child context.</summary>
    public object? Config { get; set; }
}
