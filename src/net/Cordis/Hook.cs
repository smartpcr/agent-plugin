// -----------------------------------------------------------------------
// <copyright file="Hook.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>One registered event listener.</summary>
/// <param name="ctx">The context the listener was registered from.</param>
/// <param name="callback">The listener delegate.</param>
/// <param name="global">Whether the listener ignores the dispatch filter.</param>
internal sealed class Hook(Context ctx, Delegate callback, bool global)
{
    private volatile bool removed;

    /// <summary>Gets the context the listener was registered from.</summary>
    public Context Ctx { get; } = ctx;

    /// <summary>Gets the listener delegate.</summary>
    public Delegate Callback { get; } = callback;

    /// <summary>Gets a value indicating whether the listener ignores the dispatch filter.</summary>
    public bool Global { get; } = global;

    /// <summary>
    /// Gets or sets a value indicating whether the listener was unregistered. A dispatch that
    /// already took a snapshot skips a removed hook.
    /// </summary>
    public bool Removed
    {
        get => this.removed;
        set => this.removed = value;
    }
}
