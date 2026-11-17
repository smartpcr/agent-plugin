// -----------------------------------------------------------------------
// <copyright file="MetadataComponent.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.Tests;

/// <summary>
/// Test component that declares an <see cref="InjectAttribute"/> with configuration and records the
/// intercept configuration it observes for the <c>labelled</c> key.
/// </summary>
[Inject("labelled", Config = "from-inject")]
public sealed class MetadataComponent
{
    /// <summary>Initializes a new instance of the <see cref="MetadataComponent"/> class.</summary>
    /// <param name="ctx">The context whose intercept configuration for <c>labelled</c> is recorded in <see cref="Seen"/>.</param>
    public MetadataComponent(Context ctx) => Seen = ctx.GetInterceptConfig<string>("labelled");

    /// <summary>Gets or sets the intercept configuration observed by the most recently constructed instance.</summary>
    public static string? Seen { get; set; }
}
