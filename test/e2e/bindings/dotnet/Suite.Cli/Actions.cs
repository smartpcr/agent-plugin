// -----------------------------------------------------------------------
// <copyright file="Actions.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.E2E;

using System.Text.Json;
using Bdd.Suite.Actions;
using Bdd.Suite.Extensions;
using Bdd.Suite.Model;
using Bdd.Suite.Values;
using Cordis.Loading;
using Cordis.Timers;

/// <summary>Every action of the catalog, as the VS Code extension finds them.</summary>
internal static class Actions
{
    public static IReadOnlyList<ActionRegistration> All { get; } =
    [
        new ActionRegistration("host.start", HostStart),
        new ActionRegistration("host.dispose", HostDispose),
        new ActionRegistration("host.trace", HostTrace),
        new ActionRegistration("host.settle", HostSettle),
        new ActionRegistration("host.waitFor", HostWaitFor),
        new ActionRegistration("gate.open", GateOpen),
        new ActionRegistration("component.use", ComponentUse),
        new ActionRegistration("component.wait", ComponentWait),
        new ActionRegistration("component.state", ComponentState),
        new ActionRegistration("component.update", ComponentUpdate),
        new ActionRegistration("component.restart", ComponentRestart),
        new ActionRegistration("component.dispose", ComponentDispose),
        new ActionRegistration("component.replace", ComponentReplace),
        new ActionRegistration("service.provide", ServiceProvide),
        new ActionRegistration("service.withdraw", ServiceWithdraw),
        new ActionRegistration("service.lookup", ServiceLookup),
        new ActionRegistration("events.listen", EventsListen),
        new ActionRegistration("events.dispatch", EventsDispatch),
        new ActionRegistration("timer.schedule", TimerSchedule),
        new ActionRegistration("loader.apply", LoaderApply),
        new ActionRegistration("loader.entry", LoaderEntry),
        new ActionRegistration("loader.setDisabled", LoaderSetDisabled),
        new ActionRegistration("configFile.open", ConfigFileOpen),
        new ActionRegistration("configFile.write", ConfigFileWrite),
        new ActionRegistration("configFile.sync", ConfigFileSync),
        new ActionRegistration("configFile.read", ConfigFileRead),
    ];

    // ------------------------------------------------------------------ host
    private static object? HostStart(ActionRequest request) => Run(request, async (_, _) =>
    {
        var table = Table(request);
        if (table.Take(request) is { } previous)
        {
            await previous.DisposeAsync();
        }

        var host = await ProbeHost.StartAsync(Args.Bool(request.Step, "loader", true), Args.Bool(request.Step, "timer", true));
        table.Put(request, host);
        return Map(("ok", true));
    });

    private static object? HostDispose(ActionRequest request) => Run(request, async (_, _) =>
    {
        // the host stays in the table, so a second dispose is a no-op
        var host = Table(request).Get(request);
        await host.DisposeAsync();
        return Map(("ok", true));
    });

    private static object? HostTrace(ActionRequest request)
    {
        var host = Table(request).Get(request);
        var prefix = request.Step.Text("prefix");
        var contains = request.Step.Text("contains");
        var events = host.Trace.Snapshot()
            .Where(marker => marker.StartsWith(prefix, StringComparison.Ordinal))
            .Where(marker => marker.Contains(contains, StringComparison.Ordinal))
            .ToList();
        return Map(("events", events.Cast<object?>().ToList()), ("joined", string.Join(',', events)), ("count", events.Count));
    }

    private static object? HostSettle(ActionRequest request) => Run(request, async (host, cancel) =>
    {
        await host.SettleAsync(cancel);
        return Map(("ok", true));
    });

    private static object? HostWaitFor(ActionRequest request) => Run(request, async (host, cancel) =>
    {
        var marker = request.Step.Text("marker");
        while (!host.Trace.Snapshot().Any(seen => seen.Contains(marker, StringComparison.Ordinal)))
        {
            await Task.Delay(10, cancel);
        }

        return Map(("found", true), ("joined", string.Join(',', host.Trace.Snapshot())));
    });

    private static object? GateOpen(ActionRequest request)
    {
        var name = request.Step.Text("name");
        Table(request).Get(request).OpenGate(name);
        return Map(("name", name));
    }

