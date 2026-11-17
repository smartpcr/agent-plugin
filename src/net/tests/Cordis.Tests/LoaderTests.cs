using System.Text.Json;
using Cordis.Loading;
using Cordis.Timers;

namespace Cordis.Tests;

public static class LoaderTests
{
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

    public static void Register()
    {
        Harness.Case("loader: children of a disabled group do not run", async () =>
        {
            (Context? root, LoaderService? loader, MemoryExporter _) = await StartAsync();
            EntryOptions group = Group("g", Echo("child", "c"));
            group.Disabled = true;
            await loader.ApplyAsync([group]);
            Assert.True(loader.Find("g:child")!.Fiber is null, "child of a disabled group has a fiber");
            await root.DisposeAsync();
        });

        Harness.Case("loader: children come back after their group is rebuilt", async () =>
        {
            (Context? root, LoaderService? loader, MemoryExporter _) = await StartAsync();
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
            (Context? root, LoaderService? loader, MemoryExporter _) = await StartAsync();
            EntryOptions group = Group("g", Echo("child", "c"));
            group.Inject = ["counter"];
            await loader.ApplyAsync([group]);
            Assert.True(loader.Find("g:child")!.Fiber is null, "child of a pending group loaded");

            await root.Use<CounterService>();
            await Assert.Eventually(() => loader.Find("g:child")!.IsActive, "child did not load with its group");
            await root.DisposeAsync();
        });

        Harness.Case("loader: an invalid or failing entry does not stop its siblings", async () =>
        {
            (Context? root, LoaderService? loader, MemoryExporter? logs) = await StartAsync();
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
            string errors = string.Join('\n', logs.Messages.Where(m => m.Level == LogLevel.Error).Select(m => m.Text));
            Assert.True(errors.Contains("must not contain") && errors.Contains("requires a name") && errors.Contains("cannot resolve"), errors);
            await root.DisposeAsync();
        });

        Harness.Case("loader: isolate labels are per service, and \"\" is a shared label", async () =>
        {
            (Context? root, LoaderService? loader, MemoryExporter _) = await StartAsync();
            var isolate = new Dictionary<string, string> { ["timer"] = "shared", ["counter"] = "shared" };
            await loader.ApplyAsync(
            [
                new EntryOptions { Id = "timer", Name = nameof(TimerService), Isolate = isolate },
                new EntryOptions { Id = "counter", Name = nameof(CounterService), Isolate = new(isolate) },
                new EntryOptions { Id = "a", Name = nameof(CounterService), Isolate = new() { ["counter"] = "" } },
                new EntryOptions { Id = "b", Name = nameof(CounterService), Isolate = new() { ["counter"] = "" } },
            ]);

            Assert.True(loader.Find("timer")!.IsActive && loader.Find("counter")!.IsActive, "two services under one label collided");
            Assert.True(loader.Find("a")!.IsActive, "first entry in the \"\" realm did not load");
            Assert.Equal(FiberState.Failed, loader.Find("b")!.Fiber!.State); // same realm: already registered
            await root.DisposeAsync();
        });

        Harness.Case("json file: a runtime save keeps a concurrent file edit", async () =>
        {
            DirectoryInfo directory = Directory.CreateTempSubdirectory("cordis-merge");
            string path = Path.Combine(directory.FullName, "cordis.json");
            await File.WriteAllTextAsync(path, """
            [
              { "id": "a", "name": "EchoPlugin", "config": { "times": 1, "text": "a" } },
              { "id": "b", "name": "EchoPlugin", "config": { "times": 1, "text": "b" } }
            ]
            """);

            (Context? root, LoaderService? loader, MemoryExporter _) = await StartAsync();
            Fiber file = await root.Use<JsonConfigFile>(new JsonConfigFile.Options { Path = path, Watch = false });
            var source = (JsonConfigFile)file.Instance!;

            // the file changes "b" while the runtime changes "a", before the file is re-read
            await File.WriteAllTextAsync(path, """
            [
              { "id": "a", "name": "EchoPlugin", "config": { "times": 1, "text": "a" } },
              { "id": "b", "name": "EchoPlugin", "config": { "times": 1, "text": "edited" } }
            ]
            """);
            await loader.Find("a")!.Fiber!.UpdateAsync(new EchoPlugin.Options { Times = 2, Text = "a" });
            await source.SaveAsync();
            await source.SaveAsync();

            string text = await File.ReadAllTextAsync(path);
            Assert.True(text.Contains("\"edited\""), "the file edit was lost:\n" + text);
            Assert.True(text.Contains("\"times\": 2"), "the runtime edit was lost:\n" + text);
            Assert.Equal("edited", loader.Find("b")!.Options.Config!.Value.GetProperty("text").GetString());

            await root.DisposeAsync();
            directory.Delete(recursive: true);
        });

        Harness.Case("json file: a deleted or corrupt file keeps the live tree", async () =>
        {
            DirectoryInfo directory = Directory.CreateTempSubdirectory("cordis-missing");
            string path = Path.Combine(directory.FullName, "cordis.json");
            await File.WriteAllTextAsync(path, """[ { "id": "a", "name": "EchoPlugin", "config": { "times": 1, "text": "a" } } ]""");

            (Context? root, LoaderService? loader, MemoryExporter? logs) = await StartAsync();
            Fiber file = await root.Use<JsonConfigFile>(new JsonConfigFile.Options { Path = path, Watch = false });
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
            DirectoryInfo directory = Directory.CreateTempSubdirectory("cordis-anon");
            string path = Path.Combine(directory.FullName, "cordis.json");
            EchoPlugin.Log.Clear();
            await File.WriteAllTextAsync(path, """
            [
              { "name": "EchoPlugin", "config": { "times": 1, "text": "anon" } },
              { "id": "b", "name": "EchoPlugin", "config": { "times": 1, "text": "b" } }
            ]
            """);

            (Context? root, LoaderService _, MemoryExporter _) = await StartAsync();
            Fiber file = await root.Use<JsonConfigFile>(new JsonConfigFile.Options { Path = path, Watch = false });
            await File.WriteAllTextAsync(path, """
            [
              { "name": "EchoPlugin", "config": { "times": 1, "text": "anon" } },
              { "id": "b", "name": "EchoPlugin", "config": { "times": 1, "text": "b2" } }
            ]
            """);
            await ((JsonConfigFile)file.Instance!).ReloadAsync();

            // not restarted by the reload: exactly one "-anon", from the final dispose
            await root.DisposeAsync();
            Assert.Equal(1, EchoPlugin.Log.Count(line => line == "-anon"), string.Join(',', EchoPlugin.Log));
            directory.Delete(recursive: true);
        });
    }
}
