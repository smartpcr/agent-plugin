using System.Reflection;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace Cordis.Loading;

/// <summary>Declarative description of one component instance in the config file.</summary>
public sealed class EntryOptions
{
    public string Id { get; set; } = "";

    /// <summary>Component identifier handed to the <see cref="IComponentResolver"/>.</summary>
    public string Name { get; set; } = "";

    public JsonElement? Config { get; set; }

    /// <summary>A group is a context node: it loads no component but scopes its children.</summary>
    public bool Group { get; set; }

    public bool Disabled { get; set; }

    public string[]? Inject { get; set; }

    /// <summary>
    /// Service name to realm label. Entries sharing a label share the realm for that service;
    /// <c>"true"</c> makes a realm private to this entry. Any other string, including the empty
    /// string, is an ordinary shared label.
    /// </summary>
    public Dictionary<string, string>? Isolate { get; set; }

    public List<EntryOptions>? Children { get; set; }

    public EntryOptions Clone() => new()
    {
        Id = Id,
        Name = Name,
        Config = Config,
        Group = Group,
        Disabled = Disabled,
        Inject = Inject?.ToArray(),
        Isolate = Isolate is null ? null : new Dictionary<string, string>(Isolate),
        Children = Children?.Select(child => child.Clone()).ToList(),
    };

    public bool SameShape(EntryOptions other)
        => Name == other.Name
        && Group == other.Group
        && Disabled == other.Disabled
        && (Inject ?? []).SequenceEqual(other.Inject ?? [])
        && IsolateEquals(Isolate, other.Isolate);

    private static bool IsolateEquals(Dictionary<string, string>? left, Dictionary<string, string>? right)
    {
        if (left is null || left.Count == 0) return right is null || right.Count == 0;
        if (right is null || left.Count != right.Count) return false;
        return left.All(pair => right.TryGetValue(pair.Key, out string? value) && value == pair.Value);
    }
}

public interface IComponentResolver
{
    Type? Resolve(string name);
}

/// <summary>Resolves assembly-qualified or short type names against the loaded assemblies.</summary>
public sealed class TypeComponentResolver : IComponentResolver
{
    private readonly List<Assembly> _assemblies = [];

    public TypeComponentResolver(params Assembly[] assemblies) => _assemblies.AddRange(assemblies);

    public IComponentResolver Add(Assembly assembly)
    {
        _assemblies.Add(assembly);
        return this;
    }

    public Type? Resolve(string name)
    {
        var type = Type.GetType(name, throwOnError: false);
        if (type is not null) return type;

        List<Assembly> candidates = _assemblies.Count > 0 ? _assemblies : [.. AppDomain.CurrentDomain.GetAssemblies()];
        foreach (Assembly assembly in candidates)
        {
            type = assembly.GetType(name, throwOnError: false);
            if (type is not null) return type;
            type = assembly.GetTypes().FirstOrDefault(candidate => candidate.Name == name);
            if (type is not null) return type;
        }
        return null;
    }
}

public sealed class Entry(string id, string? parentId)
{
    /// <summary>Composite id: the ids of the enclosing groups and this entry, joined by <c>:</c>.</summary>
    public string Id { get; } = id;

    /// <summary>Composite id of the enclosing group entry, or <c>null</c> at the top level.</summary>
    public string? ParentId { get; } = parentId;

    public EntryOptions Options { get; internal set; } = new() { Id = id };

    public Fiber? Fiber { get; internal set; }

    /// <summary>Context children of a group entry are created on; <c>null</c> unless the group is loaded.</summary>
    public Context? Scope { get; internal set; }

    public bool IsActive => Fiber is { State: FiberState.Active };

    internal int Order { get; set; }

    /// <summary>The config the live fiber was last given, to detect config-only edits.</summary>
    internal JsonElement? AppliedConfig { get; set; }

    public override string ToString() => $"Entry {Id} ({Options.Name})";
}

/// <summary>
/// Turns a declarative entry list into a live component tree and keeps them in sync.
/// Analogue of <c>@cordisjs/loader</c>.
/// </summary>
/// <remarks>
/// A group's children are created by the group's own body, on the group's context. They load
/// only while the group is loaded, and a group that is disabled, pending on its <c>Inject</c>,
/// or rebuilt takes its children down with it and recreates them when it loads again.
/// </remarks>
[ServiceName("loader")]
public sealed class LoaderService : Service
{
    public const char Separator = ':';

