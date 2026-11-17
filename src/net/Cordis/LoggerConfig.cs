// -----------------------------------------------------------------------
// <copyright file="LoggerConfig.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// Intercept configuration for the <c>logger</c> service. Set on a context to change the level of
/// every <see cref="Logger"/> bound to that context and its descendants.
/// </summary>
public sealed class LoggerConfig
{
    /// <summary>Gets or sets the most verbose level that is written. Defaults to <see cref="LogLevel.Info"/>.</summary>
    public LogLevel Level { get; set; } = LogLevel.Info;
}
