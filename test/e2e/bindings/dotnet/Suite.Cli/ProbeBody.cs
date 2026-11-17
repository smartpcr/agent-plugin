// -----------------------------------------------------------------------
// <copyright file="ProbeBody.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.E2E;

/// <summary>
/// The probe's body, shared by the type-based component the loader resolves and the
/// instance-based ones <c>component.use</c> creates. The order is the contract the suites assert:
/// load, lifecycle, cleanup gate, provided services, effects, body failure, update hook,
/// optional scopes, children, then the asynchronous init.
/// </summary>
internal static class ProbeBody
{
    public static void Start(Context ctx, ProbeSpec spec, string id, string implementation)
    {
        var host = ProbeHost.For(ctx);
        host.Trace.Add($"{id}:load");
        host.CountLoad(id);

        ctx.Effect(() => () => host.Trace.Add($"{id}:unload"), "probe/lifecycle");

        if (spec.CleanupGate is { } cleanupGate)
        {
            ctx.EffectAsync(
                () => new ValueTask<Disposer?>(async () =>
                {
                    host.Trace.Add($"{id}:cleanup-start");
                    await host.Gate(cleanupGate).ConfigureAwait(false);
                    if (spec.ReadOnCleanup is { } service)
                    {
                        host.Trace.Add($"{id}:read {service}={ReadState(ctx, service)}");
                    }

                    host.Trace.Add($"{id}:cleanup-done");
                }),
                "probe/cleanup");
        }

        foreach (var service in spec.Provides ?? [])
        {
            var handle = new ProbeHandle(id);
            ctx.Provide(service, handle);
            ctx.Effect(
                () =>
                {
                    host.Trace.Add($"{id}:+{service}.resource");
                    return () =>
                    {
                        handle.Closed = true;
                        host.Trace.Add($"{id}:-{service}.resource");
                    };
                },
                $"probe/{service}.resource");
        }

        foreach (var effect in spec.Effects ?? [])
        {
            ctx.Effect(
                () =>
                {
                    host.Trace.Add($"{id}:+{effect}");
                    return () => host.Trace.Add($"{id}:-{effect}");
                },
                $"probe/{effect}");
        }

        if (spec.FailsAt("body"))
        {
            throw new InvalidOperationException($"{id}: the body failed on purpose");
        }

        if (spec.AcceptUpdates)
        {
            ctx.OnUpdate<ProbeSpec>(next => host.Trace.Add($"{id}:accepted {next.Value}"));
        }

        foreach (var service in spec.Optional ?? [])
        {
            ctx.Inject([service], scope =>
            {
                host.Trace.Add($"{id}:optional+{service}");
                scope.Effect(() => () => host.Trace.Add($"{id}:optional-{service}"), $"probe/optional {service}");
            });
        }

        foreach (var child in spec.Children ?? [])
        {
            var childId = child.Id ?? throw new InvalidOperationException($"{id}: a child spec needs an id");
            var fiber = ctx.Use(ComponentSource.ForInstance(host.Implementation(implementation, null)), child, child.Inject);
            host.Register(childId, fiber);
        }
    }

    public static async ValueTask InitAsync(Context ctx, ProbeSpec spec, string id)
    {
        if (spec.InitGate is null && !spec.FailsAt("init"))
        {
            return;
        }

        var host = ProbeHost.For(ctx);
        host.Trace.Add($"{id}:init-start");
        if (spec.InitGate is { } gate)
        {
            await host.Gate(gate).ConfigureAwait(false);
        }

        if (spec.FailsAt("init"))
        {
            throw new InvalidOperationException($"{id}: the init failed on purpose");
        }

        host.Trace.Add($"{id}:init-done");
    }

    private static string ReadState(Context ctx, string service)
    {
        try
        {
            return ctx.Get(service, required: false) switch
            {
                ProbeHandle handle => handle.Closed ? "closed" : "open",
                null => "missing",
                _ => "open",
            };
        }
        catch (CordisException)
        {
            return "missing";
        }
    }
}
