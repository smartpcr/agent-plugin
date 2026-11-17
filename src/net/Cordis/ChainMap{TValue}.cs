// -----------------------------------------------------------------------
// <copyright file="ChainMap{TValue}.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// A dictionary that falls through to its parent on lookup and writes locally.
/// The C# analogue of a JavaScript prototype chain (<c>Object.create(parent)</c>).
/// </summary>
/// <typeparam name="TValue">The type of the stored values.</typeparam>
/// <param name="parent">The map consulted when a key is not bound locally, or <see langword="null"/> for a root map.</param>
public sealed class ChainMap<TValue>(ChainMap<TValue>? parent = null)
{
    private readonly Dictionary<string, TValue> own = new(StringComparer.Ordinal);

    /// <summary>Gets the map consulted when a key is not bound locally, or <see langword="null"/> for a root map.</summary>
    public ChainMap<TValue>? Parent { get; } = parent;

    /// <summary>
    /// Looks up <paramref name="key"/> in this map, then in each ancestor, using ordinal comparison.
    /// </summary>
    /// <param name="key">The key to look up.</param>
    /// <param name="value">When this method returns, the nearest value bound to <paramref name="key"/>, or the default value if none was found.</param>
    /// <returns><see langword="true"/> if this map or an ancestor binds <paramref name="key"/>; otherwise, <see langword="false"/>.</returns>
    public bool TryGet(string key, out TValue value)
    {
        for (var map = this; map is not null; map = map.Parent)
        {
            if (map.own.TryGetValue(key, out value!))
            {
                return true;
            }
        }

        value = default!;
        return false;
    }

    /// <summary>Gets the nearest value bound to <paramref name="key"/> in this map or an ancestor.</summary>
    /// <param name="key">The key to look up.</param>
    /// <returns>The nearest bound value, or the default value if no map in the chain binds <paramref name="key"/>.</returns>
    public TValue? Get(string key) => this.TryGet(key, out var value) ? value : default;

    /// <summary>
    /// Determines whether <paramref name="key"/> is bound in this map itself, ignoring ancestors.
    /// The analogue of <c>Object.hasOwn</c>.
    /// </summary>
    /// <param name="key">The key to check.</param>
    /// <returns><see langword="true"/> if this map binds <paramref name="key"/> locally; otherwise, <see langword="false"/>.</returns>
    public bool HasOwn(string key) => this.own.ContainsKey(key);

    /// <summary>
    /// Binds <paramref name="key"/> to <paramref name="value"/> in this map, shadowing any binding in
    /// an ancestor. Ancestors are never modified.
    /// </summary>
    /// <param name="key">The key to bind.</param>
    /// <param name="value">The value to store.</param>
    public void Set(string key, TValue value) => this.own[key] = value;

    /// <summary>Enumerates all values bound to <paramref name="key"/>, nearest first.</summary>
    /// <param name="key">The key to look up.</param>
    /// <returns>A lazily evaluated sequence of the values bound to <paramref name="key"/> in this map and each ancestor, nearest first.</returns>
    public IEnumerable<TValue> Chain(string key)
    {
        for (var map = this; map is not null; map = map.Parent)
        {
            if (map.own.TryGetValue(key, out var value))
            {
                yield return value;
            }
        }
    }

    /// <summary>Creates a child map whose lookups fall through to this map.</summary>
    /// <returns>A new, empty <see cref="ChainMap{TValue}"/> whose <see cref="Parent"/> is this map.</returns>
    public ChainMap<TValue> Extend() => new(this);
}
