// -----------------------------------------------------------------------
// <copyright file="IComponent.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// An object-style component. Analogue of cordis' <c>{ name, inject, Config, apply }</c> object.
/// (Upstream renamed "plugin" to "component"; <c>ctx.Plugin()</c> remains as an obsolete alias.)
/// </summary>
public interface IComponent
{
    /// <summary>
    /// Gets the display name of the component. Defaults to <see langword="null"/>, which uses the
    /// implementing type's name.
    /// </summary>
    string? ComponentName => null;

    /// <summary>
    /// Gets the service names this component depends on, in addition to any
    /// <see cref="InjectAttribute"/> declarations on the implementing type. Defaults to none.
    /// </summary>
    IEnumerable<string> Inject => [];

    /// <summary>
    /// Gets the schema that validates the component's config on every load. Defaults to
    /// <see langword="null"/>, which passes config through unchanged.
    /// </summary>
    IConfigSchema? ConfigSchema => null;

    /// <summary>
    /// Loads the component into its own context. Effects and services created through
    /// <paramref name="ctx"/> are owned by the component's fiber and undone when it unloads.
    /// </summary>
    /// <param name="ctx">The component's context.</param>
    /// <returns>A task that completes when the component has finished loading.</returns>
    ValueTask ApplyAsync(Context ctx);
}
