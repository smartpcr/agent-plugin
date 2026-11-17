// -----------------------------------------------------------------------
// <copyright file="LoaderService.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.Loading;

using System.Reflection;
using System.Text.Json;
using System.Text.Json.Serialization;

/// <summary>
/// Turns a declarative entry list into a live component tree and keeps them in sync.
/// Analogue of <c>@cordisjs/loader</c>.
/// </summary>
/// <remarks>
/// A group's children are created by the group's own body, on the group's context. They load
/// only while the group is loaded, and a group that is disabled, pending on its <c>Inject</c>,
/// or rebuilt takes its children down with it and recreates them when it loads again.
/// <para>
/// An invalid entry (an id containing <see cref="Separator"/>, a missing name, or a duplicate id)
/// is logged and skipped together with its subtree; its siblings still load. Each
/// <see cref="EntryOptions.Isolate"/> label maps to one realm per service, shared by every entry
/// that uses the same label.
/// </para>
/// </remarks>
[ServiceName("loader")]
public sealed class LoaderService : Service
{
    /// <summary>The character that joins entry ids into a composite id, such as <c>group:child</c>.</summary>
    public const char Separator = ':';

    /// <summary>
    /// The JSON options used to read and write entry trees and bind component configs: web
    /// defaults, case-insensitive names, nulls omitted, indented output.
    /// </summary>
    public static readonly JsonSerializerOptions JsonOptions = new(JsonSerializerDefaults.Web)
    {
        PropertyNameCaseInsensitive = true,
        DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
        WriteIndented = true,
    };

    private readonly Dictionary<string, Entry> entries = new(StringComparer.Ordinal);
    private readonly Dictionary<Fiber, Entry> byFiber = [];
    private readonly Dictionary<string, IsolateKey> realms = new(StringComparer.Ordinal);
    private readonly SemaphoreSlim apply = new(1, 1);
    private List<EntryOptions> current = [];

    /// <summary>Initializes a new instance of the <see cref="LoaderService"/> class.</summary>
    /// <param name="ctx">
    /// The context the service and top-level entries are created on. Config commits of entry
    /// fibers are tracked for as long as this context's fiber is loaded.
    /// </param>
    public LoaderService(Context ctx)
        : base(ctx, "loader")
    {
        this.Resolver = new TypeComponentResolver();
        ctx.Internal.Committing += this.OnCommitting;
        ctx.Effect(() => () => ctx.Internal.Committing -= this.OnCommitting, "loader/committing");
    }

    /// <summary>Raised after a fiber commits a config change, so a file source can persist it.</summary>
    public event Action? ConfigChanged;

    /// <summary>
    /// Gets or sets the resolver that maps <see cref="EntryOptions.Name"/> to a component type.
    /// Defaults to a <see cref="TypeComponentResolver"/> over the loaded assemblies.
    /// </summary>
    public IComponentResolver Resolver { get; set; }

    /// <summary>Gets a snapshot of the live entries, including those that are currently not loaded.</summary>
    public IReadOnlyCollection<Entry> Entries
    {
        get
        {
            lock (this.entries)
            {
                return [.. this.entries.Values];
            }
        }
    }

    /// <summary>Finds a live entry by composite id.</summary>
    /// <param name="id">The composite id, such as <c>group:child</c>.</param>
    /// <returns>The entry, or <c>null</c> if no entry has that id.</returns>
    public Entry? Find(string id)
    {
        lock (this.entries)
        {
            return this.entries.GetValueOrDefault(id);
        }
    }

    /// <summary>Returns the current entry tree, as it would be written back to disk.</summary>
    /// <returns>A deep copy of the last applied tree, including config committed at runtime since.</returns>
    public IReadOnlyList<EntryOptions> Snapshot()
    {
        lock (this.entries)
        {
            return this.current.Select(options => options.Clone()).ToList();
        }
    }

