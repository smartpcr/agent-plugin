using System.Diagnostics;
using Timer = System.Threading.Timer;

namespace Cordis.Timers;

/// <summary>
/// Timers whose lifetime is bound to the <em>caller's</em> fiber, not to the timer plugin's.
/// This is what <see cref="IContextScoped"/> buys: <c>ctx.Get&lt;TimerService&gt;()</c> hands back a
/// clone bound to <c>ctx</c>, so every effect below lands on the caller.
/// </summary>
[ServiceName("timer")]
public sealed class TimerService(Context ctx) : Service(ctx, "timer")
{
    /// <summary>Stateless facade: safe to hand each caller its own bound clone.</summary>
    protected override bool ScopePerCaller => true;

    /// <summary>Run <paramref name="callback"/> once. Cancelled if the caller unloads first.</summary>
    public EffectHandle Timeout(Action callback, TimeSpan delay)
    {
        var slot = new EffectHandle[1];
        slot[0] = Ctx.Effect(() =>
        {
            var timer = new Timer(_ =>
            {
                EffectHandle? handle = Volatile.Read(ref slot[0]);
                // a tick already queued when the owner unloaded must not run
                if (handle is { IsAlive: false }) return;
                if (handle is not null) _ = handle.DisposeAsync();
                callback();
            }, null, delay, System.Threading.Timeout.InfiniteTimeSpan);
            return timer.Dispose;
        }, "ctx.Timeout()");
        return slot[0];
    }

    /// <summary>Repeat <paramref name="callback"/> until the returned effect or the fiber is disposed.</summary>
    public EffectHandle Interval(Action callback, TimeSpan period)
    {
        var slot = new EffectHandle[1];
        slot[0] = Ctx.Effect(() =>
        {
            var timer = new Timer(_ =>
            {
                if (Volatile.Read(ref slot[0]) is { IsAlive: false }) return;
                callback();
            }, null, period, period);
            return timer.Dispose;
        }, "ctx.Interval()");
        return slot[0];
    }

    /// <summary>Awaitable delay that faults if the caller's fiber unloads while waiting.</summary>
    public Task Delay(TimeSpan delay)
    {
        var completion = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        EffectHandle handle = Ctx.Effect(() =>
        {
            var timer = new Timer(_ => completion.TrySetResult(), null, delay, System.Threading.Timeout.InfiniteTimeSpan);
            return () =>
            {
                timer.Dispose();
                completion.TrySetException(new InactiveFiberException());
            };
        }, "ctx.Delay()");

        return completion.Task.ContinueWith(task =>
        {
            _ = handle.DisposeAsync();
            return task;
        }, TaskScheduler.Default).Unwrap();
    }

    /// <summary>Trailing-edge debounce. The returned delegate is inert once the fiber unloads.</summary>
    public Action<T> Debounce<T>(Action<T> callback, TimeSpan delay)
    {
        var schedule = new Schedule(Ctx, "ctx.Debounce()");
        return argument => schedule.Invoke(now => (delay, () => callback(argument)));
    }

    /// <summary>
    /// Throttle: the first call runs immediately; calls inside the window collapse into one
    /// trailing call at the end of the window, unless <paramref name="noTrailing"/> is set.
    /// The window is measured on a monotonic clock. The delegate is inert once the fiber unloads.
    /// </summary>
    public Action Throttle(Action callback, TimeSpan period, bool noTrailing = false)
    {
        var schedule = new Schedule(Ctx, "ctx.Throttle()");
        TimeSpan? lastCall = null;

        void Execute()
        {
            lastCall = schedule.Now;
            callback();
        }

        return () => schedule.Invoke(now =>
        {
            TimeSpan remaining = lastCall is null ? TimeSpan.Zero : period - (now - lastCall.Value);
            if (remaining <= TimeSpan.Zero) return (null, Execute);
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
        private readonly object _gate = new();
        private readonly Stopwatch _clock = Stopwatch.StartNew();
        private Timer? _pending;
        private long _generation;
        private bool _disposed;

        public Schedule(Context ctx, string label)
        {
            ctx.Effect(() => () =>
            {
                lock (_gate)
                {
                    _disposed = true;
                    _generation++;
                    _pending?.Dispose();
                    _pending = null;
                }
            }, label);
        }

        public TimeSpan Now => _clock.Elapsed;

        public void Invoke(Func<TimeSpan, (TimeSpan? Delay, Action? Run)> trigger)
        {
            Action? runNow = null;
            lock (_gate)
            {
                if (_disposed) return;
                _generation++;
                _pending?.Dispose();
                _pending = null;

                (TimeSpan? delay, Action? run) = trigger(Now);
                if (run is null) return;
                if (delay is null)
                {
                    runNow = run;
                }
                else
                {
                    long generation = _generation;
                    _pending = new Timer(_ => Fire(generation, run), null, delay.Value, System.Threading.Timeout.InfiniteTimeSpan);
                }
            }

            // Outside the lock: a re-entering callback may schedule a trailing call, and that
            // handle must survive this invocation returning.
            runNow?.Invoke();
        }

        private void Fire(long generation, Action run)
        {
            lock (_gate)
            {
                if (_disposed || generation != _generation) return;
                _pending?.Dispose();
                _pending = null;
            }
            run();
        }
    }
}
