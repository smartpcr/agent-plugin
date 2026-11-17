// -----------------------------------------------------------------------
// <copyright file="IsolateKey.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// Reference-identity token that names an isolation realm. The C# analogue of <c>Symbol(name)</c>:
/// two keys with the same name are still distinct.
/// </summary>
/// <param name="name">The descriptive name of the token, used for diagnostics.</param>
public sealed class IsolateKey(string name)
{
    /// <summary>Gets the descriptive name of the token, used for diagnostics.</summary>
    public string Name { get; } = name;

    /// <inheritdoc/>
    public override string ToString() => $"Symbol({this.Name})";
}