    public static readonly JsonSerializerOptions JsonOptions = new(JsonSerializerDefaults.Web)
    {
        PropertyNameCaseInsensitive = true,
        DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
        WriteIndented = true,
    };

    private readonly Dictionary<string, Entry> _entries = new(StringComparer.Ordinal);
    private readonly Dictionary<Fiber, Entry> _byFiber = [];
    private readonly Dictionary<string, IsolateKey> _realms = new(StringComparer.Ordinal);
    private readonly SemaphoreSlim _apply = new(1, 1);
    private List<EntryOptions> _current = [];

    public LoaderService(Context ctx) : base(ctx, "loader")
    {
        Resolver = new TypeComponentResolver();
        ctx.Internal.Committing += OnCommitting;
        ctx.Effect(() => () => ctx.Internal.Committing -= OnCommitting, "loader/committing");
    }

    public IComponentResolver Resolver { get; set; }

    /// <summary>Raised after a fiber commits a config change, so a file source can persist it.</summary>
    public event Action? ConfigChanged;

    public IReadOnlyCollection<Entry> Entries
    {
        get { lock (_entries) return [.. _entries.Values]; }
    }

    public Entry? Find(string id)
    {
        lock (_entries) return _entries.GetValueOrDefault(id);
    }

    /// <summary>Current entry tree, as it would be written back to disk.</summary>
    public IReadOnlyList<EntryOptions> Snapshot()
    {
        lock (_entries) return _current.Select(options => options.Clone()).ToList();
    }

    /// <summary>
    /// Diff the desired tree against the live one and reconcile. Applies are serialised. An
    /// invalid or failing entry is logged and skipped; it never stops its siblings.
    /// </summary>
    public async Task ApplyAsync(IReadOnlyList<EntryOptions> desired)
    {
        await _apply.WaitAsync().ConfigureAwait(false);
        try
        {
            var tree = desired.Select(options => options.Clone()).ToList();
            var flattened = new Dictionary<string, (EntryOptions Options, string? ParentId, int Order)>(StringComparer.Ordinal);
            int order = 0;
            Flatten(tree, null, flattened, ref order);

            string[] removed;
            lock (_entries) removed = [.. _entries.Keys.Where(id => !flattened.ContainsKey(id))];
            foreach (string id in removed) await RemoveAsync(id).ConfigureAwait(false);

            string[] sequence = flattened.OrderBy(pair => pair.Value.Order).Select(pair => pair.Key).ToArray();

            // 1. records: rebuild entries whose shape changed, so step 2 recreates them
            foreach (string? id in sequence)
            {
                (EntryOptions? options, string? parentId, int position) = flattened[id];
                Entry? entry;
                lock (_entries) entry = _entries.GetValueOrDefault(id);
                if (entry is null)
                {
                    entry = new Entry(id, parentId);
                    lock (_entries) _entries[id] = entry;
                }
                else if (!entry.Options.SameShape(options))
                {
                    await DisposeFiberAsync(entry).ConfigureAwait(false);
                }
                lock (_entries)
                {
                    entry.Options = WithoutChildren(options);
                    entry.Order = position;
                }
            }

            // 2. materialise, parents first; children of unloaded groups wait for their group
            var updates = new List<Task>();
            foreach (string? id in sequence)
            {
                Entry? entry = Find(id);
                if (entry is null) continue;
                try
                {
                    if (Ensure(entry) is { } update) updates.Add(update);
                }
                catch (Exception error)
                {
                    Ctx.Logger.Error(error);
                }
            }
            await Task.WhenAll(updates).ConfigureAwait(false);

            lock (_entries) _current = tree;
        }
        finally
        {
            _apply.Release();
        }

        await WaitAllAsync().ConfigureAwait(false);
    }

    /// <summary>Wait until every entry's fiber settles. Failures are reported by the fibers themselves.</summary>
    public async Task WaitAllAsync()
    {
        foreach (Entry entry in Entries)
        {
            if (entry.Fiber is null) continue;
            try { await entry.Fiber.WaitAsync().ConfigureAwait(false); }
            catch { /* reported by the fiber */ }
        }
    }

