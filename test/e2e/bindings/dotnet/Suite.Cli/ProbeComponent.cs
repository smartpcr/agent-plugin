// -----------------------------------------------------------------------
// <copyright file="ProbeComponent.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.E2E;

/// <summary>The probe as a type, for the loader: an entry named <c>probe</c> resolves to it.</summary>
public sealed class ProbeComponent : IAsyncInit
{
    private readonly Context ctx;
    private readonly ProbeSpec spec;
    private readonly string id;

    /// <summary>Initializes a new instance of the <see cref="ProbeComponent"/> class, running the probe's body.</summary>
    /// <param name="ctx">The context of the probe's fiber.</param>
    /// <param name="config">The probe's validated config.</param>
    public ProbeComponent(Context ctx, ProbeSpec config)
    {
        this.ctx = ctx;
        this.spec = config;
        this.id = config.Id ?? ProbeHost.For(ctx).IdOf(ctx.Fiber);
        ProbeBody.Start(ctx, config, this.id, "probe");
    }

    /// <summary>Gets the schema the loader validates the probe's config with.</summary>
    public static IConfigSchema Schema => ProbeSchema.Instance;

    /// <summary>Runs the probe's asynchronous init: waits on its init gate, or fails on purpose.</summary>
    /// <returns>A task that completes when the init is done.</returns>
    public ValueTask InitAsync() => ProbeBody.InitAsync(this.ctx, this.spec, this.id);
}
