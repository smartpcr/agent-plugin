// -----------------------------------------------------------------------
// <copyright file="IContextScoped.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// Opt-in caller attribution. Replaces cordis' traceable <c>Proxy</c>: a service returns a
/// shallow clone bound to the calling context so effects it creates are owned by the caller.
/// </summary>
public interface IContextScoped
{
    /// <summary>
    /// Returns the view of this service to hand to a caller. Called by service resolution with
    /// the context making the access.
    /// </summary>
    /// <param name="ctx">The calling context.</param>
    /// <returns>An object bound to <paramref name="ctx"/>, or this instance if no rebinding is needed.</returns>
    object ForContext(Context ctx);
}