    public async Task SetDisabledAsync(string id, bool disabled)
    {
        Entry entry = Find(id) ?? throw new CordisException($"unknown entry \"{id}\"");
        var tree = Snapshot().ToList();
        EntryOptions options = entry.Options.Clone();
        options.Disabled = disabled;
        Patch(tree, id, options);
        await ApplyAsync(tree).ConfigureAwait(false);
        ConfigChanged?.Invoke();
    }

    // ---------------------------------------------------------------- internals

    private void Flatten(
        List<EntryOptions> entries,
        string? parentId,
        Dictionary<string, (EntryOptions, string?, int)> sink,
        ref int order)
    {
        foreach (EntryOptions options in entries)
        {
            if (string.IsNullOrEmpty(options.Id)) options.Id = Guid.NewGuid().ToString("n")[..8];

            // An invalid entry is reported on its own: it and its subtree are skipped, so a live
            // entry with that id is disposed, and its siblings still load.
            string? issue = null;
            if (options.Id.Contains(Separator)) issue = $"entry id \"{options.Id}\" must not contain \"{Separator}\"";
            else if (!options.Group && string.IsNullOrWhiteSpace(options.Name)) issue = $"entry \"{options.Id}\" requires a name";

            string id = parentId is null ? options.Id : $"{parentId}{Separator}{options.Id}";
            if (issue is null && sink.ContainsKey(id)) issue = $"duplicate entry id \"{id}\"";
            if (issue is not null)
            {
                Ctx.Logger.Error(new CordisException(issue));
                continue;
            }

            sink[id] = (options, parentId, order++);
            if (options.Children is { Count: > 0 }) Flatten(options.Children, id, sink, ref order);
        }
    }

    private static EntryOptions WithoutChildren(EntryOptions options)
    {
        EntryOptions copy = options.Clone();
        copy.Children = null;
        return copy;
    }

