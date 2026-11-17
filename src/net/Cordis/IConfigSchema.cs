// -----------------------------------------------------------------------
// <copyright file="IConfigSchema.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// Validator/normaliser for plugin config. The analogue of cordis' Standard Schema hook:
/// synchronous only, returns the normalised value or throws <see cref="ConfigValidationException"/>.
/// </summary>
public interface IConfigSchema
{
    /// <summary>Gets the type of the normalised config.</summary>
    Type ConfigType { get; }

    /// <summary>
    /// Called on every load (and on update of a loaded fiber) with the original config. A thrown
    /// exception surfaces as a failed load.
    /// </summary>
    /// <param name="input">The original config, as passed to the use; may be <see langword="null"/>.</param>
    /// <returns>The normalised config passed to the component.</returns>
    /// <exception cref="ConfigValidationException">The config is invalid.</exception>
    object? Validate(object? input);
}
