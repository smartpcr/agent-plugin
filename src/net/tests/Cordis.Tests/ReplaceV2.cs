// -----------------------------------------------------------------------
// <copyright file="ReplaceV2.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.Tests;

/// <summary>Test component that replaces <see cref="ReplaceV1"/> and keeps its configuration.</summary>
public sealed class ReplaceV2
{
    /// <summary>Initializes a new instance of the <see cref="ReplaceV2"/> class.</summary>
    /// <param name="ctx">The context the component is loaded into.</param>
    /// <param name="config">The configuration carried over from <see cref="ReplaceV1"/>.</param>
    public ReplaceV2(Context ctx, ReplaceV1.Options config)
    {
        Loads++;
        this.Text = config.Text;
    }

    /// <summary>Gets or sets the number of instances constructed.</summary>
    public static int Loads { get; set; }

    /// <summary>Gets the text received from the carried-over configuration.</summary>
    public string Text { get; }
}