    private static bool Patch(List<EntryOptions> entries, string id, EntryOptions replacement)
    {
        string[] segments = id.Split(Separator);
        List<EntryOptions> cursor = entries;
        for (int i = 0; i < segments.Length; i++)
        {
            int index = cursor.FindIndex(options => options.Id == segments[i]);
            if (index < 0) return false;
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

    /// <summary>
    /// Bring one entry in line with its options: create its fiber if it should run and has none,
    /// or push a config-only edit to the live fiber. Returns the pending update, if any.
    /// </summary>
    private Task? Ensure(Entry entry)
    {
        lock (_entries)
        {
            EntryOptions options = entry.Options;
            if (options.Disabled) return null;

            Context scope;
            if (entry.ParentId is null)
            {
                scope = Ctx;
            }
            else if (_entries.GetValueOrDefault(entry.ParentId)?.Scope is { } parentScope)
            {
                scope = parentScope;
            }
            else
            {
                return null; // the enclosing group is disabled, pending or reloading
            }

            if (entry.Fiber is { Uid: not null } fiber)
            {
                if (JsonEquals(entry.AppliedConfig, options.Config)) return null;
                entry.AppliedConfig = options.Config;
                if (options.Group) return null;
                return UpdateFiberAsync(fiber, BindConfig(Resolve(options.Name), options.Config));
            }

            foreach ((string? name, string? realm) in options.Isolate ?? [])
            {
                scope = scope.Isolate(name, RealmKey(name, realm, entry.Id));
            }

            Fiber created;
            if (options.Group)
            {
                created = scope.Apply($"group:{options.Id}", groupCtx => LoadGroup(entry, groupCtx), options.Inject ?? []);
            }
            else
            {
                Type type = Resolve(options.Name);
                created = scope.Use(type, BindConfig(type, options.Config), options.Inject);
            }

            entry.Fiber = created;
            entry.AppliedConfig = options.Config;
            _byFiber[created] = entry;
            return null;
        }
    }

    /// <summary>Body of a group entry: expose the scope and create the children on it.</summary>
    private void LoadGroup(Entry group, Context groupCtx)
    {
        Entry[] children;
        lock (_entries)
        {
            group.Scope = groupCtx;
            children = [.. _entries.Values.Where(entry => entry.ParentId == group.Id).OrderBy(entry => entry.Order)];
        }

        // registered first, so it runs last: after the children (effects of this fiber) are gone
        groupCtx.Effect(() => () =>
        {
            lock (_entries)
            {
                group.Scope = null;
                foreach (Entry? child in _entries.Values.Where(entry => entry.ParentId == group.Id))
                {
                    if (child.Fiber is not { Uid: null } disposed) continue;
                    _byFiber.Remove(disposed);
                    child.Fiber = null;
                }
            }
        }, "loader/group");

        foreach (Entry child in children)
        {
            try { Ensure(child); }
            catch (Exception error) { Ctx.Logger.Error(error); }
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
            Ctx.Logger.Error(error); // e.g. rejected by the schema before anything changed
        }
        catch
        {
            // a failed load is reported by the fiber itself
        }
    }

    private async Task DisposeFiberAsync(Entry entry)
    {
        Fiber? fiber;
        lock (_entries)
        {
            fiber = entry.Fiber;
            entry.Fiber = null;
            entry.Scope = null;
            if (fiber is not null) _byFiber.Remove(fiber);
        }
        if (fiber is not null) await fiber.DisposeAsync().ConfigureAwait(false);
    }

    private async Task RemoveAsync(string id)
    {
        Entry? entry;
        lock (_entries)
        {
            if (!_entries.Remove(id, out entry)) return;
        }
        await DisposeFiberAsync(entry).ConfigureAwait(false);
    }

    /// <summary>
    /// The realm of one service for one entry. A label is shared per service by every entry
    /// using it; <c>"true"</c> is private to the entry.
    /// </summary>
    private IsolateKey RealmKey(string service, string realm, string entryId)
    {
        string label = realm == "true" ? $"{service}#{entryId}" : $"{service}@{realm}";
        lock (_realms)
        {
            if (!_realms.TryGetValue(label, out IsolateKey? key)) _realms[label] = key = new IsolateKey(label);
            return key;
        }
    }

    private Type Resolve(string name)
        => Resolver.Resolve(name) ?? throw new CordisException($"cannot resolve component \"{name}\"");

    private void OnCommitting(Fiber fiber, object? config)
    {
        lock (_entries)
        {
            if (!_byFiber.TryGetValue(fiber, out Entry? entry)) return;
            EntryOptions options = entry.Options.Clone();
            options.Config = config is null
                ? null
                : JsonSerializer.SerializeToElement(config, config.GetType(), JsonOptions);
            entry.Options = options;
            entry.AppliedConfig = options.Config;
            Patch(_current, entry.Id, options.Clone());
        }
        ConfigChanged?.Invoke();
    }

    private static bool JsonEquals(JsonElement? left, JsonElement? right)
    {
        if (left is null || right is null) return left is null && right is null;
        return left.Value.GetRawText() == right.Value.GetRawText();
    }

    /// <summary>
    /// Binds raw JSON to the component's config type, discovered from its static
    /// <c>Schema</c> property or from the second constructor parameter.
    /// </summary>
    internal static object? BindConfig(Type componentType, JsonElement? json)
    {
        if (json is null) return null;
        Type? configType = ConfigTypeOf(componentType);
        return configType is null ? null : json.Value.Deserialize(configType, JsonOptions);
    }

    internal static Type? ConfigTypeOf(Type componentType)
    {
        PropertyInfo? property = componentType.GetProperty("Schema", BindingFlags.Public | BindingFlags.Static | BindingFlags.FlattenHierarchy);
        if (property?.GetValue(null) is IConfigSchema fromProperty) return fromProperty.ConfigType;

        FieldInfo? field = componentType.GetField("Schema", BindingFlags.Public | BindingFlags.Static | BindingFlags.FlattenHierarchy);
        if (field?.GetValue(null) is IConfigSchema fromField) return fromField.ConfigType;

        ConstructorInfo? ctor = componentType.GetConstructors()
            .FirstOrDefault(candidate => candidate.GetParameters() is { Length: 2 } parameters
                && parameters[0].ParameterType == typeof(Context));
        return ctor?.GetParameters()[1].ParameterType;
    }
}
