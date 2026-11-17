// -----------------------------------------------------------------------
// <copyright file="ValueComponent.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.Tests;

/// <summary>Test component that reports its configured value to <see cref="OnApply"/> each time it is constructed.</summary>
public sealed class ValueComponent
{
    /// <summary>Initializes a new instance of the <see cref="ValueComponent"/> class.</summary>
    /// <param name="ctx">The context the component is loaded into.</param>
    /// <param name="config">The configuration whose value is passed to <see cref="OnApply"/>.</param>
    public ValueComponent(Context ctx, Options config) => OnApply?.Invoke(config.Value);

    /// <summary>Gets or sets the optional callback invoked with <see cref="Options.Value"/> when an instance is constructed.</summary>
    public static Action<int>? OnApply { get; set; }

    /// <summary>Configuration for <see cref="ValueComponent"/>.</summary>
    public sealed class Options
    {
        /// <summary>Gets or sets the configured value.</summary>
        public int Value { get; set; }
    }
}
