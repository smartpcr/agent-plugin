using Cordis.Timers;

namespace Cordis.Tests;

public static class TimerTests
{
    public static void Register()
    {
        Harness.Case("throttle runs the first call and one trailing call per window", async () =>
        {
            var root = new Context();
            await root.Use<TimerService>();
            int calls = 0;
            Action? throttled = null;
            Fiber fiber = await root.Inject(["timer"], ctx =>
                throttled = ctx.Get<TimerService>().Throttle(() => Interlocked.Increment(ref calls), TimeSpan.FromMilliseconds(80)));

            throttled!();
            throttled!();
            throttled!();
            Assert.Equal(1, Volatile.Read(ref calls));
            await Assert.Eventually(() => Volatile.Read(ref calls) == 2, "trailing call never ran");
            await Task.Delay(120);
            Assert.Equal(2, Volatile.Read(ref calls));
            await fiber.DisposeAsync();
        });

        Harness.Case("throttle with noTrailing drops calls inside the window", async () =>
        {
            var root = new Context();
            await root.Use<TimerService>();
            int calls = 0;
            Action? throttled = null;
            await root.Inject(["timer"], ctx =>
                throttled = ctx.Get<TimerService>().Throttle(() => Interlocked.Increment(ref calls), TimeSpan.FromMilliseconds(50), noTrailing: true));

            throttled!();
            throttled!();
            await Task.Delay(100);
            Assert.Equal(1, Volatile.Read(ref calls));
        });

        Harness.Case("no trailing throttle or debounce call runs after the owner unloads (#161)", async () =>
        {
            var root = new Context();
            await root.Use<TimerService>();
            int calls = 0;
            Action? throttled = null;
            Action<int>? debounced = null;
            Fiber fiber = await root.Inject(["timer"], ctx =>
            {
                TimerService timer = ctx.Get<TimerService>();
                throttled = timer.Throttle(() => Interlocked.Increment(ref calls), TimeSpan.FromMilliseconds(40));
                debounced = timer.Debounce<int>(_ => Interlocked.Increment(ref calls), TimeSpan.FromMilliseconds(40));
            });

            throttled!();
            throttled!();   // schedules a trailing call
            debounced!(1);  // schedules a debounced call
            await fiber.DisposeAsync();
            await Task.Delay(100);
            Assert.Equal(1, Volatile.Read(ref calls));
            throttled!();
            Assert.Equal(1, Volatile.Read(ref calls));
        });

        Harness.Case("a re-entering throttled callback keeps its trailing call (#224)", async () =>
        {
            var root = new Context();
            await root.Use<TimerService>();
            int calls = 0;
            Action? throttled = null;
            await root.Inject(["timer"], ctx =>
                throttled = ctx.Get<TimerService>().Throttle(() =>
                {
                    if (Interlocked.Increment(ref calls) == 1) throttled!(); // re-enter inside the window
                }, TimeSpan.FromMilliseconds(40)));

            throttled!();
            await Assert.Eventually(() => Volatile.Read(ref calls) == 2, "re-entrant trailing call was lost");
        });
    }
}
