// -----------------------------------------------------------------------
// <copyright file="ConfigValidationException.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// Thrown by an <see cref="IConfigSchema"/> when component config is invalid; the message lists
/// every issue.
/// </summary>
/// <param name="issues">The problems found.</param>
public sealed class ConfigValidationException(IReadOnlyList<ConfigIssue> issues)
    : CordisException("invalid config:\n" + string.Join('\n', issues))
{
    /// <summary>Gets the problems found.</summary>
    public IReadOnlyList<ConfigIssue> Issues { get; } = issues;
}
