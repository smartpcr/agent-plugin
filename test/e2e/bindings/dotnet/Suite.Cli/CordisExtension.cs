// -----------------------------------------------------------------------
// <copyright file="CordisExtension.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.E2E;

using Bdd.Suite.Extensions;

/// <summary>The extension suiterun loads: the actions of test/e2e/schema/actions.catalog.yaml.</summary>
public sealed class CordisExtension : IExtension
{
    /// <summary>Gets the extension's name, as suites refer to it.</summary>
    public string Name => "cordis";

    /// <summary>Gets every action of the catalog.</summary>
    public IReadOnlyList<ActionRegistration> Actions => Cordis.E2E.Actions.All;

    /// <summary>Starts a run: each run keeps its own table of probe hosts.</summary>
    /// <param name="options">The run's options; the cordis actions take none.</param>
    /// <returns>The run state holding an empty host table.</returns>
    public RunState Start(IReadOnlyDictionary<string, string> options) => new() { State = new HostTable() };
}
