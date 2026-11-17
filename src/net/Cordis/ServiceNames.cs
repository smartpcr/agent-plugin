// -----------------------------------------------------------------------
// <copyright file="ServiceNames.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

using System.Collections.Concurrent;
using System.Reflection;

/// <summary>
/// Maps a type to the string key its service is registered and resolved under: the
/// <see cref="ServiceNameAttribute.Name"/> declared directly on the type, otherwise its full name.
/// Results are cached per type.
/// </summary>
public static class ServiceNames
{
    private static readonly ConcurrentDictionary<Type, string> Cache = new();

    /// <summary>Gets the service name of <typeparamref name="T"/>.</summary>
    /// <typeparam name="T">The service type.</typeparam>
    /// <returns>The service name of <typeparamref name="T"/>.</returns>
    public static string For<T>() => For(typeof(T));

    /// <summary>Gets the service name of a type.</summary>
    /// <param name="type">The service type.</param>
    /// <returns>The <see cref="ServiceNameAttribute.Name"/> declared directly on <paramref name="type"/>, otherwise its full name.</returns>
    public static string For(Type type) => Cache.GetOrAdd(type, static t =>
        t.GetCustomAttribute<ServiceNameAttribute>(inherit: false)?.Name ?? t.FullName ?? t.Name);
}
