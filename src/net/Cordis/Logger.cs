// -----------------------------------------------------------------------
// <copyright file="Logger.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// A named logger bound to a context. The level comes from the nearest <see cref="LoggerConfig"/>
/// intercept, falling back to <c>[Inject("logger")]</c> metadata, then the service default.
/// </summary>
/// <param name="service">The logger service that supplies the default level and the exporters.</param>
/// <param name="ctx">The context whose <see cref="LoggerConfig"/> intercept determines the level.</param>
/// <param name="name">The logger name written with every message.</param>
public sealed class Logger(LoggerService service, Context ctx, string name)
{
    /// <summary>Gets the logger name written with every message.</summary>
    public string Name => name;

    private LogLevel Threshold =>
        ctx.GetInterceptConfig<LoggerConfig>(LoggerService.ServiceName)?.Level ?? service.Level;

    /// <summary>
    /// Logs an error. An <see cref="AggregateException"/> with members is flattened into one
    /// entry per member; one with no members is logged as itself.
    /// </summary>
    /// <param name="error">The exception to log; its message is the entry text.</param>
    public void Error(Exception error)
    {
        if (error is AggregateException { InnerExceptions.Count: > 0 } aggregate)
        {
            foreach (var inner in aggregate.InnerExceptions)
            {
                this.Error(inner);
            }

            return;
        }

        this.Write(LogLevel.Error, error.Message, error);
    }

    /// <summary>Logs a message at <see cref="LogLevel.Error"/> if the effective level allows it.</summary>
    /// <param name="text">The message text.</param>
    public void Error(string text) => this.Write(LogLevel.Error, text, null);

    /// <summary>Logs a message at <see cref="LogLevel.Warn"/> if the effective level allows it.</summary>
    /// <param name="text">The message text.</param>
    public void Warn(string text) => this.Write(LogLevel.Warn, text, null);

    /// <summary>Logs a message at <see cref="LogLevel.Info"/> if the effective level allows it.</summary>
    /// <param name="text">The message text.</param>
    public void Info(string text) => this.Write(LogLevel.Info, text, null);

    /// <summary>Logs a message at <see cref="LogLevel.Debug"/> if the effective level allows it.</summary>
    /// <param name="text">The message text.</param>
    public void Debug(string text) => this.Write(LogLevel.Debug, text, null);

    private void Write(LogLevel level, string text, Exception? error)
    {
        if (level > this.Threshold)
        {
            return;
        }

        service.Export(new LogMessage(DateTimeOffset.Now, name, level, text, error));
    }
}
