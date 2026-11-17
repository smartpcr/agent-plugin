// -----------------------------------------------------------------------
// <copyright file="ProbeSchema.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.E2E;

/// <summary>Rejects a config whose value is above its maxValue, as a component's schema would.</summary>
public sealed class ProbeSchema : IConfigSchema
{
    /// <summary>The one instance every probe shares.</summary>
    public static readonly ProbeSchema Instance = new();

    /// <summary>Gets the config type the schema validates.</summary>
    public Type ConfigType => typeof(ProbeSpec);

    /// <summary>Validates a probe config.</summary>
    /// <param name="input">The config as handed in; <c>null</c> stands for an empty spec.</param>
    /// <returns>The validated spec.</returns>
    /// <exception cref="ConfigValidationException">The input is not a spec, or its value is above its maximum.</exception>
    public object? Validate(object? input)
    {
        var spec = input switch
        {
            null => new ProbeSpec(),
            ProbeSpec typed => typed,
            _ => throw new ConfigValidationException([new ConfigIssue($"expected a probe spec, got {input.GetType().Name}")]),
        };
        if (spec.MaxValue is { } max && spec.Value > max)
        {
            throw new ConfigValidationException([new ConfigIssue($"value {spec.Value} is above the maximum {max}", "value")]);
        }

        return spec;
    }
}
