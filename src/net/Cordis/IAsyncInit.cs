// -----------------------------------------------------------------------
// <copyright file="IAsyncInit.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>Post-construction hook. The analogue of cordis' <c>Service.init</c>.</summary>
public interface IAsyncInit
{
    /// <summary>
    /// Called once per load after a component instance is constructed, as part of the load; a
    /// failure fails the load.
    /// </summary>
    /// <returns>A task that completes when initialization has finished.</returns>
    ValueTask InitAsync();
}