    /// <summary>
    /// Diffs the desired tree against the live one and reconciles. Applies are serialised. An
    /// invalid or failing entry is logged and skipped; it never stops its siblings.
    /// </summary>
    /// <remarks>
    /// Entries missing from <paramref name="desired"/> are disposed. Entries whose shape changed
    /// (see <see cref="EntryOptions.SameShape(EntryOptions)"/>) are rebuilt; a config-only edit is
    /// pushed to the live fiber. Children of a group are created by the group itself once it loads.
    /// </remarks>
    /// <param name="desired">The desired entry tree. It is copied; the caller's objects are not retained.</param>
    /// <returns>A task that completes when the tree has been reconciled and every entry's fiber has settled.</returns>
    public async Task ApplyAsync(IReadOnlyList<EntryOptions> desired)
    {
        await this.apply.WaitAsync().ConfigureAwait(false);
        try
        {
            var tree = desired.Select(options => options.Clone()).ToList();
            var flattened = new Dictionary<string, (EntryOptions Options, string? ParentId, int Order)>(StringComparer.Ordinal);
            var order = 0;
            this.Flatten(tree, null, flattened, ref order);

            string[] removed;
            lock (this.entries)
            {
                removed = [.. this.entries.Keys.Where(id => !flattened.ContainsKey(id))];
            }

            foreach (var id in removed)
            {
                await this.RemoveAsync(id).ConfigureAwait(false);
            }

            var sequence = flattened.OrderBy(pair => pair.Value.Order).Select(pair => pair.Key).ToArray();

            // 1. records: rebuild entries whose shape changed, so step 2 recreates them
            foreach (var id in sequence)
            {
                (var options, var parentId, var position) = flattened[id];
                Entry? entry;
                lock (this.entries)
                {
                    entry = this.entries.GetValueOrDefault(id);
                }

                if (entry is null)
                {
                    entry = new Entry(id, parentId);
                    lock (this.entries)
                    {
                        this.entries[id] = entry;
                    }
                }
                else if (!entry.Options.SameShape(options))
                {
                    await this.DisposeFiberAsync(entry).ConfigureAwait(false);
                }

                lock (this.entries)
                {
                    entry.Options = WithoutChildren(options);
                    entry.Order = position;
                }
            }

            // 2. materialise, parents first; children of unloaded groups wait for their group
            var updates = new List<Task>();
            foreach (var id in sequence)
            {
                var entry = this.Find(id);
                if (entry is null)
                {
                    continue;
                }

                try
                {
                    if (this.Ensure(entry) is { } update)
                    {
                        updates.Add(update);
                    }
                }
                catch (Exception error)
                {
                    this.Ctx.Logger.Error(error);
                }
            }

            await Task.WhenAll(updates).ConfigureAwait(false);

            lock (this.entries)
            {
                this.current = tree;
            }
        }
        finally
        {
            this.apply.Release();
        }

        await this.WaitAllAsync().ConfigureAwait(false);
    }

    /// <summary>Waits until every entry's fiber settles. Failures are reported by the fibers themselves.</summary>
    /// <returns>A task that completes when every current entry fiber has settled; it never faults for a failed fiber.</returns>
    public async Task WaitAllAsync()
    {
        foreach (var entry in this.Entries)
        {
            if (entry.Fiber is null)
            {
                continue;
            }

            try
            {
                await entry.Fiber.WaitAsync().ConfigureAwait(false);
            }
            catch
            { /* reported by the fiber */
            }
        }
    }

    /// <summary>
    /// Enables or disables one entry, reapplies the tree, and raises <see cref="ConfigChanged"/>
    /// so a file source can persist the change. Disabling a group also unloads its children.
    /// </summary>
    /// <param name="id">The composite id of the entry.</param>
    /// <param name="disabled"><c>true</c> to disable the entry; <c>false</c> to enable it.</param>
    /// <returns>A task that completes when the updated tree has been applied and every entry has settled.</returns>
    /// <exception cref="CordisException">No entry has the id <paramref name="id"/>.</exception>
    public async Task SetDisabledAsync(string id, bool disabled)
    {
        var entry = this.Find(id) ?? throw new CordisException($"unknown entry \"{id}\"");
        var tree = this.Snapshot().ToList();
        var options = entry.Options.Clone();
        options.Disabled = disabled;
        Patch(tree, id, options);
        await this.ApplyAsync(tree).ConfigureAwait(false);
        this.ConfigChanged?.Invoke();
    }

    /// <summary>
    /// Binds raw JSON to the component's config type, discovered from its static
    /// <c>Schema</c> property or from the second constructor parameter.
    /// </summary>
    /// <param name="componentType">The component type whose config type is bound.</param>
    /// <param name="json">The raw config, or <c>null</c> for none.</param>
    /// <returns>The bound config, or <c>null</c> if there is no JSON or no config type.</returns>
    internal static object? BindConfig(Type componentType, JsonElement? json)
    {
        if (json is null)
        {
            return null;
        }