    // ------------------------------------------------------------ components
    private static object? ComponentUse(ActionRequest request)
    {
        var host = Table(request).Get(request);
        var spec = ProbeSpec.FromMap(request.Step);
        var id = spec.Id ?? throw ActionException.Failed("component.use: id is required");
        var parent = Args.TextOrNull(request.Step, "parent") is { } parentId ? host.Fiber(parentId).Ctx : host.Root;
        var scope = host.Isolated(parent, Isolate(request.Step.Map("isolate")));
        var implementation = host.Implementation(Args.TextOrNull(request.Step, "implementation") ?? "probe", null);
        var fiber = scope.Use(ComponentSource.ForInstance(implementation), spec, spec.Inject);
        host.Register(id, fiber);
        return Map(("id", id), ("state", fiber.State.ToString()));
    }

    private static object? ComponentWait(ActionRequest request) => Run(request, async (host, cancel) =>
    {
        var id = request.Step.Text("id");
        var fiber = host.Fiber(id);
        await Quietly(fiber.WaitAsync(), cancel);
        return FiberState(host, id, fiber);
    });

    private static object? ComponentState(ActionRequest request)
    {
        var host = Table(request).Get(request);
        var id = request.Step.Text("id");
        return FiberState(host, id, host.Fiber(id));
    }

    private static object? ComponentUpdate(ActionRequest request) => Run(request, async (host, cancel) =>
    {
        var id = request.Step.Text("id");
        var fiber = host.Fiber(id);
        var current = fiber.Config as ProbeSpec ?? new ProbeSpec();
        var next = current.WithValue(Args.IntOrNull(request.Step, "value") ?? 0);
        if (!Args.Bool(request.Step, "wait", true))
        {
            _ = fiber.UpdateAsync(next).ContinueWith(_ => { }, TaskScheduler.Default);
            return Map(("accepted", true), ("error", null), ("state", fiber.State.ToString()), ("loads", host.Loads(id)));
        }

        string? error = null;
        try
        {
            await fiber.UpdateAsync(next).WaitAsync(cancel);
        }
        catch (OperationCanceledException)
        {
            throw;
        }
        catch (Exception failure)
        {
            error = failure.Message;
        }

        await host.SettleAsync(cancel);
        return Map(("accepted", error is null), ("error", error), ("state", fiber.State.ToString()), ("loads", host.Loads(id)));
    });

    private static object? ComponentRestart(ActionRequest request) => Run(request, async (host, cancel) =>
    {
        var id = request.Step.Text("id");
        var fiber = host.Fiber(id);
        var restart = fiber.RestartAsync();
        if (Args.Bool(request.Step, "wait", true))
        {
            await Quietly(restart, cancel);
            await host.SettleAsync(cancel);
        }
        else
        {
            _ = restart.ContinueWith(_ => { }, TaskScheduler.Default);
        }

        return FiberState(host, id, fiber);
    });

    private static object? ComponentDispose(ActionRequest request) => Run(request, async (host, cancel) =>
    {
        var id = request.Step.Text("id");
        var fiber = host.Fiber(id);
        var dispose = fiber.DisposeAsync().AsTask();
        if (Args.Bool(request.Step, "wait", true))
        {
            await Quietly(dispose, cancel);
        }
        else
        {
            _ = dispose.ContinueWith(_ => { }, TaskScheduler.Default);
        }

        return FiberState(host, id, fiber);
    });

    private static object? ComponentReplace(ActionRequest request)
    {
        var host = Table(request).Get(request);
        var from = host.Implementation(request.Step.Text("from"), null);
        var to = host.Implementation(request.Step.Text("to"), Args.Strings(request.Step, "inject") ?? []);
        var moved = host.Root.Registry.Replace(from, to);
        return Map(("moved", moved.Count));
    }

    // ---------------------------------------------------------------- services
    private static object? ServiceProvide(ActionRequest request)
    {
        var host = Table(request).Get(request);
        var name = request.Step.Text("name");
        var value = Args.TextOrNull(request.Step, "value") ?? name;
        var scope = Args.TextOrNull(request.Step, "realm") is { } label ? host.Root.Isolate(name, host.Realm(name, label)) : host.Root;
        host.KeepService(name, scope.Provide(name, value));
        return Map(("name", name));
    }

