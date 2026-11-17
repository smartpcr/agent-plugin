// -----------------------------------------------------------------------
// <copyright file="ProbeSpec.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.E2E;

using Bdd.Suite.Model;

/// <summary>
/// The probe component's config: what it provides, requires and acquires, and
/// where it waits or fails. The keys are documented in test/e2e/schema/actions.catalog.yaml.
/// A loader entry carries the same keys as its JSON config.
/// </summary>
public sealed class ProbeSpec
{
    /// <summary>Gets or sets the probe's id in the trace; a loader entry's id when unset.</summary>
    public string? Id { get; set; }

    /// <summary>Gets or sets the services the probe provides, each with a resource of its own.</summary>
    public List<string>? Provides { get; set; }

    /// <summary>Gets or sets the services the probe declares it needs.</summary>
    public List<string>? Inject { get; set; }

    /// <summary>Gets or sets the services the probe uses only while they are available, each in an inner scope.</summary>
    public List<string>? Optional { get; set; }

    /// <summary>Gets or sets the names of plain effects the probe acquires.</summary>
    public List<string>? Effects { get; set; }

    /// <summary>Gets or sets the gate the probe's init waits on, or <c>null</c> for an immediate init.</summary>
    public string? InitGate { get; set; }

    /// <summary>Gets or sets the gate the probe's cleanup waits on, or <c>null</c> for an immediate cleanup.</summary>
    public string? CleanupGate { get; set; }

    /// <summary>Gets or sets the service whose state the cleanup reads after its gate opens.</summary>
    public string? ReadOnCleanup { get; set; }

    /// <summary>Gets or sets where the probe fails on purpose: <c>body</c> or <c>init</c>.</summary>
    public string? Fail { get; set; }

    /// <summary>Gets or sets the value below which <see cref="Fail"/> applies; <c>null</c> to fail at any value.</summary>
    public int? FailWhileValueBelow { get; set; }

    /// <summary>Gets or sets a value indicating whether the probe accepts config updates in place.</summary>
    public bool AcceptUpdates { get; set; }

    /// <summary>Gets or sets the largest value the probe's schema accepts, or <c>null</c> for no limit.</summary>
    public int? MaxValue { get; set; }

    /// <summary>Gets or sets the probe's value, changed by updates.</summary>
    public int Value { get; set; }

    /// <summary>Gets or sets the child probes the probe loads in its body.</summary>
    public List<ProbeSpec>? Children { get; set; }

    /// <summary>Reads a spec from a step or from one item of a <c>children</c> list.</summary>
    /// <param name="map">The step, or one child item.</param>
    /// <returns>The spec the map describes.</returns>
    public static ProbeSpec FromMap(OrderedMap map) => new()
    {
        Id = Args.TextOrNull(map, "id"),
        Provides = Args.Strings(map, "provides"),
        Inject = Args.Strings(map, "inject"),
        Optional = Args.Strings(map, "optional"),
        Effects = Args.Strings(map, "effects"),
        InitGate = Args.TextOrNull(map, "initGate"),
        CleanupGate = Args.TextOrNull(map, "cleanupGate"),
        ReadOnCleanup = Args.TextOrNull(map, "readOnCleanup"),
        Fail = Args.TextOrNull(map, "fail"),
        FailWhileValueBelow = Args.IntOrNull(map, "failWhileValueBelow"),
        AcceptUpdates = Args.Bool(map, "acceptUpdates", false),
        MaxValue = Args.IntOrNull(map, "maxValue"),
        Value = Args.IntOrNull(map, "value") ?? 0,
        Children = map.List("children")?.OfType<OrderedMap>().Select(FromMap).ToList(),
    };

    /// <summary>Whether the probe fails at <paramref name="point"/> (body or init) with this config.</summary>
    /// <param name="point">The failure point: <c>body</c> or <c>init</c>.</param>
    /// <returns><c>true</c> when the probe fails there.</returns>
    public bool FailsAt(string point)
        => string.Equals(this.Fail, point, StringComparison.Ordinal)
        && (this.FailWhileValueBelow is not { } below || this.Value < below);

    /// <summary>A copy holding <paramref name="value"/>.</summary>
    /// <param name="value">The value of the copy.</param>
    /// <returns>A copy of this spec with <see cref="Value"/> set.</returns>
    public ProbeSpec WithValue(int value)
    {
        var copy = (ProbeSpec)this.MemberwiseClone();
        copy.Value = value;
        return copy;
    }
}
