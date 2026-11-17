// -----------------------------------------------------------------------
// <copyright file="DataAnnotationsSchema{T}.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

using System.ComponentModel.DataAnnotations;

/// <summary>Validates a POCO with <c>System.ComponentModel.DataAnnotations</c> attributes.</summary>
/// <typeparam name="T">The config type; a missing config is replaced by a new instance.</typeparam>
public sealed class DataAnnotationsSchema<T> : IConfigSchema
    where T : class, new()
{
    /// <summary>The shared schema instance for <typeparamref name="T"/>.</summary>
    public static readonly DataAnnotationsSchema<T> Instance = new();

    /// <inheritdoc/>
    public Type ConfigType => typeof(T);

    /// <summary>
    /// Validates every property of the config against its data annotation attributes.
    /// A <see langword="null"/> input is replaced by a new <typeparamref name="T"/> before validation.
    /// </summary>
    /// <param name="input">The original config.</param>
    /// <returns>The validated <typeparamref name="T"/> instance.</returns>
    /// <exception cref="ConfigValidationException">The input is not a <typeparamref name="T"/>, or fails validation.</exception>
    public object? Validate(object? input)
    {
        var value = input switch
        {
            null => new T(),
            T typed => typed,
            _ => throw new ConfigValidationException([new ConfigIssue($"expected {typeof(T).Name}, got {input.GetType().Name}")]),
        };

        var results = new List<ValidationResult>();
        if (Validator.TryValidateObject(value, new ValidationContext(value), results, validateAllProperties: true))
        {
            return value;
        }

        var issues = results
            .Select(r => new ConfigIssue(r.ErrorMessage ?? "invalid value", r.MemberNames.FirstOrDefault()))
            .ToArray();
        throw new ConfigValidationException(issues);
    }
}
