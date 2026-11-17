// -----------------------------------------------------------------------
// <copyright file="HeartbeatPlugin.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

using System.ComponentModel.DataAnnotations;
using Cordis;
using Cordis.Timers;

/// <summary>
/// Sample plugin that depends on the <c>database</c> and <c>timer</c> services and logs a heartbeat
/// containing the database greeting at a configured interval; the interval stops when the plugin unloads.
/// </summary>
[Inject("database")]
[Inject("timer")]
public sealed class HeartbeatPlugin
{
    /// <summary>The configuration schema that validates <see cref="Options"/> through data annotations.</summary>
    public static readonly IConfigSchema Schema = DataAnnotationsSchema<Options>.Instance;

    /// <summary>Initializes a new instance of the <see cref="HeartbeatPlugin"/> class.</summary>
    /// <param name="ctx">The context that provides the <see cref="DatabaseService"/> and <see cref="TimerService"/>.</param>
    /// <param name="config">The validated plugin configuration.</param>
    public HeartbeatPlugin(Context ctx, Options config)
    {
        var database = ctx.Get<DatabaseService>();
        var timer = ctx.Get<TimerService>();

        // The interval is owned by *this* fiber: unloading the plugin stops it.
        timer.Interval(
            () => ctx.Logger.Info($"beat: {database.Read("greeting")}"),
            TimeSpan.FromMilliseconds(config.IntervalMs));

        ctx.Effect(
            () =>
            {
                ctx.Logger.Info($"heartbeat every {config.IntervalMs}ms");
                return () => ctx.Logger.Info("heartbeat stopped");
            },
            "heartbeat");
    }

    /// <summary>Configuration for <see cref="HeartbeatPlugin"/>.</summary>
    public sealed class Options
    {
        /// <summary>Gets or sets the heartbeat interval in milliseconds; must be between 10 and 60,000.</summary>
        [Range(10, 60_000)]
        public int IntervalMs { get; set; } = 250;
    }
}
