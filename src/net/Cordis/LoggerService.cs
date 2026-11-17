// -----------------------------------------------------------------------
// <copyright file="LoggerService.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// Provided on the root context as the <c>logger</c> service. Creates <see cref="Logger"/> instances,
/// holds the default level and fans every written message out to its exporters. A
/// <see cref="ConsoleExporter"/> is registered by default.
/// </summary>
[ServiceName(ServiceName)]
public sealed class LoggerService
{
    /// <summary>The service name, <c>logger</c>, also used as the intercept name for <see cref="LoggerConfig"/>.</summary>
    public const string ServiceName = "logger";

    private readonly List<ILogExporter> exporters = [new ConsoleExporter()];

    /// <summary>
    /// Gets or sets the default level, used by loggers whose context has no <see cref="LoggerConfig"/>
    /// intercept. Defaults to <see cref="LogLevel.Info"/>.
    /// </summary>
    public LogLevel Level { get; set; } = LogLevel.Info;

    /// <summary>Gets a snapshot of the registered exporters, in registration order.</summary>
    public IReadOnlyList<ILogExporter> Exporters
    {
        get
        {
            lock (this.exporters)
            {
                return [.. this.exporters];
            }
        }
    }

    /// <summary>Registers an exporter that receives every message written from now on.</summary>
    /// <param name="exporter">The exporter to add.</param>
    /// <returns>An <see cref="IDisposable"/> that removes the exporter when disposed.</returns>
    public IDisposable AddExporter(ILogExporter exporter)
    {
        lock (this.exporters)
        {
            this.exporters.Add(exporter);
        }

        return new Removal(this, exporter);
    }

    /// <summary>Removes every registered exporter, including the default <see cref="ConsoleExporter"/>.</summary>
    public void ClearExporters()
    {
        lock (this.exporters)
        {
            this.exporters.Clear();
        }
    }

    /// <summary>Creates a logger bound to <paramref name="ctx"/> and named after its fiber.</summary>
    /// <param name="ctx">The context whose <see cref="LoggerConfig"/> intercept determines the level.</param>
    /// <returns>A new <see cref="Logger"/> named after <paramref name="ctx"/>'s fiber.</returns>
    public Logger For(Context ctx) => new(this, ctx, ctx.Fiber.Name);

    /// <summary>Creates a logger bound to <paramref name="ctx"/> with an explicit name.</summary>
    /// <param name="ctx">The context whose <see cref="LoggerConfig"/> intercept determines the level.</param>
    /// <param name="name">The logger name written with every message.</param>
    /// <returns>A new <see cref="Logger"/> with the given name.</returns>
    public Logger For(Context ctx, string name) => new(this, ctx, name);

    internal void Export(LogMessage message)
    {
        foreach (var exporter in this.Exporters)
        {
            try
            {
                exporter.Export(message);
            }
            catch
            { /* an exporter must never break the caller */
            }
        }
    }

    private sealed class Removal(LoggerService service, ILogExporter exporter) : IDisposable
    {
        public void Dispose()
        {
            lock (service.exporters)
            {
                service.exporters.Remove(exporter);
            }
        }
    }
}
