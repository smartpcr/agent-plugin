using System.ComponentModel.DataAnnotations;
using System.Text.Json;

namespace Cordis.Loading;

/// <summary>
/// Backs a <see cref="LoaderService"/> with a JSON file: reads it, watches it, and writes
/// config changes back. Analogue of <c>@cordisjs/include</c>.
/// </summary>
/// <remarks>
/// <para>
/// Every read and write runs on one serial drain loop, so a file edit and a runtime config
/// change can never interleave. The component remembers the file content it last read or
/// wrote (the <em>base</em>). Runtime edits are the live tree compared with that base, and
/// file edits are the file compared with it; a reload merges the two per entry. When both
/// sides changed the same entry differently, the file wins and the conflict is logged.
/// </para>
/// <para>
/// A write first checks that the file still holds the base. If someone else changed it, the
/// write is deferred until their edit has been merged. Writes go to a temporary file that is
/// renamed over the config, retried while another process holds it open.
/// </para>
/// <para>
/// A missing file is only seeded from <see cref="Options.Initial"/> at startup. If the file
/// disappears or becomes unreadable later, the live tree is kept and nothing is written.
/// </para>
/// </remarks>
[Inject("loader")]
public sealed class JsonConfigFile : IAsyncInit, IAsyncDisposable
{
    public sealed class Options
    {
        /// <summary>Relative paths resolve against the process working directory.</summary>
        [Required]
        public string Path { get; set; } = "cordis.json";

        public bool Watch { get; set; } = true;

        /// <summary>Written out when the file does not exist at startup.</summary>
        public List<EntryOptions>? Initial { get; set; }

        public int DebounceMs { get; set; } = 100;
    }

    private const int WriteAttempts = 10;

    public static readonly IConfigSchema Schema = DataAnnotationsSchema<Options>.Instance;

    private readonly Context _ctx;
    private readonly Options _config;
    private readonly LoaderService _loader;
    private readonly object _sync = new();
    private Task _drain = Task.CompletedTask;
    private bool _running;
    private bool _dirtyRead;
    private bool _dirtyWrite;
    private bool _disposed;
    private int _tempCounter;

    /// <summary>The file text last read or written. Read and written only by the drain loop.</summary>
    private string? _known;

    /// <summary>File text that could not be parsed; never written over, never merged.</summary>
    private string? _unmergeable;

    /// <summary>The entry tree matching <see cref="_known"/>, ids included.</summary>
    private List<EntryOptions> _base = [];

    private CancellationTokenSource? _debounce;

    public JsonConfigFile(Context ctx, Options config)
    {
        _ctx = ctx;
        _config = config;
        _loader = ctx.Get<LoaderService>();
        FullPath = System.IO.Path.GetFullPath(config.Path);
    }

    public string FullPath { get; }

    public async ValueTask InitAsync()
    {
        _loader.ConfigChanged += OnConfigChanged;
        _ctx.Effect(() => () => _loader.ConfigChanged -= OnConfigChanged, "loader/config-changed");

        string text;
        List<EntryOptions> entries;
        if (await TryReadAsync().ConfigureAwait(false) is { } existing)
        {
            text = existing;
            entries = Parse(text) ?? throw new CordisException($"config file \"{FullPath}\" must contain a JSON array of entries");
        }
        else
        {
            entries = _config.Initial?.Select(options => options.Clone()).ToList() ?? [];
            text = Serialize(entries);
            await WriteAtomicAsync(text, expected: null).ConfigureAwait(false);
        }

        AssignStableIds(entries, null, []);
        await _loader.ApplyAsync(entries).ConfigureAwait(false);
        _known = text;
        _base = Clone(entries);

        if (_config.Watch) StartWatching();
    }

    /// <summary>Re-read the file and merge it into the live tree.</summary>
    public Task ReloadAsync() => Schedule(read: true, write: false);

    /// <summary>Write the live tree back, after merging any edit made to the file meanwhile.</summary>
    public Task SaveAsync() => Schedule(read: false, write: true);

    // ------------------------------------------------------------- drain loop

