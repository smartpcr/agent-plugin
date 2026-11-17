// -----------------------------------------------------------------------
// <copyright file="HostTable.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.E2E;

using System.Collections.Concurrent;
using Bdd.Suite.Actions;

/// <summary>The probe host of each running case, keyed by suite and case (rows included).</summary>
internal sealed class HostTable
{
    private readonly ConcurrentDictionary<string, ProbeHost> hosts = new(StringComparer.Ordinal);

    public static string KeyOf(ActionRequest request)
        => request.Ref is { } step ? $"{step.Suite}\u0001{step.Case}" : "default";

    public void Put(ActionRequest request, ProbeHost host) => this.hosts[KeyOf(request)] = host;

    public ProbeHost Get(ActionRequest request)
        => this.hosts.TryGetValue(KeyOf(request), out var host) ? host : throw ActionException.Failed("no host: run host.start first");

    public ProbeHost? Take(ActionRequest request) => this.hosts.TryRemove(KeyOf(request), out var host) ? host : null;
}
