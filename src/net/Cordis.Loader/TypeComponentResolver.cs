// -----------------------------------------------------------------------
// <copyright file="TypeComponentResolver.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.Loading;

using System.Reflection;

/// <summary>Resolves assembly-qualified or short type names against the loaded assemblies.</summary>
public sealed class TypeComponentResolver : IComponentResolver
{
    private readonly List<Assembly> assemblies = [];

    /// <summary>Initializes a new instance of the <see cref="TypeComponentResolver"/> class.</summary>
    /// <param name="assemblies">
    /// The assemblies to search. When none are given, every assembly loaded in the current
    /// application domain is searched.
    /// </param>
    public TypeComponentResolver(params Assembly[] assemblies) => this.assemblies.AddRange(assemblies);

    /// <summary>Adds an assembly to the search list.</summary>
    /// <param name="assembly">The assembly to search.</param>
    /// <returns>This resolver, for chaining.</returns>
    public IComponentResolver Add(Assembly assembly)
    {
        this.assemblies.Add(assembly);
        return this;
    }

    /// <summary>
    /// Resolves <paramref name="name"/> first with <see cref="Type.GetType(string, bool)"/>, then in
    /// each searched assembly by full name, then by short type name.
    /// </summary>
    /// <param name="name">An assembly-qualified, full or short type name.</param>
    /// <returns>The first matching type, or <c>null</c> if none matches.</returns>
    public Type? Resolve(string name)
    {
        var type = Type.GetType(name, throwOnError: false);
        if (type is not null)
        {
            return type;
        }

        var candidates = this.assemblies.Count > 0 ? this.assemblies : [.. AppDomain.CurrentDomain.GetAssemblies()];
        foreach (var assembly in candidates)
        {
            type = assembly.GetType(name, throwOnError: false);
            if (type is not null)
            {
                return type;
            }

            type = assembly.GetTypes().FirstOrDefault(candidate => candidate.Name == name);
            if (type is not null)
            {
                return type;
            }
        }

        return null;
    }
}
