// -----------------------------------------------------------------------
// <copyright file="ReplaceV1.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.Tests;

/// <summary>Test component that is replaced by <see cref="ReplaceV2"/> in the registry hot-replace tests.</summary>
public sealed class ReplaceV1
{
    /// <summary>Initializes a new instance of the <see cref="ReplaceV1"/> class.</summary>
    /// <param name="ctx">The context the component is loaded into.</param>
    /// <param name="config">The component configuration.</param>
    public ReplaceV1(Context ctx, Options config) => Loads++;

    /// <summary>Gets or sets the number of instances constructed.</summary>
    public static int Loads { get; set; }

    /// <summary>Configuration shared by <see cref="ReplaceV1"/> and <see cref="ReplaceV2"/>.</summary>
    public sealed class Options
    {
        /// <summary>Gets or sets the text carried over when the component is replaced.</summary>
        public string Text { get; set; } = string.Empty;
    }
}
