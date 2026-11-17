// -----------------------------------------------------------------------
// <copyright file="ConsoleExporter.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// Writes log entries to the console as <c>HH:mm:ss [L] name text</c>, followed by the exception if
/// any. <see cref="LogLevel.Error"/> and <see cref="LogLevel.Warn"/> entries go to standard error;
/// the rest go to standard output. Registered by default on <see cref="LoggerService"/>.
/// </summary>
public sealed class ConsoleExporter : ILogExporter
{
    /// <inheritdoc/>
    public void Export(LogMessage message)
    {
        var writer = message.Level <= LogLevel.Warn ? Console.Error : Console.Out;
        writer.WriteLine($"{message.Timestamp:HH:mm:ss} [{message.Level.ToString()[0]}] {message.Name} {message.Text}");
        if (message.Error is not null)
        {
            writer.WriteLine(message.Error);
        }
    }
}
