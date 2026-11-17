// -----------------------------------------------------------------------
// <copyright file="TestEvents.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

using Cordis;

/// <summary>Typed event keys shared by the event tests.</summary>
public static class TestEvents
{
    /// <summary>A plain event that carries an integer payload.</summary>
    public static readonly EventKey<int> Tick = new("tick");

    /// <summary>A bail event that takes a string and stops at the first listener returning a string result.</summary>
    public static readonly BailKey<string, string> Resolve = new("resolve");

    /// <summary>A waterfall event that passes a string through each listener in turn.</summary>
    public static readonly WaterfallKey<string> Transform = new("transform");
}
