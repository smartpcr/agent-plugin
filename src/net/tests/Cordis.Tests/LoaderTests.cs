// -----------------------------------------------------------------------
// <copyright file="LoaderTests.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.Tests;

using System.Text.Json;
using Cordis.Loading;
using Cordis.Timers;

/// <summary>Tests for the <see cref="LoaderService"/> entry tree and the <see cref="JsonConfigFile"/> configuration source.</summary>
public static class LoaderTests
{
    /// <summary>Registers the loader and JSON configuration file test cases with the <see cref="Harness"/>.</summary>
    public static void Register()
    {
        Harness.Case("loader: children of a disabled group do not run", async () =>
        {
            (var root, var loader, var _) = await StartAsync();
            var group = Group("g", Echo("child", "c"));
            group.Disabled = true;
            await loader.ApplyAsync([group]);
            Assert.True(loader.Find("g:child")!.Fiber is null, "child of a disabled group has a fiber");
            await root.DisposeAsync();
        });

        Harness.Case("loader: children come back after their group is rebuilt", async () =>
        {
            (var root, var loader, var _) = await StartAsync();
            await loader.ApplyAsync([Group("g", Echo("child", "c"))]);
            Assert.True(loader.Find("g:child")!.IsActive, "child never loaded");

            await loader.SetDisabledAsync("g", true);
            Assert.True(loader.Find("g:child")!.Fiber is null, "child survived its disabled group");

            await loader.SetDisabledAsync("g", false);
            await Assert.Eventually(() => loader.Find("g:child")!.IsActive, "child did not come back with its group");
            await root.DisposeAsync();
        });

        Harness.Case("loader: a pending group holds its children back", async () =>
        {
            (var root, var loader, var _) = await StartAsync();
            var group = Group("g", Echo("child", "c"));
            group.Inject = ["counter"];
            await loader.ApplyAsync([group]);
            Assert.True(loader.Find("g:child")!.Fiber is null, "child of a pending group loaded");

            await root.Use<CounterService>();
            await Assert.Eventually(() => loader.Find("g:child")!.IsActive, "child did not load with its group");
            await root.DisposeAsync();
        });

        Harness.Case("loader: an invalid or failing entry does not stop its siblings", async () =>
        {
            (var root, var loader, var logs) = await StartAsync();
            await loader.ApplyAsync(
            [
                new EntryOptions { Id = "bad:id", Name = nameof(EchoPlugin) },
                new EntryOptions { Id = "nameless" },
                new EntryOptions { Id = "unknown", Name = "NoSuchComponent" },
                new EntryOptions { Id = "broken", Name = nameof(FailingComponent) },
                Echo("ok", "fine"),
            ]);

            Assert.True(loader.Find("ok")!.IsActive, "valid sibling did not load");
            Assert.True(loader.Find("bad:id") is null && loader.Find("nameless") is null, "invalid entries were created");
            Assert.Equal(FiberState.Failed, loader.Find("broken")!.Fiber!.State);
            var errors = string.Join('\n', logs.Messages.Where(m => m.Level == LogLevel.Error).Select(m => m.Text));
            Assert.True(errors.Contains("must not contain", StringComparison.Ordinal) && errors.Contains("requires a name", StringComparison.Ordinal) && errors.Contains("cannot resolve", StringComparison.Ordinal), errors);
            await root.DisposeAsync();
        });

        Harness.Case("loader: isolate labels are per service, and \"\" is a shared label", async () =>
        {
            (var root, var loader, var _) = await StartAsync();
            var isolate = new Dictionary<string, string> { ["timer"] = "shared", ["counter"] = "shared" };
            await loader.ApplyAsync(
            [
                new EntryOptions { Id = "timer", Name = nameof(TimerService), Isolate = isolate },
                new EntryOptions { Id = "counter", Name = nameof(CounterService), Isolate = new(isolate) },
                new EntryOptions { Id = "a", Name = nameof(CounterService), Isolate = new() { ["counter"] = string.Empty } },
                new EntryOptions { Id = "b", Name = nameof(CounterService), Isolate = new() { ["counter"] = string.Empty } },
            ]);

            Assert.True(loader.Find("timer")!.IsActive && loader.Find("counter")!.IsActive, "two services under one label collided");
            Assert.True(loader.Find("a")!.IsActive, "first entry in the \"\" realm did not load");
            Assert.Equal(FiberState.Failed, loader.Find("b")!.Fiber!.State); // same realm: already registered
            await root.DisposeAsync();
        });

        Harness.Case("json file: a runtime save keeps a concurrent file edit", async () =>
        {
            var directory = Directory.CreateTempSubdirectory("cordis-merge");
            var path = Path.Combine(directory.FullName, "cordis.json");
            var json = """
            [
              { "id": "a", "name": "EchoPlugin", "config": { "times": 1, "text": "a" } },
              { "id": "b", "name": "EchoPlugin", "config": { "times": 1, "text": "b" } }
            ]
            """;
            await File.WriteAllTextAsync(path, json);

            (var root, var loader, var _) = await StartAsync();
            var file = await root.Use<JsonConfigFile>(new JsonConfigFile.Options { Path = path, Watch = false });
            var source = (JsonConfigFile)file.Instance!;

            // the file changes "b" while the runtime changes "a", before the file is re-read
            json = """
            [
              { "id": "a", "name": "EchoPlugin", "config": { "times": 1, "text": "a" } },
              { "id": "b", "name": "EchoPlugin", "config": { "times": 1, "text": "edited" } }
            ]
            """;
            await File.WriteAllTextAsync(path, json);
            await loader.Find("a")!.Fiber!.UpdateAsync(new EchoPlugin.Options { Times = 2, Text = "a" });
            await source.SaveAsync();
            await source.SaveAsync();

            var text = await File.ReadAllTextAsync(path);
            Assert.True(text.Contains("\"edited\"", StringComparison.Ordinal), "the file edit was lost:\n" + text);
            Assert.True(text.Contains("\"times\": 2", StringComparison.Ordinal), "the runtime edit was lost:\n" + text);
            Assert.Equal("edited", loader.Find("b")!.Options.Config!.Value.GetProperty("text").GetString());

            await root.DisposeAsync();
            directory.Delete(recursive: true);
        });

        Harness.Case("json file: a deleted or corrupt file keeps the live tree", async () =>
        {
            var directory = Directory.CreateTempSubdirectory("cordis-missing");
            var path = Path.Combine(directory.FullName, "cordis.json");
            await File.WriteAllTextAsync(path, """[ { "id": "a", "name": "EchoPlugin", "config": { "times": 1, "text": "a" } } ]""");

            (var root, var loader, var logs) = await StartAsync();
            var file = await root.Use<JsonConfigFile>(new JsonConfigFile.Options { Path = path, Watch = false });
            var source = (JsonConfigFile)file.Instance!;

            File.Delete(path);
            await source.ReloadAsync();
            Assert.True(loader.Find("a")!.IsActive, "entry was dropped when the file vanished");
            Assert.True(!File.Exists(path), "a missing file was recreated at runtime");

            await File.WriteAllTextAsync(path, "null");
            await source.ReloadAsync();
            Assert.True(loader.Find("a")!.IsActive, "entry was dropped for a non-array file");
            Assert.Equal("null", await File.ReadAllTextAsync(path));
            Assert.Equal(2, logs.Messages.Count(m => m.Level == LogLevel.Warn));

            await root.DisposeAsync();
            directory.Delete(recursive: true);
        });

        Harness.Case("json file: anonymous entries survive an edit to a neighbour", async () =>
        {
            var directory = Directory.CreateTempSubdirectory("cordis-anon");
            var path = Path.Combine(directory.FullName, "cordis.json");
            EchoPlugin.Log.Clear();
            var json = """
            [
              { "name": "EchoPlugin", "config": { "times": 1, "text": "anon" } },
              { "id": "b", "name": "EchoPlugin", "config": { "times": 1, "text": "b" } }
            ]
            """;
            await File.WriteAllTextAsync(path, json);

            (var root, var _, var _) = await StartAsync();
            var file = await root.Use<JsonConfigFile>(new JsonConfigFile.Options { Path = path, Watch = false });
            json = """
            [
              { "name": "EchoPlugin", "config": { "times": 1, "text": "anon" } },
              { "id": "b", "name": "EchoPlugin", "config": { "times": 1, "text": "b2" } }
            ]
            """;
            await File.WriteAllTextAsync(path, json);
            await ((JsonConfigFile)file.Instance!).ReloadAsync();

            // not restarted by the reload: exactly one "-anon", from the final dispose
            await root.DisposeAsync();
            Assert.Equal(1, EchoPlugin.Log.Count(line => line == "-anon"), string.Join(',', EchoPlugin.Log));
            directory.Delete(recursive: true);
        });

        // upstream include/tests/watch.spec.ts: "keeps watching the file across its own rename-based writes"
        Harness.Case("json file: watching survives rename-based saves, by others and by itself", async () =>
        {
            var directory = Directory.CreateTempSubdirectory("cordis-watch");
            var path = Path.Combine(directory.FullName, "cordis.json");
            var json = """[ { "id": "a", "name": "EchoPlugin", "config": { "times": 1, "text": "v1" } } ]""";
            await File.WriteAllTextAsync(path, json);

            (var root, var loader, var _) = await StartAsync();
            await root.Use<JsonConfigFile>(new JsonConfigFile.Options { Path = path, Watch = true, DebounceMs = 20 });

            string Text() => loader.Find("a")?.Options.Config?.GetProperty("text").GetString() ?? string.Empty;

            // an editor-style save: write a temporary file, then rename it over the config
            async Task SaveByRenameAsync(string text)
            {
                var temp = Path.Combine(directory.FullName, $"cordis.json.{Guid.NewGuid():n}.tmp");
                var content = $$"""[ { "id": "a", "name": "EchoPlugin", "config": { "times": 1, "text": "{{text}}" } } ]""";
                await File.WriteAllTextAsync(temp, content);
                File.Move(temp, path, overwrite: true);
            }

            await SaveByRenameAsync("v2");
            await Assert.Eventually(() => Text() == "v2", "the first rename-based save was not picked up");

            // the component's own write-back is itself a rename; watching must continue after it
            await loader.Find("a")!.Fiber!.UpdateAsync(new EchoPlugin.Options { Times = 1, Text = "runtime" });
            await Assert.Eventually(
                () => File.ReadAllText(path).Contains("\"runtime\"", StringComparison.Ordinal),
                "the runtime change was not written back");

            await SaveByRenameAsync("v3");
            await Assert.Eventually(() => Text() == "v3", "a save after the component's own write was not picked up");

            await root.DisposeAsync();
            directory.Delete(recursive: true);
        });
    }

    private static async Task<(Context Root, LoaderService Loader, MemoryExporter Logs)> StartAsync()
    {
        var root = new Context();
        root.Loggers.ClearExporters();
        var logs = new MemoryExporter();
        root.Loggers.AddExporter(logs);
        await root.Use<LoaderService>();
        return (root, root.Get<LoaderService>(), logs);
    }

    private static EntryOptions Echo(string id, string text) => new()
    {
        Id = id,
        Name = nameof(EchoPlugin),
        Config = JsonSerializer.SerializeToElement(new { times = 1, text }),
    };

    private static EntryOptions Group(string id, params EntryOptions[] children) => new()
    {
        Id = id,
        Name = "group",
        Group = true,
        Children = [.. children],
    };
}
