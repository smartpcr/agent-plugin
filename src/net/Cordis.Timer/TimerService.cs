// -----------------------------------------------------------------------
// <copyright file="TimerService.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.Timers;

using System.Diagnostics;
using Timer = System.Threading.Timer;

/// <summary>
/// Timers whose lifetime is bound to the <em>caller's</em> fiber, not to the timer plugin's.
/// This is what <see cref="IContextScoped"/> buys: <c>ctx.Get&lt;TimerService&gt;()</c> hands back a
/// clone bound to <c>ctx</c>, so every effect below lands on the caller.
/// </summary>
/// <remarks>
/// Every timer is registered as an effect of the calling fiber. When that fiber unloads, pending
/// timers are disposed, ticks already queued are discarded, and the delegates returned by
/// <see cref="Debounce{T}(Action{T}, TimeSpan)"/> and <see cref="Throttle(Action, TimeSpan, bool)"/>
/// become inert. Throttle windows are measured on a monotonic clock, so wall-clock changes do not
/// affect them.
/// </remarks>
/// <param name="ctx">The context the service is bound to; per-caller clones are bound to the caller's context.</param>
[ServiceName("timer")]
public sealed class TimerService(Context ctx) : Service(ctx, "timer")
{
    /// <summary>
    /// Gets a value indicating whether each caller receives its own clone bound to its context.
    /// Always <c>true</c>: the service is a stateless facade, so per-caller clones are safe.
    /// </summary>
    protected override bool ScopePerCaller => true;

    /// <summary>
    /// Runs <paramref name="callback"/> once after <paramref name="delay"/>. Cancelled if the
    /// returned effect is disposed or the caller's fiber unloads first.
    /// </summary>
    /// <param name="callback">The action to run when the delay elapses.</param>
    /// <param name="delay">The time to wait before running <paramref name="callback"/>.</param>
    /// <returns>
    /// The effect that owns the timer; disposing it cancels the pending call. The effect disposes
    /// itself just before the callback runs.
    /// </returns>
    public EffectHandle Timeout(Action callback, TimeSpan delay)
    {
        var slot = new EffectHandle[1];
        slot[0] = this.Ctx.Effect(
            () =>
            {
                var timer = new Timer(
    _ =>
                {
                    var handle = Volatile.Read(ref slot[0]);

                    // a tick already queued when the owner unloaded must not run
                    if (handle is { IsAlive: false })
                    {
                        return;
                    }

                    if (handle is not null)
                    {
                        _ = handle.DisposeAsync();
                    }

                    callback();
                },
    null,
    delay,
    System.Threading.Timeout.InfiniteTimeSpan);
                return timer.Dispose;
            },
            "ctx.Timeout()");
        return slot[0];
    }

    /// <summary>
    /// Runs <paramref name="callback"/> every <paramref name="period"/>, starting one period from
    /// now, until the returned effect or the caller's fiber is disposed.
    /// </summary>
    /// <param name="callback">The action to run on each tick.</param>
    /// <param name="period">The time between ticks, also used as the initial delay.</param>
    /// <returns>The effect that owns the timer; disposing it stops further ticks.</returns>
    public EffectHandle Interval(Action callback, TimeSpan period)
    {
        var slot = new EffectHandle[1];
        slot[0] = this.Ctx.Effect(
            () =>
            {
                var timer = new Timer(
    _ =>
                {
                    if (Volatile.Read(ref slot[0]) is { IsAlive: false })
                    {
                        return;
                    }

                    callback();
                },
    null,
    period,
    period);
                return timer.Dispose;
            },
            "ctx.Interval()");
        return slot[0];
    }

    /// <summary>Awaitable delay that faults if the caller's fiber unloads while waiting.</summary>
    /// <param name="delay">The time to wait.</param>
    /// <returns>
    /// A task that completes when <paramref name="delay"/> elapses, or faults with
    /// <see cref="InactiveFiberException"/> if the caller's fiber unloads first.
    /// </returns>
    public Task Delay(TimeSpan delay)
    {
        var completion = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var handle = this.Ctx.Effect(
            () =>
            {
                var timer = new Timer(_ => completion.TrySetResult(), null, delay, System.Threading.Timeout.InfiniteTimeSpan);
                return () =>
                {
                    timer.Dispose();
                    completion.TrySetException(new InactiveFiberException());
                };
            },
            "ctx.Delay()");

        return completion.Task.ContinueWith(
            task =>
            {
                _ = handle.DisposeAsync();
                return task;
            },
            TaskScheduler.Default).Unwrap();
    }