    private Task Schedule(bool read, bool write)
    {
        lock (_sync)
        {
            if (_disposed && !write) return _drain;
            _dirtyRead |= read;
            _dirtyWrite |= write;
            if (!_running)
            {
                _running = true;
                _drain = Task.Run(DrainAsync);
            }
            return _drain;
        }
    }

    private async Task DrainAsync()
    {
        while (true)
        {
            bool read;
            lock (_sync)
            {
                if (!_dirtyRead && !_dirtyWrite)
                {
                    _running = false;
                    return;
                }
                read = _dirtyRead;
                _dirtyRead = false;
                if (!read) _dirtyWrite = false;
            }

            try
            {
                if (read) await ReadStepAsync().ConfigureAwait(false);
                else await WriteStepAsync().ConfigureAwait(false);
            }
            catch (Exception error)
            {
                _ctx.Logger.Error(error);
            }
        }
    }

    private async Task ReadStepAsync()
    {
        string? text = await TryReadAsync().ConfigureAwait(false);
        if (text is null)
        {
            _ctx.Logger.Warn($"config file \"{FullPath}\" is missing; keeping the current tree");
            return;
        }
        if (text == _known) return;

        if (Parse(text) is not { } theirs)
        {
            if (text == _unmergeable) return;
            _unmergeable = text;
            _ctx.Logger.Warn($"config file \"{FullPath}\" is not a JSON array of entries; keeping the current tree");
            return;
        }

        AssignStableIds(theirs, null, _base);
        IReadOnlyList<EntryOptions> ours = _loader.Snapshot();
        var conflicts = new List<string>();
        List<EntryOptions> merged = Merge(_base, ours, theirs, conflicts);
        foreach (string id in conflicts)
        {
            _ctx.Logger.Error($"entry \"{id}\" was changed both in \"{FullPath}\" and at runtime; the file wins");
        }

        await _loader.ApplyAsync(merged).ConfigureAwait(false);
        _known = text;
        _base = Clone(theirs);

        // runtime edits survived the merge: write them out
        if (Serialize(merged) != Serialize(theirs))
        {
            lock (_sync) _dirtyWrite = true;
        }
    }

    private async Task WriteStepAsync()
    {
        IReadOnlyList<EntryOptions> entries = _loader.Snapshot();
        string text = Serialize(entries);
        if (text == _known) return;

        string? onDisk = await TryReadAsync().ConfigureAwait(false);
        if (onDisk is not null && onDisk != _known)
        {
            // a file we cannot parse is left alone; the changes stay in memory until it is fixed
            if (onDisk == _unmergeable) return;

            // someone else edited the file: merge their edit first, then write
            lock (_sync)
            {
                _dirtyRead = true;
                _dirtyWrite = true;
            }
            return;
        }

        if (await WriteAtomicAsync(text, onDisk).ConfigureAwait(false))
        {
            _known = text;
            _base = Clone(entries);
        }
    }

    // ------------------------------------------------------------ file access

    /// <summary>The file text, or <c>null</c> only if the file does not exist. Other errors propagate.</summary>
    private async Task<string?> TryReadAsync()
    {
        try
        {
            return await File.ReadAllTextAsync(FullPath).ConfigureAwait(false);
        }
        catch (Exception error) when (error is FileNotFoundException or DirectoryNotFoundException)
        {
            return null;
        }
    }

