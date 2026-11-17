// -----------------------------------------------------------------------
// <copyright file="Disposer.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>A cleanup callback. The analogue of cordis' <c>Disposable</c>.</summary>
/// <returns>A task that completes when the resource has been released.</returns>
public delegate ValueTask Disposer();