    private static object? ServiceWithdraw(ActionRequest request) => Run(request, async (host, cancel) =>
    {
        var handle = host.TakeService(request.Step.Text("name"));
        var withdraw = handle.DisposeAsync().AsTask();
        if (Args.Bool(request.Step, "wait", true))
        {
            await withdraw.WaitAsync(cancel);
            await host.SettleAsync(cancel);
        }

        return Map(("done", withdraw.IsCompleted));
    });

    private static object? ServiceLookup(ActionRequest request)
    {
        var host = Table(request).Get(request);
        var name = request.Step.Text("name");
        var from = Args.TextOrNull(request.Step, "from") is { } id ? host.Fiber(id).Ctx : host.Root;
        var optional = Args.Bool(request.Step, "optional", false);
        if (optional)
        {
            // The port's unchecked lookup (upstream's ctx.get): no inject check, never fails.
            var found = from.Lookup(name);
            return Map(("found", found is not null), ("value", found?.ToString()), ("error", null));
        }

        try
        {
            var value = from.Get(name, required: true);
            return Map(("found", value is not null), ("value", value?.ToString()), ("error", null));
        }
        catch (ServiceNotInjectedException)
        {
            return Map(("found", false), ("value", null), ("error", "not-injected"));
        }
        catch (ServiceUnavailableException)
        {
            return Map(("found", false), ("value", null), ("error", "unavailable"));
        }
    }

    // ------------------------------------------------------------------ events
    private static object? EventsListen(ActionRequest request)
    {
        var host = Table(request).Get(request);
        var owner = request.Step.Text("owner");
        var ctx = host.Fiber(owner).Ctx;
        var name = request.Step.Text("event");
        var mode = request.Step.Text("mode");
        var label = request.Step.Text("label");
        var behaviour = Args.TextOrNull(request.Step, "behaviour") ?? "record";
        var answer = request.Step.Text("answer");
        var delay = Args.IntOrNull(request.Step, "delayMs") ?? 100;
        var prepend = Args.Bool(request.Step, "prepend", false);
        var marker = $"{owner}:listener {label}";

        async ValueTask Enter()
        {
            if (behaviour == "delay")
            {
                await Task.Delay(delay);
            }

            host.Trace.Add(marker);
            if (behaviour == "throw")
            {
                throw new InvalidOperationException($"listener {label} failed on purpose");
            }
        }

        switch (mode)
        {
            case "emit":
            case "parallel":
                ctx.On(host.EventKey(name, "emit", () => new EventKey<string>(name)), async (_, _) => await Enter(), prepend);
                break;
            case "bail":
                ctx.On(
                    host.EventKey(name, mode, () => new BailKey<string, string>(name)),
                    async (_, _) =>
                    {
                        await Enter();
                        return behaviour is "answer" or "veto" ? answer : null;
                    },
                    prepend);
                break;
            case "waterfall":
                ctx.On(
                    host.EventKey(name, mode, () => new WaterfallKey<string>(name)),
                    async (_, value, next) =>
                    {
                        await Enter();
                        return behaviour switch
                        {
                            "veto" => answer,
                            "answer" => await next(answer),
                            "callNextTwice" => await NextTwice(next, value),
                            _ => await next(value),
                        };
                    },
                    prepend);
                break;
            default:
                throw ActionException.Failed($"events.listen: unknown mode {mode}");
        }

        return Map(("label", label));

        static async ValueTask<string> NextTwice(Func<string, ValueTask<string>> next, string value)
        {
            await next(value);
            return await next(value);
        }
    }

