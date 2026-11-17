// -----------------------------------------------------------------------
// <copyright file="ILogExporter.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// A sink for log entries, registered with <see cref="LoggerService.AddExporter"/>. Exceptions thrown
/// by an exporter are swallowed so that logging never breaks the caller.
/// </summary>
public interface ILogExporter
{
    /// <summary>Writes one log entry. Called synchronously on the logging thread.</summary>
    /// <param name="message">The entry to write.</param>
    void Export(LogMessage message);
}
