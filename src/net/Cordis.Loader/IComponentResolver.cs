// -----------------------------------------------------------------------
// <copyright file="IComponentResolver.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.Loading;

/// <summary>Maps the component name of an <see cref="EntryOptions"/> to the component type to load.</summary>
public interface IComponentResolver
{
    /// <summary>Finds the component type for <paramref name="name"/>.</summary>
    /// <param name="name">The component identifier from <see cref="EntryOptions.Name"/>.</param>
    /// <returns>The component type, or <c>null</c> if no type matches.</returns>
    Type? Resolve(string name);
}
