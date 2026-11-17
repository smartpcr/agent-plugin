// -----------------------------------------------------------------------
// <copyright file="Program.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

using System.Text.Json;
using Cordis;
using Cordis.Loading;
using Cordis.Timers;

/// <summary>
/// Sample host that demonstrates imperative composition, live reconfiguration, dependency removal and
/// restoration, and declarative composition through the loader.
/// </summary>
public static class Program
{
    /// <summary>Runs the sample scenario and then disposes the root context.</summary>
    /// <returns>A task that completes when the scenario has finished and the root context is disposed.</returns>
    public static async Task Main()
    {
        var root = new Context();
        root.Loggers.Level = LogLevel.Debug;

        // 1. imperative composition
        await root.Use<TimerService>();
        var database = root.Use<DatabaseService>();

        // The heartbeat is registered before the database exists; it stays PENDING and
        // starts by itself once the database finishes InitAsync.
        var heartbeat = root.Use<HeartbeatPlugin>(new HeartbeatPlugin.Options { IntervalMs = 200 });
        Console.WriteLine($"heartbeat before database: {heartbeat.State}");

        await database;
        await heartbeat;
        Console.WriteLine($"heartbeat after database:  {heartbeat.State}");

        await Task.Delay(500);

        // 2. live reconfiguration: the plugin unloads and reloads with the new config
        await heartbeat.UpdateAsync(new HeartbeatPlugin.Options { IntervalMs = 100 });
        await Task.Delay(300);

        // 3. pull the dependency: every dependent unloads, then comes back
        await database.DisposeAsync();
        Console.WriteLine($"heartbeat without database: {heartbeat.State}");
        await root.Use<DatabaseService>();
        await heartbeat;
        Console.WriteLine($"heartbeat restored:         {heartbeat.State}");
        await Task.Delay(300);

        // 4. declarative composition through the loader
        var configPath = Path.Combine(Path.GetTempPath(), "cordis-sample.json");
        await File.WriteAllTextAsync(configPath, JsonSerializer.Serialize(
            new[]
            {
                new EntryOptions
                {
                    Id = "beat2",
                    Name = nameof(HeartbeatPlugin),
                    Config = JsonSerializer.SerializeToElement(new { intervalMs = 150 }),
                },
            },
            LoaderService.JsonOptions));

        await root.Use<LoaderService>();
        await root.Use<JsonConfigFile>(new JsonConfigFile.Options { Path = configPath, Watch = true });
        Console.WriteLine("loaded from file; edit it and watch it reload");
        await Task.Delay(500);

        await root.DisposeAsync();
        File.Delete(configPath);
    }
}