    private static object? EventsDispatch(ActionRequest request) => Run(request, async (host, cancel) =>
    {
        var name = request.Step.Text("event");
        var mode = request.Step.Text("mode");
        var fromId = Args.TextOrNull(request.Step, "from") ?? "root";
        var ctx = fromId == "root" ? host.Root : host.Fiber(fromId).Ctx;
        var value = request.Step.Text("value");
        string? result = null;
        string? error = null;
        var errors = 0;
        try
        {
            switch (mode)
            {
                case "emit":
                    await ctx.EmitAsync(host.EventKey(name, "emit", () => new EventKey<string>(name)), value);
                    break;
                case "parallel":
                    await ctx.ParallelAsync(host.EventKey(name, "emit", () => new EventKey<string>(name)), value);
                    break;
                case "bail":
                    result = await ctx.BailAsync(host.EventKey(name, mode, () => new BailKey<string, string>(name)), value);
                    break;
                case "waterfall":
                    result = await ctx.WaterfallAsync(host.EventKey(name, mode, () => new WaterfallKey<string>(name)), value, terminal =>
                    {
                        host.Trace.Add($"{fromId}:terminal");
                        return new ValueTask<string>(terminal);
                    });
                    break;
                default:
                    throw ActionException.Failed($"events.dispatch: unknown mode {mode}");
            }
        }
        catch (ActionException)
        {
            throw;
        }
        catch (Exception failure)
        {
            error = failure.Message;
            errors = failure is AggregateException aggregate ? aggregate.InnerExceptions.Count : 1;
        }

        _ = cancel;
        return Map(("result", result), ("error", error), ("errors", errors));
    });

    // ------------------------------------------------------------------ timers
    private static object? TimerSchedule(ActionRequest request)
    {
        var host = Table(request).Get(request);
        var owner = request.Step.Text("owner");
        var label = request.Step.Text("label");
        var delay = TimeSpan.FromMilliseconds(Args.IntOrNull(request.Step, "delayMs") ?? 0);
        var calls = Args.IntOrNull(request.Step, "calls") ?? 1;
        var timer = host.Fiber(owner).Ctx.Get<TimerService>();
        void Fire() => host.Trace.Add($"{owner}:timer {label}");

        switch (request.Step.Text("kind"))
        {
            case "timeout":
                timer.Timeout(Fire, delay);
                break;
            case "interval":
                timer.Interval(Fire, delay);
                break;
            case "debounce":
                var debounced = timer.Debounce<int>(_ => Fire(), delay);
                for (var i = 0; i < calls; i++)
                {
                    debounced(i);
                }

                break;
            case "throttle":
                var throttled = timer.Throttle(Fire, delay);
                for (var i = 0; i < calls; i++)
                {
                    throttled();
                }

                break;
            default:
                throw ActionException.Failed("timer.schedule: unknown kind");
        }

        return Map(("label", label));
    }

    // ------------------------------------------------------------------ loader
    private static object? LoaderApply(ActionRequest request) => Run(request, async (host, cancel) =>
    {
        var loader = Loader(host);
        var entries = (request.Step.List("entries") ?? []).OfType<OrderedMap>().Select(Entry).ToList();
        await loader.ApplyAsync(entries).WaitAsync(cancel);
        await host.SettleAsync(cancel);
        return Map(("entries", loader.Entries.Count));
    });

    private static object? LoaderEntry(ActionRequest request)
    {
        var host = Table(request).Get(request);
        var id = request.Step.Text("id");
        var entry = Loader(host).Find(id);
        var fiber = entry?.Fiber is { Uid: not null } live ? live : null;
        return Map(
            ("exists", entry is not null),
            ("hasFiber", fiber is not null),
            ("state", fiber?.State.ToString()),
            ("loads", host.Loads(id)),
            ("value", (fiber?.Config as ProbeSpec)?.Value ?? 0));
    }

    private static object? LoaderSetDisabled(ActionRequest request) => Run(request, async (host, cancel) =>
    {
        await Loader(host).SetDisabledAsync(request.Step.Text("id"), Args.Bool(request.Step, "disabled", true)).WaitAsync(cancel);
        await host.SettleAsync(cancel);
        return Map(("ok", true));
    });

    private static object? ConfigFileOpen(ActionRequest request) => Run(request, async (host, cancel) =>
    {
        var directory = Directory.CreateTempSubdirectory("cordis-e2e");
        var path = Path.Combine(directory.FullName, "cordis.json");
        await File.WriteAllTextAsync(path, EntriesJson(request.Step.List("entries")), cancel);
        host.ConfigPath = path;
        var options = new JsonConfigFile.Options { Path = path, Watch = Args.Bool(request.Step, "watch", false), DebounceMs = 50 };
        var fiber = await host.Root.Use<JsonConfigFile>(options).WaitAsync().WaitAsync(cancel);
        host.ConfigFile = fiber.Instance as JsonConfigFile;
        await host.SettleAsync(cancel);
        return Map(("path", path));
    });

