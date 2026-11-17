// -----------------------------------------------------------------------
// <copyright file="Args.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.E2E;

using Bdd.Suite.Model;
using Bdd.Suite.Values;

/// <summary>Reads typed parameters from a step.</summary>
internal static class Args
{
    public static string? TextOrNull(OrderedMap map, string key)
        => map.Value(key) is { } value && Value.Text(value) is { Length: > 0 } text ? text : null;

    public static int? IntOrNull(OrderedMap map, string key)
        => map.Value(key) is { } value && Value.TryNumber(value, out var number) ? (int)number : null;

    public static bool Bool(OrderedMap map, string key, bool fallback) => map.Value(key) switch
    {
        bool flag => flag,
        string text when bool.TryParse(text, out var parsed) => parsed,
        _ => fallback,
    };

    public static List<string>? Strings(OrderedMap map, string key)
        => map.List(key)?.Select(Value.Text).ToList();

    /// <summary>A step value as plain dictionaries, lists and scalars, ready for JSON.</summary>
    public static object? Plain(object? value) => value switch
    {
        OrderedMap map => map.Keys.ToDictionary(key => key, key => Plain(map.Value(key)), StringComparer.Ordinal),
        List<object?> list => list.Select(Plain).ToList(),
        double number when number == Math.Floor(number) && Math.Abs(number) < long.MaxValue => (long)number,
        _ => value,
    };
}