        var configType = ConfigTypeOf(componentType);
        return configType is null ? null : json.Value.Deserialize(configType, JsonOptions);
    }

    internal static Type? ConfigTypeOf(Type componentType)
    {
        var property = componentType.GetProperty("Schema", BindingFlags.Public | BindingFlags.Static | BindingFlags.FlattenHierarchy);
        if (property?.GetValue(null) is IConfigSchema fromProperty)
        {
            return fromProperty.ConfigType;
        }

        var field = componentType.GetField("Schema", BindingFlags.Public | BindingFlags.Static | BindingFlags.FlattenHierarchy);
        if (field?.GetValue(null) is IConfigSchema fromField)
        {
            return fromField.ConfigType;
        }

        var ctor = componentType.GetConstructors()
            .FirstOrDefault(candidate => candidate.GetParameters() is { Length: 2 } parameters
                && parameters[0].ParameterType == typeof(Context));
        return ctor?.GetParameters()[1].ParameterType;
    }

    private static EntryOptions WithoutChildren(EntryOptions options)
    {
        var copy = options.Clone();
        copy.Children = null;
        return copy;
    }

    private static bool Patch(List<EntryOptions> entries, string id, EntryOptions replacement)
    {
        var segments = id.Split(Separator);
        var cursor = entries;
        for (var i = 0; i < segments.Length; i++)
        {
            var index = cursor.FindIndex(options => options.Id == segments[i]);
            if (index < 0)
            {
                return false;
            }

            if (i == segments.Length - 1)
            {
                replacement.Children = cursor[index].Children;
                cursor[index] = replacement;
                return true;
            }

            cursor = cursor[index].Children ?? [];
        }

        return false;
    }

    private static bool JsonEquals(JsonElement? left, JsonElement? right)
    {
        if (left is null || right is null)
        {
            return left is null && right is null;
        }

        return left.Value.GetRawText() == right.Value.GetRawText();
    }

    // ---------------------------------------------------------------- internals
    private void Flatten(
        List<EntryOptions> entries,
        string? parentId,
        Dictionary<string, (EntryOptions Options, string? ParentId, int Order)> sink,
        ref int order)
    {
        foreach (var options in entries)
        {
            if (string.IsNullOrEmpty(options.Id))
            {
                options.Id = Guid.NewGuid().ToString("n")[..8];
            }

            // An invalid entry is reported on its own: it and its subtree are skipped, so a live
            // entry with that id is disposed, and its siblings still load.
            string? issue = null;
            if (options.Id.Contains(Separator, StringComparison.Ordinal))
            {
                issue = $"entry id \"{options.Id}\" must not contain \"{Separator}\"";
            }
            else if (!options.Group && string.IsNullOrWhiteSpace(options.Name))
            {
                issue = $"entry \"{options.Id}\" requires a name";
            }

            var id = parentId is null ? options.Id : $"{parentId}{Separator}{options.Id}";
            if (issue is null && sink.ContainsKey(id))
            {
                issue = $"duplicate entry id \"{id}\"";
            }

            if (issue is not null)
            {
                this.Ctx.Logger.Error(new CordisException(issue));
                continue;
            }

            sink[id] = (options, parentId, order++);
            if (options.Children is { Count: > 0 })
            {
                this.Flatten(options.Children, id, sink, ref order);
            }
        }
    }

