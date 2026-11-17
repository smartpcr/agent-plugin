// -----------------------------------------------------------------------
// <copyright file="Disposer.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>A cleanup callback. The analogue of cordis' <c>Disposable</c>.</summary>
/// <returns>A task that completes when the resource has been released.</returns>
public delegate ValueTask Disposer();
