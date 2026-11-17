// -----------------------------------------------------------------------
// <copyright file="JsonConfigFile.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.Loading;

using System.ComponentModel.DataAnnotations;
using System.Text.Json;

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
    /// <summary>The config schema of the component: <see cref="Options"/> validated with data annotations.</summary>
    public static readonly IConfigSchema Schema = DataAnnotationsSchema<Options>.Instance;

    private const int WriteAttempts = 10;

    private readonly Context ctx;
    private readonly Options config;
    private readonly LoaderService loader;
    private readonly object sync = new();
    private Task drain = Task.CompletedTask;
    private bool running;
    private bool dirtyRead;
    private bool dirtyWrite;
    private bool disposed;
    private int tempCounter;

    /// <summary>The file text last read or written. Read and written only by the drain loop.</summary>
    private string? known;

    /// <summary>File text that could not be parsed; never written over, never merged.</summary>
    private string? unmergeable;

    /// <summary>The entry tree matching <see cref="known"/>, ids included.</summary>
    private List<EntryOptions> @base = [];

    private CancellationTokenSource? debounce;

    /// <summary>Initializes a new instance of the <see cref="JsonConfigFile"/> class.</summary>
    /// <param name="ctx">The context of the component's fiber; it must provide the <see cref="LoaderService"/>.</param>
    /// <param name="config">The file location and watch settings.</param>
    public JsonConfigFile(Context ctx, Options config)
    {
        this.ctx = ctx;
        this.config = config;
        this.loader = ctx.Get<LoaderService>();
        this.FullPath = System.IO.Path.GetFullPath(config.Path);
    }

    /// <summary>Gets the absolute path of the config file.</summary>
    public string FullPath { get; }

    /// <summary>
    /// Subscribes to runtime config changes, loads the file into the loader, and starts watching
    /// it when <see cref="Options.Watch"/> is set. A missing file is first written from
    /// <see cref="Options.Initial"/>.
    /// </summary>
    /// <returns>A task that completes when the initial tree has been applied and every entry has settled.</returns>
    /// <exception cref="CordisException">The file exists but does not contain a JSON array of entries.</exception>
    public async ValueTask InitAsync()
    {
        this.loader.ConfigChanged += this.OnConfigChanged;
        this.ctx.Effect(() => () => this.loader.ConfigChanged -= this.OnConfigChanged, "loader/config-changed");

        string text;
        List<EntryOptions> entries;
        if (await this.TryReadAsync().ConfigureAwait(false) is { } existing)
        {
            text = existing;
            entries = Parse(text) ?? throw new CordisException($"config file \"{this.FullPath}\" must contain a JSON array of entries");
        }
        else
        {
            entries = this.config.Initial?.Select(options => options.Clone()).ToList() ?? [];
            text = Serialize(entries);
            await this.WriteAtomicAsync(text, expected: null).ConfigureAwait(false);
        }

        AssignStableIds(entries, null, []);
        await this.loader.ApplyAsync(entries).ConfigureAwait(false);
        this.known = text;
        this.@base = Clone(entries);

        if (this.config.Watch)
        {
            this.StartWatching();
        }
    }

    /// <summary>
    /// Re-reads the file and merges it into the live tree. A missing or unparsable file is
    /// logged and leaves the live tree unchanged. Ignored after disposal.
    /// </summary>
    /// <returns>A task that completes when the drain loop has processed the request and any work queued behind it.</returns>
    public Task ReloadAsync() => this.Schedule(read: true, write: false);

    /// <summary>Writes the live tree back, after merging any edit made to the file meanwhile.</summary>
    /// <returns>A task that completes when the drain loop has processed the request and any work queued behind it.</returns>
    public Task SaveAsync() => this.Schedule(read: false, write: true);

    /// <summary>Stops watching, then flushes pending runtime changes to disk.</summary>
    /// <returns>A task that completes when the final flush has finished; a failed flush is logged, not thrown.</returns>
    public async ValueTask DisposeAsync()
    {
        var debounce = Interlocked.Exchange(ref this.debounce, null);
        debounce?.Cancel();
        debounce?.Dispose();

        Task flush;
        lock (this.sync)
        {
            this.disposed = true;
        }

        flush = this.Schedule(read: false, write: true);
        try
        {
            await flush.ConfigureAwait(false);
        }
        catch (Exception error)
        {
            this.ctx.Logger.Error(error);
        }
    }

    // ---------------------------------------------------------------- merging
    private static List<EntryOptions>? Parse(string text)
    {
        try
        {
            using var document = JsonDocument.Parse(text);
            if (document.RootElement.ValueKind != JsonValueKind.Array)
            {
                return null;
            }

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
        var copy = options.Clone();
        copy.Id = string.Empty;
        copy.Children = null;
        return JsonSerializer.Serialize(copy, LoaderService.JsonOptions);
    }

    /// <summary>
    /// Give each anonymous entry the id of an identical sibling in <paramref name="previous"/>,
    /// so an edit elsewhere in the file does not restart it; otherwise a fresh id.
    /// </summary>
    private static void AssignStableIds(List<EntryOptions> entries, string? parentId, IReadOnlyList<EntryOptions> previous)
    {
        var flat = Flatten(previous);
        var taken = new HashSet<string>(entries.Where(options => options.Id != string.Empty).Select(options => options.Id), StringComparer.Ordinal);
        foreach (var options in entries)
        {
            if (options.Id == string.Empty)
            {
                var shape = Shape(options);
                var match = flat.Values.FirstOrDefault(node =>
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

    private static Dictionary<string, Node> Flatten(IReadOnlyList<EntryOptions> entries)
    {
        var result = new Dictionary<string, Node>(StringComparer.Ordinal);
        Visit(entries, null);
        return result;

        void Visit(IReadOnlyList<EntryOptions> level, string? parentId)
        {
            foreach (var options in level)
            {
                var id = Compose(parentId, options.Id);
                result.TryAdd(id, new Node(options, parentId, Shape(options), result.Count));
                if (options.Children is { Count: > 0 } children)
                {
                    Visit(children, id);
                }
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
        var b = Flatten(@base);
        var o = Flatten(ours);
        var t = Flatten(theirs);

        var ids = t.OrderBy(pair => pair.Value.Order).Select(pair => pair.Key)
            .Concat(o.OrderBy(pair => pair.Value.Order).Select(pair => pair.Key).Where(id => !t.ContainsKey(id)))
            .ToList();

        var chosen = new Dictionary<string, Node>(StringComparer.Ordinal);
        foreach (var id in ids)
        {
            var baseShape = b.TryGetValue(id, out var bn) ? bn.Shape : null;
            var ourShape = o.TryGetValue(id, out var on) ? on.Shape : null;
            var theirShape = t.TryGetValue(id, out var tn) ? tn.Shape : null;

            Node? pick;
            if (ourShape == baseShape)
            {
                pick = theirShape is null ? null : tn;
            }
            else if (theirShape == baseShape || ourShape == theirShape)
            {
                pick = ourShape is null ? null : on;
            }
            else
            {
                conflicts.Add(id);
                pick = theirShape is null ? null : tn;
            }

            if (pick is { } node)
            {
                chosen[id] = node;
            }
        }

        var roots = new List<EntryOptions>();
        var built = new Dictionary<string, EntryOptions>(StringComparer.Ordinal);
        foreach (var id in ids)
        {
            if (!chosen.TryGetValue(id, out var node))
            {
                continue;
            }

            var copy = node.Options.Clone();
            copy.Children = null;
            if (node.ParentId is null)
            {
                roots.Add(copy);
            }
            else if (built.TryGetValue(node.ParentId, out var parent))
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

    // ------------------------------------------------------------- drain loop
    private Task Schedule(bool read, bool write)
    {
        lock (this.sync)
        {
            if (this.disposed && !write)
            {
                return this.drain;
            }

            this.dirtyRead |= read;
            this.dirtyWrite |= write;
            if (!this.running)
            {
                this.running = true;
                this.drain = Task.Run(this.DrainAsync);
            }

            return this.drain;
        }
    }

    private async Task DrainAsync()
    {
        while (true)
        {
            bool read;
            lock (this.sync)
            {
                if (!this.dirtyRead && !this.dirtyWrite)
                {
                    this.running = false;
                    return;
                }

                read = this.dirtyRead;
                this.dirtyRead = false;
                if (!read)
                {
                    this.dirtyWrite = false;
                }
            }

            try
            {
                if (read)
                {
                    await this.ReadStepAsync().ConfigureAwait(false);
                }
                else
                {
                    await this.WriteStepAsync().ConfigureAwait(false);
                }
            }
            catch (Exception error)
            {
                this.ctx.Logger.Error(error);
            }
        }
    }

    private async Task ReadStepAsync()
    {
        var text = await this.TryReadAsync().ConfigureAwait(false);
        if (text is null)
        {
            this.ctx.Logger.Warn($"config file \"{this.FullPath}\" is missing; keeping the current tree");
            return;
        }

        if (text == this.known)
        {
            return;
        }

        if (Parse(text) is not { } theirs)
        {
            if (text == this.unmergeable)
            {
                return;
            }

            this.unmergeable = text;
            this.ctx.Logger.Warn($"config file \"{this.FullPath}\" is not a JSON array of entries; keeping the current tree");
            return;
        }

        AssignStableIds(theirs, null, this.@base);
        var ours = this.loader.Snapshot();
        var conflicts = new List<string>();
        var merged = Merge(this.@base, ours, theirs, conflicts);
        foreach (var id in conflicts)
        {
            this.ctx.Logger.Error($"entry \"{id}\" was changed both in \"{this.FullPath}\" and at runtime; the file wins");
        }

        await this.loader.ApplyAsync(merged).ConfigureAwait(false);
        this.known = text;
        this.@base = Clone(theirs);

        // runtime edits survived the merge: write them out
        if (Serialize(merged) != Serialize(theirs))
        {
            lock (this.sync)
            {
                this.dirtyWrite = true;
            }
        }
    }

    private async Task WriteStepAsync()
    {
        var entries = this.loader.Snapshot();
        var text = Serialize(entries);
        if (text == this.known)
        {
            return;
        }

        var onDisk = await this.TryReadAsync().ConfigureAwait(false);
        if (onDisk is not null && onDisk != this.known)
        {
            // a file we cannot parse is left alone; the changes stay in memory until it is fixed
            if (onDisk == this.unmergeable)
            {
                return;
            }

            // someone else edited the file: merge their edit first, then write
            lock (this.sync)
            {
                this.dirtyRead = true;
                this.dirtyWrite = true;
            }

            return;
        }

        if (await this.WriteAtomicAsync(text, onDisk).ConfigureAwait(false))
        {
            this.known = text;
            this.@base = Clone(entries);
        }
    }

    // ------------------------------------------------------------ file access

    /// <summary>The file text, or <c>null</c> only if the file does not exist. Other errors propagate.</summary>
    private async Task<string?> TryReadAsync()
    {
        try
        {
            return await File.ReadAllTextAsync(this.FullPath).ConfigureAwait(false);
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
        var directory = System.IO.Path.GetDirectoryName(this.FullPath)!;
        var temp = System.IO.Path.Combine(
            directory,
            $".{System.IO.Path.GetFileName(this.FullPath)}.{Environment.ProcessId}.{Interlocked.Increment(ref this.tempCounter)}.tmp");
        try
        {
            await File.WriteAllTextAsync(temp, text).ConfigureAwait(false);
            for (var attempt = 1; ; attempt++)
            {
                if (await this.TryReadAsync().ConfigureAwait(false) != expected)
                {
                    lock (this.sync)
                    {
                        this.dirtyRead = true;
                        this.dirtyWrite = true;
                    }

                    return false;
                }

                try
                {
                    File.Move(temp, this.FullPath, overwrite: true);
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
            this.ctx.Logger.Warn($"cannot write config file \"{this.FullPath}\": {error.Message}; changes are kept in memory");
            return false;
        }
        finally
        {
            try
            {
                File.Delete(temp);
            }
            catch
            { /* best effort */
            }
        }
    }

    // --------------------------------------------------------------- watching
    private void StartWatching()
    {
        this.ctx.Effect(
            () =>
            {
                var directory = System.IO.Path.GetDirectoryName(this.FullPath);
                var watcher = new FileSystemWatcher(
                    string.IsNullOrEmpty(directory) ? "." : directory,
                    System.IO.Path.GetFileName(this.FullPath))
                {
                    NotifyFilter = NotifyFilters.LastWrite | NotifyFilters.Size | NotifyFilters.FileName,
                    EnableRaisingEvents = true,
                };
                watcher.Changed += this.OnFileEvent;
                watcher.Created += this.OnFileEvent;
                watcher.Renamed += this.OnFileEvent;

                // a buffer overflow drops events: re-read to be sure
                watcher.Error += (_, _) => _ = this.ReloadAsync();
                return watcher.Dispose;
            },
            "loader/watch");
    }

    private void OnFileEvent(object sender, FileSystemEventArgs args)
    {
        var current = new CancellationTokenSource();
        var previous = Interlocked.Exchange(ref this.debounce, current);
        previous?.Cancel();
        previous?.Dispose();
        var token = current.Token;

        _ = Task.Run(
            async () =>
            {
                try
                {
                    await Task.Delay(this.config.DebounceMs, token).ConfigureAwait(false);
                    await this.ReloadAsync().ConfigureAwait(false);
                }
                catch (OperationCanceledException)
                {
                }
                catch (Exception error)
                {
                    this.ctx.Logger.Error(error);
                }
            },
            CancellationToken.None);
    }

    private void OnConfigChanged() => _ = this.SaveAsync();

    private readonly record struct Node(EntryOptions Options, string? ParentId, string Shape, int Order);

    /// <summary>Config of a <see cref="JsonConfigFile"/> component.</summary>
    public sealed class Options
    {
        /// <summary>
        /// Gets or sets the path of the config file. Relative paths resolve against the process
        /// working directory. Defaults to <c>cordis.json</c>.
        /// </summary>
        [Required]
        public string Path { get; set; } = "cordis.json";

        /// <summary>Gets or sets a value indicating whether file changes are watched and merged into the live tree. Defaults to <c>true</c>.</summary>
        public bool Watch { get; set; } = true;

        /// <summary>Gets or sets the entries written out when the file does not exist at startup.</summary>
        public List<EntryOptions>? Initial { get; set; }

        /// <summary>Gets or sets the quiet period, in milliseconds, after a file event before the file is re-read. Defaults to 100.</summary>
        public int DebounceMs { get; set; } = 100;
    }
}
