// -----------------------------------------------------------------------
// <copyright file="ConfigIssue.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>One problem found while validating component config.</summary>
/// <param name="Message">A description of the problem.</param>
/// <param name="Path">The member or path of the offending value, or <see langword="null"/> if the problem concerns the whole config.</param>
public sealed record ConfigIssue(string Message, string? Path = null)
{
    /// <summary>Formats the issue as an indented bullet line, with its path when known.</summary>
    /// <returns>The formatted issue.</returns>
    public override string ToString() => this.Path is null ? $"  - {this.Message}" : $"  - {this.Message} (at {this.Path})";
}
