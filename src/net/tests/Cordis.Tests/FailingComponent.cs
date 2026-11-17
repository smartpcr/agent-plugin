// -----------------------------------------------------------------------
// <copyright file="FailingComponent.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.Tests;

/// <summary>Test component whose constructor always throws, used to exercise failed-fiber behaviour.</summary>
public sealed class FailingComponent
{
    /// <summary>Initializes a new instance of the <see cref="FailingComponent"/> class.</summary>
    /// <param name="ctx">The context the component is loaded into.</param>
    /// <exception cref="InvalidOperationException">Always thrown.</exception>
    public FailingComponent(Context ctx) => throw new InvalidOperationException("failed");
}
