// -----------------------------------------------------------------------
// <copyright file="DelegateSchema{T}.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>Schema built from a delegate. Use for hand-rolled validation or normalisation.</summary>
/// <typeparam name="T">The normalised config type.</typeparam>
/// <param name="validate">Returns the normalised config, or throws <see cref="ConfigValidationException"/> when the input is invalid.</param>
public sealed class DelegateSchema<T>(Func<object?, T> validate) : IConfigSchema
{
    /// <inheritdoc/>
    public Type ConfigType => typeof(T);

    /// <summary>Validates and normalises the config by invoking the delegate.</summary>
    /// <param name="input">The original config.</param>
    /// <returns>The value returned by the delegate.</returns>
    public object? Validate(object? input) => validate(input);
}
