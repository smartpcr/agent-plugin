// -----------------------------------------------------------------------
// <copyright file="LogLevel.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// The severity of a log message, and the threshold of a logger. Levels are ordered from least to
/// most verbose; a logger writes a message when its level is at or below the threshold.
/// </summary>
public enum LogLevel
{
    /// <summary>As a threshold, writes nothing.</summary>
    Silent = 0,

    /// <summary>Failures that need attention.</summary>
    Error = 1,

    /// <summary>Unexpected conditions that do not stop the operation.</summary>
    Warn = 2,

    /// <summary>Normal operational messages. The default threshold.</summary>
    Info = 3,

    /// <summary>Detailed diagnostic messages.</summary>
    Debug = 4,
}