    /// <summary>
    /// Trailing-edge debounce: each call restarts the wait, and <paramref name="callback"/> runs
    /// once with the latest argument after <paramref name="delay"/> passes without a new call.
    /// The returned delegate is inert once the caller's fiber unloads.
    /// </summary>
    /// <typeparam name="T">The type of the argument passed to <paramref name="callback"/>.</typeparam>
    /// <param name="callback">The action to run after the calls settle.</param>
    /// <param name="delay">The quiet period that must pass after the last call.</param>
    /// <returns>The debounced delegate.</returns>
    public Action<T> Debounce<T>(Action<T> callback, TimeSpan delay)
    {
        var schedule = new Schedule(this.Ctx, "ctx.Debounce()");
        return argument => schedule.Invoke(now => (delay, () => callback(argument)));
    }

    /// <summary>
    /// Throttle: the first call runs immediately; calls inside the window collapse into one
    /// trailing call at the end of the window, unless <paramref name="noTrailing"/> is set.
    /// The window is measured on a monotonic clock. The delegate is inert once the fiber unloads.
    /// </summary>
    /// <param name="callback">The action to rate-limit.</param>
    /// <param name="period">The minimum time between two runs of <paramref name="callback"/>.</param>
    /// <param name="noTrailing">
    /// <c>true</c> to drop calls made inside the window instead of running one trailing call.
    /// </param>
    /// <returns>The throttled delegate.</returns>
    public Action Throttle(Action callback, TimeSpan period, bool noTrailing = false)
    {
        var schedule = new Schedule(this.Ctx, "ctx.Throttle()");
        TimeSpan? lastCall = null;

        void Execute()
        {
            lastCall = schedule.Now;
            callback();
        }

        return () => schedule.Invoke(now =>
        {
            var remaining = lastCall is null ? TimeSpan.Zero : period - (now - lastCall.Value);
            if (remaining <= TimeSpan.Zero)
            {
                return (null, Execute);
            }

            return noTrailing ? (null, null) : (remaining, Execute);
        });
    }

    /// <summary>
    /// Shared state of a debounced or throttled delegate. Each call cancels the pending trailing
    /// call, then <c>trigger</c> decides whether to run now (<c>delay</c> null) or later. Timer
    /// ticks that lost a race with a newer call or with disposal are discarded by generation.
    /// </summary>
    private sealed class Schedule
    {
        private readonly object gate = new();
        private readonly Stopwatch clock = Stopwatch.StartNew();
        private Timer? pending;
        private long generation;
        private bool disposed;

        public Schedule(Context ctx, string label)
        {
            ctx.Effect(
                () => () =>
                {
                    lock (this.gate)
                    {
                        this.disposed = true;
                        this.generation++;
                        this.pending?.Dispose();
                        this.pending = null;
                    }
                },
                label);
        }

        public TimeSpan Now => this.clock.Elapsed;

        public void Invoke(Func<TimeSpan, (TimeSpan? Delay, Action? Run)> trigger)
        {
            Action? runNow = null;
            lock (this.gate)
            {
                if (this.disposed)
                {
                    return;
                }

                this.generation++;
                this.pending?.Dispose();
                this.pending = null;

                (var delay, var run) = trigger(this.Now);
                if (run is null)
                {
                    return;
                }

                if (delay is null)
                {
                    runNow = run;
                }
                else
                {
                    var generation = this.generation;
                    this.pending = new Timer(_ => this.Fire(generation, run), null, delay.Value, System.Threading.Timeout.InfiniteTimeSpan);
                }
            }

            // Outside the lock: a re-entering callback may schedule a trailing call, and that
            // handle must survive this invocation returning.
            runNow?.Invoke();
        }

        private void Fire(long generation, Action run)
        {
            lock (this.gate)
            {
                if (this.disposed || generation != this.generation)
                {
                    return;
                }

                this.pending?.Dispose();
                this.pending = null;
            }

            run();
        }
    }
}
