// -----------------------------------------------------------------------
// <copyright file="IComponent{TConfig}.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// An object-style component that receives typed config. When config is supplied, the typed
/// <see cref="ApplyAsync(Context, TConfig)"/> overload is called with the schema-validated value;
/// without config, <see cref="IComponent.ApplyAsync(Context)"/> forwards to it with the default value.
/// </summary>
/// <typeparam name="TConfig">The type of the component's config.</typeparam>
public interface IComponent<in TConfig> : IComponent
{
    /// <summary>
    /// Loads the component into its own context with its config.
    /// </summary>
    /// <param name="ctx">The component's context.</param>
    /// <param name="config">The validated config of this use.</param>
    /// <returns>A task that completes when the component has finished loading.</returns>
    ValueTask ApplyAsync(Context ctx, TConfig config);

    /// <inheritdoc/>
    ValueTask IComponent.ApplyAsync(Context ctx) => this.ApplyAsync(ctx, default!);
}
