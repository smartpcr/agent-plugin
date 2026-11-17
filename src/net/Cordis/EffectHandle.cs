// -----------------------------------------------------------------------
// <copyright file="EffectHandle.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// A unit of resource ownership attached to a <see cref="Fiber"/>. Disposing it releases every
/// resource collected during execution, in reverse order, including nested effects.
/// </summary>
public sealed class EffectHandle : IAsyncDisposable
{
    private readonly List<Disposer> disposers = [];
    private readonly List<EffectHandle> children = [];
    private Task? pending;

    internal EffectHandle(Fiber fiber, string label)
    {
        this.Fiber = fiber;
        this.Label = label;
    }

    /// <summary>Gets the diagnostic label of this effect, such as <c>ctx.On(event)</c>.</summary>
    public string Label { get; }

    /// <summary>Gets the fiber that owns this effect.</summary>
    public Fiber Fiber { get; }

    /// <summary>Gets a value indicating whether this effect has not been disposed yet.</summary>
    public bool IsAlive { get; private set; } = true;

    /// <summary>Gets nested effects that were collected by this one.</summary>
    public IReadOnlyList<EffectHandle> Children => this.children;

    /// <summary>
    /// Hands ownership of this effect to an enclosing effect. When the returned disposer is
    /// collected by another effect, this one is detached from its fiber and released with its new owner.
    /// </summary>
    /// <returns>A <see cref="Disposer"/> that disposes this effect.</returns>
    public Disposer ToDisposer() => this.DisposeAsync;

    /// <summary>
    /// Releases this effect. Detaches it from its fiber, waits for any pending execution, then runs
    /// every collected disposer in reverse order. Disposer failures are logged, not thrown. Calling
    /// this more than once has no further effect.
    /// </summary>
    /// <returns>A task that completes when every disposer has run.</returns>
    public async ValueTask DisposeAsync()
    {
        Task? pending;
        lock (this.Fiber.Gate)
        {
            if (!this.IsAlive)
            {
                return;
            }

            this.IsAlive = false;
            pending = this.pending;
        }

        this.Fiber.Detach(this);

        if (pending is not null)
        {
            try
            {
                await pending.ConfigureAwait(false);
            }
            catch
            { /* already reported by the runner */
            }
        }

        Disposer[] disposers;
        lock (this.Fiber.Gate)
        {
            disposers = [.. this.disposers];
            this.disposers.Clear();
        }

        for (var i = disposers.Length - 1; i >= 0; i--)
        {
            try
            {
                await disposers[i]().ConfigureAwait(false);
            }
            catch (Exception error)
            {
                this.Fiber.Ctx.Logger.Error(error);
            }
        }
    }

    /// <inheritdoc/>
    public override string ToString() => $"Effect <{this.Label}>";

    internal void Collect(Disposer? disposer)
    {
        if (disposer is null)
        {
            return;
        }

        lock (this.Fiber.Gate)
        {
            if (!this.IsAlive)
            {
                _ = disposer();
                return;
            }

            this.disposers.Add(disposer);

            // A disposer that *is* another handle's DisposeAsync means the child was adopted:
            // detach it from the fiber so it is not disposed twice.
            if (disposer.Target is EffectHandle child && disposer.Method.Name == nameof(this.DisposeAsync))
            {
                this.Fiber.Detach(child);
                this.children.Add(child);
            }
        }
    }

    internal void SetPending(Task task) => this.pending = task;
}
