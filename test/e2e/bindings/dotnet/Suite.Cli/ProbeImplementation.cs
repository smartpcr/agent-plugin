// -----------------------------------------------------------------------
// <copyright file="ProbeImplementation.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.E2E;

/// <summary>
/// One probe implementation, as an object component: its name is its identity, so
/// <c>component.replace</c> can swap one for another, and the <c>inject</c> it is built
/// with is the component's own declaration.
/// </summary>
internal sealed class ProbeImplementation(string name, IReadOnlyList<string> inject) : IComponent<ProbeSpec>
{
    public string ComponentName => name;

    public IEnumerable<string> Inject => inject;

    public IConfigSchema ConfigSchema => ProbeSchema.Instance;

    public async ValueTask ApplyAsync(Context ctx, ProbeSpec config)
    {
        var id = config.Id ?? ProbeHost.For(ctx).IdOf(ctx.Fiber);
        ProbeBody.Start(ctx, config, id, name);
        await ProbeBody.InitAsync(ctx, config, id).ConfigureAwait(false);
    }
}
