// -----------------------------------------------------------------------
// <copyright file="EchoPlugin.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

using System.ComponentModel.DataAnnotations;
using Cordis;

/// <summary>
/// Test plugin that registers an effect which appends <c>+line</c> to <see cref="Log"/> when it starts
/// and <c>-line</c> when it is disposed, where <c>line</c> is <see cref="Options.Text"/> repeated
/// <see cref="Options.Times"/> times.
/// </summary>
/// <param name="ctx">The context the plugin is loaded into.</param>
/// <param name="config">The validated plugin configuration.</param>
public sealed class EchoPlugin(Context ctx, EchoPlugin.Options config)
{
    /// <summary>The configuration schema that validates <see cref="Options"/> through data annotations.</summary>
    public static readonly IConfigSchema Schema = DataAnnotationsSchema<Options>.Instance;

    /// <summary>The shared log of effect start (<c>+</c>) and dispose (<c>-</c>) entries.</summary>
    public static readonly List<string> Log = [];

    private readonly EffectHandle handle = ctx.Effect(
        () =>
        {
            var line = string.Join(' ', Enumerable.Repeat(config.Text, config.Times));
            Log.Add($"+{line}");
            return () => Log.Add($"-{line}");
        },
        "echo");

    /// <summary>Configuration for <see cref="EchoPlugin"/>.</summary>
    public sealed class Options
    {
        /// <summary>Gets or sets how many times <see cref="Text"/> is repeated; must be between 1 and 10.</summary>
        [Range(1, 10)]
        public int Times { get; set; } = 1;

        /// <summary>Gets or sets the text to echo.</summary>
        public string Text { get; set; } = string.Empty;
    }
}