    private static object? ConfigFileWrite(ActionRequest request) => Run(request, async (host, cancel) =>
    {
        var path = host.ConfigPath ?? throw ActionException.Failed("configFile.write: run configFile.open first");
        var text = Args.TextOrNull(request.Step, "text") ?? EntriesJson(request.Step.List("entries"));
        if (Args.Bool(request.Step, "byRename", false))
        {
            var temp = Path.Combine(Path.GetDirectoryName(path)!, $"cordis.json.{Guid.NewGuid():n}.tmp");
            await File.WriteAllTextAsync(temp, text, cancel);
            File.Move(temp, path, overwrite: true);
        }
        else
        {
            await File.WriteAllTextAsync(path, text, cancel);
        }

        return Map(("ok", true));
    });

    private static object? ConfigFileSync(ActionRequest request) => Run(request, async (host, cancel) =>
    {
        var file = host.ConfigFile ?? throw ActionException.Failed("configFile.sync: run configFile.open first");
        await file.ReloadAsync().WaitAsync(cancel);
        await host.SettleAsync(cancel);
        return Map(("ok", true));
    });

    private static object? ConfigFileRead(ActionRequest request) => Run(request, async (host, cancel) =>
    {
        var path = host.ConfigPath ?? throw ActionException.Failed("configFile.read: run configFile.open first");
        return Map(("text", await File.ReadAllTextAsync(path, cancel)));
    });

    // ----------------------------------------------------------------- helpers
    private static HostTable Table(ActionRequest request)
        => request.State as HostTable ?? throw ActionException.Failed("the cordis extension did not start");

    private static LoaderService Loader(ProbeHost host)
        => host.Loader ?? throw ActionException.Failed("this host was started without the loader");

    /// <summary>Runs an async action body to completion on the caller's thread, under the step's deadline.</summary>
    private static object? Run(ActionRequest request, Func<ProbeHost, CancellationToken, Task<object?>> body)
    {
        var cancel = request.Cancellation;
        try
        {
            var host = request.Action == "host.start" ? null! : Table(request).Get(request);
            return Task.Run(() => body(host, cancel), cancel).WaitAsync(cancel).GetAwaiter().GetResult();
        }
        catch (OperationCanceledException) when (cancel.IsCancellationRequested)
        {
            throw ActionException.TimedOut($"{request.Action} did not finish within its step limit");
        }
    }

    private static async Task Quietly(Task task, CancellationToken cancel)
    {
        try
        {
            await task.WaitAsync(cancel);
        }
        catch (OperationCanceledException) when (cancel.IsCancellationRequested)
        {
            throw;
        }
        catch (Exception)
        {
            // the fiber's state carries the failure
        }
    }

    private static OrderedMap FiberState(ProbeHost host, string id, Fiber fiber) => Map(
        ("id", id),
        ("state", fiber.State.ToString()),
        ("loads", host.Loads(id)),
        ("value", (fiber.Config as ProbeSpec)?.Value ?? 0),
        ("implementation", fiber.Runtime?.Name),
        ("error", fiber.Error?.Message));

    private static OrderedMap Map(params (string Key, object? Value)[] fields)
    {
        var map = new OrderedMap();
        foreach (var (key, value) in fields)
        {
            map.Set(key, value);
        }

        return map;
    }

    private static Dictionary<string, string>? Isolate(OrderedMap? map)
        => map?.Keys.ToDictionary(key => key, key => Value.Text(map.Value(key)), StringComparer.Ordinal);

    private static EntryOptions Entry(OrderedMap map) => new()
    {
        Id = map.Text("id"),
        Name = map.Text("name"),
        Config = map.Value("config") is { } config ? JsonSerializer.SerializeToElement(Args.Plain(config)) : null,
        Group = Args.Bool(map, "group", false),
        Disabled = Args.Bool(map, "disabled", false),
        Inject = Args.Strings(map, "inject")?.ToArray(),
        Isolate = Isolate(map.Map("isolate")),
        Children = map.List("children")?.OfType<OrderedMap>().Select(Entry).ToList(),
    };

    private static string EntriesJson(List<object?>? entries)
        => JsonSerializer.Serialize((entries ?? []).OfType<OrderedMap>().Select(Entry).ToList(), LoaderService.JsonOptions);
}