    /// <summary>
    /// Write through a temporary file renamed over the target, so readers never see a partial
    /// file. Retries while the target is locked; gives up if its content stops matching
    /// <paramref name="expected"/>. Returns whether the write landed.
    /// </summary>
    private async Task<bool> WriteAtomicAsync(string text, string? expected)
    {
        string directory = System.IO.Path.GetDirectoryName(FullPath)!;
        string temp = System.IO.Path.Combine(
            directory,
            $".{System.IO.Path.GetFileName(FullPath)}.{Environment.ProcessId}.{Interlocked.Increment(ref _tempCounter)}.tmp");
        try
        {
            await File.WriteAllTextAsync(temp, text).ConfigureAwait(false);
            for (int attempt = 1; ; attempt++)
            {
                if (await TryReadAsync().ConfigureAwait(false) != expected)
                {
                    lock (_sync)
                    {
                        _dirtyRead = true;
                        _dirtyWrite = true;
                    }
                    return false;
                }
                try
                {
                    File.Move(temp, FullPath, overwrite: true);
                    return true;
                }
                catch (Exception error) when (error is IOException or UnauthorizedAccessException && attempt < WriteAttempts)
                {
                    await Task.Delay(20 * attempt).ConfigureAwait(false);
                }
            }
        }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException)
        {
            _ctx.Logger.Warn($"cannot write config file \"{FullPath}\": {error.Message}; changes are kept in memory");
            return false;
        }
        finally
        {
            try { File.Delete(temp); }
            catch { /* best effort */ }
        }
    }

    // ---------------------------------------------------------------- merging

    private static List<EntryOptions>? Parse(string text)
    {
        try
        {
            using var document = JsonDocument.Parse(text);
            if (document.RootElement.ValueKind != JsonValueKind.Array) return null;
            return document.RootElement.Deserialize<List<EntryOptions>>(LoaderService.JsonOptions) ?? [];
        }
        catch (JsonException)
        {
            return null;
        }
    }

    private static string Serialize(IReadOnlyList<EntryOptions> entries)
        => JsonSerializer.Serialize(entries, LoaderService.JsonOptions);

    private static List<EntryOptions> Clone(IReadOnlyList<EntryOptions> entries)
        => entries.Select(options => options.Clone()).ToList();

    /// <summary>The entry's own fields as JSON, without id and children.</summary>
    private static string Shape(EntryOptions options)
    {
        EntryOptions copy = options.Clone();
        copy.Id = "";
        copy.Children = null;
        return JsonSerializer.Serialize(copy, LoaderService.JsonOptions);
    }

    /// <summary>
    /// Give each anonymous entry the id of an identical sibling in <paramref name="previous"/>,
    /// so an edit elsewhere in the file does not restart it; otherwise a fresh id.
    /// </summary>
    private static void AssignStableIds(List<EntryOptions> entries, string? parentId, IReadOnlyList<EntryOptions> previous)
    {
        Dictionary<string, Node> flat = Flatten(previous);
        var taken = new HashSet<string>(entries.Where(options => options.Id != "").Select(options => options.Id), StringComparer.Ordinal);
        foreach (EntryOptions options in entries)
        {
            if (options.Id == "")
            {
                string shape = Shape(options);
                Node match = flat.Values.FirstOrDefault(node =>
                    node.ParentId == parentId && !taken.Contains(node.Options.Id) && node.Shape == shape);
                options.Id = match.Options?.Id ?? Guid.NewGuid().ToString("n")[..8];
                taken.Add(options.Id);
            }
            if (options.Children is { Count: > 0 } children)
            {
                AssignStableIds(children, Compose(parentId, options.Id), previous);
            }
        }
    }

    private static string Compose(string? parentId, string id)
        => parentId is null ? id : $"{parentId}{LoaderService.Separator}{id}";

    private readonly record struct Node(EntryOptions Options, string? ParentId, string Shape, int Order);

    private static Dictionary<string, Node> Flatten(IReadOnlyList<EntryOptions> entries)
    {
        var result = new Dictionary<string, Node>(StringComparer.Ordinal);
        Visit(entries, null);
        return result;

        void Visit(IReadOnlyList<EntryOptions> level, string? parentId)
        {
            foreach (EntryOptions options in level)
            {
                string id = Compose(parentId, options.Id);
                result.TryAdd(id, new Node(options, parentId, Shape(options), result.Count));
                if (options.Children is { Count: > 0 } children) Visit(children, id);
            }
        }
    }

    /// <summary>
    /// Three-way merge per entry (by composite id). Each side keeps its own edits; when both
    /// sides changed an entry differently, the file (<paramref name="theirs"/>) wins.
    /// </summary>
    private static List<EntryOptions> Merge(
        IReadOnlyList<EntryOptions> @base,
        IReadOnlyList<EntryOptions> ours,
        IReadOnlyList<EntryOptions> theirs,
        List<string> conflicts)
    {
        Dictionary<string, Node> b = Flatten(@base);
        Dictionary<string, Node> o = Flatten(ours);
        Dictionary<string, Node> t = Flatten(theirs);

        var ids = t.OrderBy(pair => pair.Value.Order).Select(pair => pair.Key)
            .Concat(o.OrderBy(pair => pair.Value.Order).Select(pair => pair.Key).Where(id => !t.ContainsKey(id)))
            .ToList();

        var chosen = new Dictionary<string, Node>(StringComparer.Ordinal);
        foreach (string? id in ids)
        {
            string? baseShape = b.TryGetValue(id, out Node bn) ? bn.Shape : null;
            string? ourShape = o.TryGetValue(id, out Node on) ? on.Shape : null;
            string? theirShape = t.TryGetValue(id, out Node tn) ? tn.Shape : null;

            Node? pick;
            if (ourShape == baseShape) pick = theirShape is null ? null : tn;
            else if (theirShape == baseShape || ourShape == theirShape) pick = ourShape is null ? null : on;
            else
            {
                conflicts.Add(id);
                pick = theirShape is null ? null : tn;
            }
            if (pick is { } node) chosen[id] = node;
        }

        var roots = new List<EntryOptions>();
        var built = new Dictionary<string, EntryOptions>(StringComparer.Ordinal);
        foreach (string? id in ids)
        {
            if (!chosen.TryGetValue(id, out Node node)) continue;
            EntryOptions copy = node.Options.Clone();
            copy.Children = null;
            if (node.ParentId is null)
            {
                roots.Add(copy);
            }
            else if (built.TryGetValue(node.ParentId, out EntryOptions? parent))
            {
                (parent.Children ??= []).Add(copy);
            }
            else
            {
                continue; // the enclosing group was removed
            }
            built[id] = copy;
        }
        return roots;
    }

    // --------------------------------------------------------------- watching

    private void StartWatching()
    {
        _ctx.Effect(() =>
        {
            string? directory = System.IO.Path.GetDirectoryName(FullPath);
            var watcher = new FileSystemWatcher(
                string.IsNullOrEmpty(directory) ? "." : directory,
                System.IO.Path.GetFileName(FullPath))
            {
                NotifyFilter = NotifyFilters.LastWrite | NotifyFilters.Size | NotifyFilters.FileName,
                EnableRaisingEvents = true,
            };
            watcher.Changed += OnFileEvent;
            watcher.Created += OnFileEvent;
            watcher.Renamed += OnFileEvent;
            // a buffer overflow drops events: re-read to be sure
            watcher.Error += (_, _) => _ = ReloadAsync();
            return watcher.Dispose;
        }, "loader/watch");
    }

    private void OnFileEvent(object sender, FileSystemEventArgs args)
    {
        var current = new CancellationTokenSource();
        CancellationTokenSource? previous = Interlocked.Exchange(ref _debounce, current);
        previous?.Cancel();
        previous?.Dispose();
        CancellationToken token = current.Token;

        _ = Task.Run(async () =>
        {
            try
            {
                await Task.Delay(_config.DebounceMs, token).ConfigureAwait(false);
                await ReloadAsync().ConfigureAwait(false);
            }
            catch (OperationCanceledException) { }
            catch (Exception error) { _ctx.Logger.Error(error); }
        }, CancellationToken.None);
    }

    private void OnConfigChanged() => _ = SaveAsync();

    /// <summary>Stops watching, then flushes pending runtime changes to disk.</summary>
    public async ValueTask DisposeAsync()
    {
        CancellationTokenSource? debounce = Interlocked.Exchange(ref _debounce, null);
        debounce?.Cancel();
        debounce?.Dispose();

        Task flush;
        lock (_sync) _disposed = true;
        flush = Schedule(read: false, write: true);
        try { await flush.ConfigureAwait(false); }
        catch (Exception error) { _ctx.Logger.Error(error); }
    }
}
