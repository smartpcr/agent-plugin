// -----------------------------------------------------------------------
// <copyright file="Entry.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.Loading;

using System.Text.Json;

/// <summary>
/// The live record of one entry in a <see cref="LoaderService"/> tree: its current options and,
/// while it runs, the fiber that realises it.
/// </summary>
/// <param name="id">The composite id of the entry.</param>
/// <param name="parentId">The composite id of the enclosing group entry, or <c>null</c> at the top level.</param>
public sealed class Entry(string id, string? parentId)
{
    /// <summary>Gets the composite id: the ids of the enclosing groups and this entry, joined by <c>:</c>.</summary>
    public string Id { get; } = id;

    /// <summary>Gets the composite id of the enclosing group entry, or <c>null</c> at the top level.</summary>
    public string? ParentId { get; } = parentId;

    /// <summary>
    /// Gets the options the entry was last reconciled with, without its children. A runtime config
    /// commit on the entry's fiber replaces them with the committed config.
    /// </summary>
    public EntryOptions Options { get; internal set; } = new() { Id = id };

    /// <summary>
    /// Gets the fiber that realises the entry, or <c>null</c> while the entry is disabled, its
    /// enclosing group is not loaded, or it is being rebuilt.
    /// </summary>
    public Fiber? Fiber { get; internal set; }

    /// <summary>Gets the context the children of a group entry are created on; <c>null</c> unless the group is loaded.</summary>
    public Context? Scope { get; internal set; }

    /// <summary>Gets a value indicating whether the entry's fiber exists and is in the <see cref="FiberState.Active"/> state.</summary>
    public bool IsActive => this.Fiber is { State: FiberState.Active };

    internal int Order { get; set; }

    /// <summary>Gets or sets the config the live fiber was last given, to detect config-only edits.</summary>
    internal JsonElement? AppliedConfig { get; set; }

    /// <summary>Returns a short description of the entry for logs.</summary>
    /// <returns>A string with the entry's composite id and component name.</returns>
    public override string ToString() => $"Entry {this.Id} ({this.Options.Name})";
}