    /// <summary>
    /// Bring one entry in line with its options: create its fiber if it should run and has none,
    /// or push a config-only edit to the live fiber. Returns the pending update, if any.
    /// </summary>
    private Task? Ensure(Entry entry)
    {
        lock (this.entries)
        {
            var options = entry.Options;
            if (options.Disabled)
            {
                return null;
            }

            Context scope;
            if (entry.ParentId is null)
            {
                scope = this.Ctx;
            }
            else if (this.entries.GetValueOrDefault(entry.ParentId)?.Scope is { } parentScope)
            {
                scope = parentScope;
            }
            else
            {
                return null; // the enclosing group is disabled, pending or reloading
            }

            if (entry.Fiber is { Uid: not null } fiber)
            {
                if (JsonEquals(entry.AppliedConfig, options.Config))
                {
                    return null;
                }

                entry.AppliedConfig = options.Config;
                if (options.Group)
                {
                    return null;
                }

                return this.UpdateFiberAsync(fiber, BindConfig(this.Resolve(options.Name), options.Config));
            }

            foreach ((var name, var realm) in options.Isolate ?? [])
            {
                scope = scope.Isolate(name, this.RealmKey(name, realm, entry.Id));
            }

            Fiber created;
            if (options.Group)
            {
                created = scope.Apply($"group:{options.Id}", groupCtx => this.LoadGroup(entry, groupCtx), options.Inject ?? []);
            }
            else
            {
                var type = this.Resolve(options.Name);
                created = scope.Use(type, BindConfig(type, options.Config), options.Inject);
            }

            entry.Fiber = created;
            entry.AppliedConfig = options.Config;
            this.byFiber[created] = entry;
            return null;
        }
    }

    /// <summary>Body of a group entry: expose the scope and create the children on it.</summary>
    private void LoadGroup(Entry group, Context groupCtx)
    {
        Entry[] children;
        lock (this.entries)
        {
            group.Scope = groupCtx;
            children = [.. this.entries.Values.Where(entry => entry.ParentId == group.Id).OrderBy(entry => entry.Order)];
        }

        // registered first, so it runs last: after the children (effects of this fiber) are gone
        groupCtx.Effect(
            () => () =>
            {
                lock (this.entries)
                {
                    group.Scope = null;
                    foreach (var child in this.entries.Values.Where(entry => entry.ParentId == group.Id))
                    {
                        if (child.Fiber is not { Uid: null } disposed)
                        {
                            continue;
                        }

                        this.byFiber.Remove(disposed);
                        child.Fiber = null;
                    }
                }
            },
            "loader/group");

        foreach (var child in children)
        {
            try
            {
                this.Ensure(child);
            }
            catch (Exception error)
            {
                this.Ctx.Logger.Error(error);
            }
        }
    }

    private async Task UpdateFiberAsync(Fiber fiber, object? config)
    {
        try
        {
            await fiber.UpdateAsync(config, noSave: true).ConfigureAwait(false);
        }
        catch (Exception error) when (fiber.State != FiberState.Failed)
        {
            this.Ctx.Logger.Error(error); // e.g. rejected by the schema before anything changed
        }
        catch
        {
            // a failed load is reported by the fiber itself
        }
    }

    private async Task DisposeFiberAsync(Entry entry)
    {
        Fiber? fiber;
        lock (this.entries)
        {
            fiber = entry.Fiber;
            entry.Fiber = null;
            entry.Scope = null;
            if (fiber is not null)
            {
                this.byFiber.Remove(fiber);
            }
        }

        if (fiber is not null)
        {
            await fiber.DisposeAsync().ConfigureAwait(false);
        }
    }

    private async Task RemoveAsync(string id)
    {
        Entry? entry;
        lock (this.entries)
        {
            if (!this.entries.Remove(id, out entry))
            {
                return;
            }
        }

        await this.DisposeFiberAsync(entry).ConfigureAwait(false);
    }

    /// <summary>
    /// The realm of one service for one entry. A label is shared per service by every entry
    /// using it; <c>"true"</c> is private to the entry.
    /// </summary>
    private IsolateKey RealmKey(string service, string realm, string entryId)
    {
        var label = realm == "true" ? $"{service}#{entryId}" : $"{service}@{realm}";
        lock (this.realms)
        {
            if (!this.realms.TryGetValue(label, out var key))
            {
                this.realms[label] = key = new IsolateKey(label);
            }

            return key;
        }
    }

    private Type Resolve(string name)
        => this.Resolver.Resolve(name) ?? throw new CordisException($"cannot resolve component \"{name}\"");

    private void OnCommitting(Fiber fiber, object? config)
    {
        lock (this.entries)
        {
            if (!this.byFiber.TryGetValue(fiber, out var entry))
            {
                return;
            }

            var options = entry.Options.Clone();
            options.Config = config is null
                ? null
                : JsonSerializer.SerializeToElement(config, config.GetType(), JsonOptions);
            entry.Options = options;
            entry.AppliedConfig = options.Config;
            Patch(this.current, entry.Id, options.Clone());
        }

        this.ConfigChanged?.Invoke();
    }
}
