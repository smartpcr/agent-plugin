// -----------------------------------------------------------------------
// <copyright file="MemoryExporter.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>Collects messages in memory. Useful in tests.</summary>
public sealed class MemoryExporter : ILogExporter
{
    private readonly List<LogMessage> messages = [];

    /// <summary>Gets a snapshot of the collected messages, in the order they were exported.</summary>
    public IReadOnlyList<LogMessage> Messages
    {
        get
        {
            lock (this.messages)
            {
                return [.. this.messages];
            }
        }
    }

    /// <inheritdoc/>
    public void Export(LogMessage message)
    {
        lock (this.messages)
        {
            this.messages.Add(message);
        }
    }
}
