// -----------------------------------------------------------------------
// <copyright file="TraceLog.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.E2E;

/// <summary>The ordered record of what the probes saw.</summary>
internal sealed class TraceLog
{
    private readonly List<string> markers = [];

    public void Add(string marker)
    {
        lock (this.markers)
        {
            this.markers.Add(marker);
        }
    }

    public List<string> Snapshot()
    {
        lock (this.markers)
        {
            return [.. this.markers];
        }
    }
}
