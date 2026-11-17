// -----------------------------------------------------------------------
// <copyright file="EntryOptions.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.Loading;

using System.Text.Json;

/// <summary>Declarative description of one component instance in the config file.</summary>
public sealed class EntryOptions
{
    /// <summary>
    /// Gets or sets the entry's id among its siblings. It must not contain
    /// <see cref="LoaderService.Separator"/>; an empty id is replaced by a generated one.
    /// </summary>
    public string Id { get; set; } = string.Empty;

    /// <summary>Gets or sets the component identifier handed to the <see cref="IComponentResolver"/>. Required unless the entry is a group.</summary>
    public string Name { get; set; } = string.Empty;

    /// <summary>Gets or sets the raw JSON config bound to the component's config type, or <c>null</c> for none.</summary>
    public JsonElement? Config { get; set; }

    /// <summary>Gets or sets a value indicating whether the entry is a group: a context node that loads no component but scopes its children.</summary>
    public bool Group { get; set; }

    /// <summary>Gets or sets a value indicating whether the entry is disabled; a disabled entry, and any children it has, do not load.</summary>
    public bool Disabled { get; set; }

    /// <summary>Gets or sets the names of the services the entry requires before it loads, or <c>null</c> for none.</summary>
    public string[]? Inject { get; set; }

    /// <summary>
    /// Gets or sets the map from service name to realm label. Entries sharing a label share the realm for that service;
    /// <c>"true"</c> makes a realm private to this entry. Any other string, including the empty
    /// string, is an ordinary shared label.
    /// </summary>
    public Dictionary<string, string>? Isolate { get; set; }

    /// <summary>Gets or sets the child entries of a group, created on the group's context while it is loaded.</summary>
    public List<EntryOptions>? Children { get; set; }

    /// <summary>Creates a deep copy of the options, including the children.</summary>
    /// <returns>A new <see cref="EntryOptions"/> that shares no mutable collection with this one.</returns>
    public EntryOptions Clone() => new()
    {
        Id = this.Id,
        Name = this.Name,
        Config = this.Config,
        Group = this.Group,
        Disabled = this.Disabled,
        Inject = this.Inject?.ToArray(),
        Isolate = this.Isolate is null ? null : new Dictionary<string, string>(this.Isolate),
        Children = this.Children?.Select(child => child.Clone()).ToList(),
    };

    /// <summary>
    /// Compares the fields that require rebuilding a live entry when they change: name, group flag,
    /// disabled flag, injected services and isolation. Id, config and children are not compared.
    /// </summary>
    /// <param name="other">The options to compare with.</param>
    /// <returns><c>true</c> if a live entry can keep its fiber when moving to <paramref name="other"/>; otherwise <c>false</c>.</returns>
    public bool SameShape(EntryOptions other)
        => this.Name == other.Name
        && this.Group == other.Group
        && this.Disabled == other.Disabled
        && (this.Inject ?? []).SequenceEqual(other.Inject ?? [])
        && IsolateEquals(this.Isolate, other.Isolate);

    private static bool IsolateEquals(Dictionary<string, string>? left, Dictionary<string, string>? right)
    {
        if (left is null || left.Count == 0)
        {
            return right is null || right.Count == 0;
        }

        if (right is null || left.Count != right.Count)
        {
            return false;
        }

        return left.All(pair => right.TryGetValue(pair.Key, out var value) && value == pair.Value);
    }
}
