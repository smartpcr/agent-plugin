// -----------------------------------------------------------------------
// <copyright file="ProbeHost.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.E2E;

using System.Collections.Concurrent;
using Cordis.Loading;
using Cordis.Timers;

/// <summary>
/// One cordis tree for one case, and everything the actions need to drive and observe it:
/// the ordered trace, named gates, fibers by id, realm keys, event keys, the probe
/// implementations, and the loader and config file when a case uses them.
/// </summary>
internal sealed class ProbeHost
{
    private static readonly ConcurrentDictionary<Context, ProbeHost> ByRoot = new(ReferenceEqualityComparer.Instance);

    private readonly object sync = new();
    private readonly Dictionary<string, Fiber> fibers = new(StringComparer.Ordinal);
    private readonly HashSet<Fiber> allFibers = [];
    private readonly Dictionary<string, int> loads = new(StringComparer.Ordinal);
    private readonly ConcurrentDictionary<string, TaskCompletionSource> gates = new(StringComparer.Ordinal);
    private readonly ConcurrentDictionary<string, IsolateKey> realms = new(StringComparer.Ordinal);
    private readonly ConcurrentDictionary<string, ProbeImplementation> implementations = new(StringComparer.Ordinal);
    private readonly ConcurrentDictionary<string, object> eventKeys = new(StringComparer.Ordinal);
    private readonly ConcurrentDictionary<string, EffectHandle> services = new(StringComparer.Ordinal);
    private int disposed;

    private ProbeHost(Context root)
    {
        this.Root = root;
        root.Internal.PluginChanged += fiber =>
        {
            if (fiber.Uid is not null)
            {
                lock (this.sync)
                {
                    this.allFibers.Add(fiber);
                }
            }
        };
    }

    public Context Root { get; }

    public TraceLog Trace { get; } = new();

    public LoaderService? Loader { get; private set; }

    public JsonConfigFile? ConfigFile { get; set; }

    public string? ConfigPath { get; set; }

    public static ProbeHost For(Context ctx)
        => ByRoot.TryGetValue(ctx.Root, out var host) ? host : throw new InvalidOperationException("no probe host for this tree");

    public static async Task<ProbeHost> StartAsync(bool loader, bool timer)
    {
        var root = new Context();
        root.Loggers.ClearExporters();
        root.Loggers.AddExporter(new MemoryExporter());
        var host = new ProbeHost(root);
        ByRoot[root] = host;
        host.Register("root", root.Fiber);

        if (timer)
        {
            // A root service: reachable from every fiber, scoped to the caller's fiber per access.
            _ = new TimerService(root);
        }

        if (loader)
        {
            await root.Use<LoaderService>();
            host.Loader = root.Get<LoaderService>();
            host.Loader.Resolver = new ProbeResolver();
        }

        return host;
    }

    public async Task DisposeAsync()
    {
        if (Interlocked.Exchange(ref this.disposed, 1) == 1)
        {
            return;
        }

        await this.Root.DisposeAsync();
        ByRoot.TryRemove(this.Root, out _);
        if (this.ConfigPath is { } path && Path.GetDirectoryName(path) is { } directory)
        {
            try
            {
                Directory.Delete(directory, recursive: true);
            }
            catch (IOException)
            {
                // best effort
            }
        }
    }

    // ------------------------------------------------------------- fibers
    public void Register(string id, Fiber fiber)
    {
        lock (this.sync)
        {
            this.fibers[id] = fiber;
            this.allFibers.Add(fiber);
        }
    }

    /// <summary>A fiber by the id <c>component.use</c> gave it, or a loader entry's composite id.</summary>
    public Fiber Fiber(string id)
    {
        lock (this.sync)
        {
            if (this.fibers.TryGetValue(id, out var fiber))
            {
                return fiber;
            }
        }

        return this.Loader?.Find(id)?.Fiber ?? throw new InvalidOperationException($"no fiber has the id \"{id}\"");
    }

    /// <summary>The id of a fiber the loader created, by its entry.</summary>
    public string IdOf(Fiber fiber)
    {
        lock (this.sync)
        {
            foreach (var (id, known) in this.fibers)
            {
                if (ReferenceEquals(known, fiber))
                {
                    return id;
                }
            }
        }

        return this.Loader?.Entries.FirstOrDefault(entry => ReferenceEquals(entry.Fiber, fiber))?.Id
            ?? throw new InvalidOperationException("a probe fiber has no id");
    }

    public void CountLoad(string id)
    {
        lock (this.sync)
        {
            this.loads[id] = this.Loads(id) + 1;
        }
    }

    public int Loads(string id)
    {
        lock (this.sync)
        {
            return this.loads.GetValueOrDefault(id);
        }
    }

    /// <summary>Waits until no fiber in the tree has a load or unload running, twice in a row.</summary>
    public async Task SettleAsync(CancellationToken cancellation)
    {
        var quiet = 0;
        while (quiet < 2)
        {
            Fiber[] snapshot;
            lock (this.sync)
            {
                snapshot = [.. this.allFibers];
            }

            var busy = snapshot.Where(fiber => fiber.State is FiberState.Loading or FiberState.Unloading).ToArray();
            if (busy.Length == 0)
            {
                quiet++;
                await Task.Delay(10, cancellation);
                continue;
            }

            quiet = 0;
            foreach (var fiber in busy)
            {
                try
                {
                    await fiber.WaitAsync().WaitAsync(cancellation);
                }
                catch (OperationCanceledException)
                {
                    throw;
                }
                catch (Exception)
                {
                    // a failed load is a settled fiber
                }
            }
        }
    }

    // ------------------------------------------------------------- shared keys
    public Task Gate(string name)
        => this.gates.GetOrAdd(name, _ => new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously)).Task;

    public void OpenGate(string name)
        => this.gates.GetOrAdd(name, _ => new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously)).TrySetResult();

    /// <summary>One realm key per service and label, shared by every scope that names it.</summary>
    public IsolateKey Realm(string service, string label)
        => this.realms.GetOrAdd($"{service}@{label}", key => new IsolateKey(key));

    /// <summary>A context below <paramref name="ctx"/> with each listed service isolated in its labelled realm.</summary>
    public Context Isolated(Context ctx, IReadOnlyDictionary<string, string>? isolate)
    {
        foreach (var (service, label) in isolate ?? new Dictionary<string, string>())
        {
            ctx = ctx.Isolate(service, this.Realm(service, label));
        }

        return ctx;
    }

    /// <summary>The probe implementation called <paramref name="name"/>; <paramref name="inject"/> applies when it is first made.</summary>
    public ProbeImplementation Implementation(string name, IReadOnlyList<string>? inject)
        => this.implementations.GetOrAdd(name, key => new ProbeImplementation(key, inject ?? []));

    /// <summary>Makes a new implementation under <paramref name="name"/>, replacing a cached one.</summary>
    public ProbeImplementation NewImplementation(string name, IReadOnlyList<string> inject)
        => this.implementations[name] = new ProbeImplementation(name, inject);

    public TKey EventKey<TKey>(string name, string mode, Func<TKey> create)
        where TKey : class
        => (TKey)this.eventKeys.GetOrAdd($"{mode}:{name}", _ => create());

    public void KeepService(string name, EffectHandle handle) => this.services[name] = handle;

    public EffectHandle TakeService(string name)
        => this.services.TryRemove(name, out var handle) ? handle : throw new InvalidOperationException($"no service \"{name}\" was provided by service.provide");
}
