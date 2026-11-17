// -----------------------------------------------------------------------
// <copyright file="CoreTests.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.Tests;

/// <summary>Ports of the upstream core behaviour added since the initial port (see README "Upstream sync").</summary>
public static class CoreTests
{
    /// <summary>Registers the core behaviour test cases with the <see cref="Harness"/>.</summary>
    public static void Register()
    {
        // ------------------------------------------------------------ fiber lifecycle
        Harness.Case("failed fiber does not reload when a provider changes (#98)", async () =>
        {
            (var root, var _) = Quiet();
            var loads = 0;
            var fiber = root.Inject(["thing"], _ =>
            {
                loads++;
                throw new InvalidOperationException("boom");
            });
            var first = root.Provide("thing", 1);
            await Assert.ThrowsAsync<InvalidOperationException>(() => fiber.WaitAsync());
            Assert.Equal(FiberState.Failed, fiber.State);

            await first.DisposeAsync();
            root.Provide("thing", 2);
            await Task.Delay(20);
            Assert.Equal(1, loads);
            Assert.Equal(FiberState.Failed, fiber.State);
        });

        Harness.Case("restart while loading is not lost (#189)", async () =>
        {
            var root = new Context();
            var started = Gate();
            var release = Gate();
            var applied = 0;
            var fiber = root.ApplyAsync("p", async _ =>
            {
                if (Interlocked.Increment(ref applied) == 1)
                {
                    started.SetResult();
                    await release.Task;
                }
            });

            await started.Task;
            var restart = fiber.RestartAsync();
            release.SetResult();
            await restart;
            Assert.Equal(2, applied);
            Assert.Equal(FiberState.Active, fiber.State);
        });

        Harness.Case("concurrent updates settle on the latest config", async () =>
        {
            var root = new Context();
            GatedValue.Reset();
            var fiber = await root.Use<GatedValue>(new GatedValue.Options { Value = 0 });

            var first = fiber.UpdateAsync(new GatedValue.Options { Value = 1 });
            await GatedValue.Started.Task;
            var second = fiber.UpdateAsync(new GatedValue.Options { Value = 2 });
            var third = fiber.UpdateAsync(new GatedValue.Options { Value = 3 });
            GatedValue.Release.SetResult();
            await Task.WhenAll(first, second, third);

            Assert.Equal("0,1,3", string.Join(',', GatedValue.Applied));
            Assert.Equal("0,1", string.Join(',', GatedValue.Disposed));
            Assert.Equal(3, ((GatedValue.Options)fiber.Config!).Value);
            Assert.Equal(FiberState.Active, fiber.State);
            await root.DisposeAsync();
            Assert.Equal("0,1,3", string.Join(',', GatedValue.Disposed));
        });

        Harness.Case("superseded load failure does not fail the latest update", async () =>
        {
            (var root, var logs) = Quiet();
            var started = Gate();
            var release = Gate();
            var calls = 0;
            var fiber = root.ApplyAsync("p", async _ =>
            {
                if (Interlocked.Increment(ref calls) != 1)
                {
                    return;
                }

                started.SetResult();
                await release.Task;
                throw new InvalidOperationException("superseded load");
            });

            await started.Task;
            var updating = fiber.UpdateAsync(null);
            release.SetResult();
            await updating;
            Assert.Equal(FiberState.Active, fiber.State);
            Assert.Equal(1, logs.Messages.Count(m => m.Level == LogLevel.Error));
        });

        Harness.Case("update hook accepts a config in place and dies with the fiber (#56)", async () =>
        {
            var root = new Context();
            var applied = 0;
            var received = new List<int>();
            var fiber = await root.Use<ValueComponent>(new ValueComponent.Options { Value = 1 });
            ValueComponent.OnApply = _ => applied++;
            fiber.Ctx.OnUpdate<ValueComponent.Options>(options => received.Add(options.Value));

            await fiber.UpdateAsync(new ValueComponent.Options { Value = 2 });
            Assert.Equal(0, applied);
            Assert.Equal("2", string.Join(',', received));
            Assert.Equal(2, ((ValueComponent.Options)fiber.Config!).Value);

            // a hook calling next() lets the restart happen; the reload drops the old hook
            await fiber.RestartAsync();
            Assert.Equal(1, applied);
            await fiber.UpdateAsync(new ValueComponent.Options { Value = 3 });
            Assert.Equal(2, applied);
            Assert.Equal("2", string.Join(',', received));
        });

        Harness.Case("update validates a loaded fiber up front and commits the original config", async () =>
        {
            var root = new Context();
            var committed = new List<object?>();
            root.Internal.Committing += (_, config) => committed.Add(config);
            var fiber = await root.Use<EchoPlugin>(new EchoPlugin.Options { Times = 1, Text = "a" });

            var invalid = new EchoPlugin.Options { Times = 99 };
            await Assert.ThrowsAsync<ConfigValidationException>(() => fiber.UpdateAsync(invalid));
            Assert.Equal(1, ((EchoPlugin.Options)fiber.Config!).Times);
            Assert.Equal(0, committed.Count);

            var valid = new EchoPlugin.Options { Times = 2, Text = "a" };
            await fiber.UpdateAsync(valid);
            Assert.True(ReferenceEquals(committed.Single(), valid), "commit did not carry the original config");

            await fiber.UpdateAsync(new EchoPlugin.Options { Times = 3, Text = "a" }, noSave: true);
            Assert.Equal(1, committed.Count);
        });

        Harness.Case("disposed fiber settles to Disposed and reports it (#133)", async () =>
        {
            (var root, var _) = Quiet();
            var states = new List<FiberState>();
            var fiber = root.Apply("p", _ => throw new InvalidOperationException("boom"));
            try
            {
                await fiber;
            }
            catch (InvalidOperationException)
            {
            }

            root.Internal.StatusChanged += (changed, _) =>
            {
                if (ReferenceEquals(changed, fiber))
                {
                    states.Add(changed.State);
                }
            };

            await fiber.DisposeAsync();
            Assert.Equal(FiberState.Disposed, fiber.State);
            Assert.Equal(FiberState.Disposed, states.Last());
        });

        Harness.Case("generator effect stops advancing once disposed and runs its finally (#268)", async () =>
        {
            var root = new Context();
            var release = Gate();
            var events = new List<string>();
            EffectHandle? handle = null;

            // the generator keeps the fiber loading until it finishes, so do not await the fiber
            var fiber = root.Apply("p", ctx =>
            {
                handle = ctx.EffectMany(Steps);

                async IAsyncEnumerable<Disposer> Steps()
                {
                    try
                    {
                        yield return () =>
                        {
                            events.Add("dispose 1");
                            return default;
                        };
                        await release.Task;
                        yield return () =>
                        {
                            events.Add("dispose 2");
                            return default;
                        };
                        events.Add("outdated continuation");
                    }
                    finally
                    {
                        events.Add("finally");
                    }
                }
            });

            await Assert.Eventually(() => Volatile.Read(ref handle) is not null, "effect never started");
            var disposing = handle!.DisposeAsync().AsTask();
            release.SetResult();
            await disposing;
            Assert.Equal("dispose 2,finally,dispose 1", string.Join(',', events));
            await fiber.DisposeAsync();
        });

        // ---------------------------------------------------------- service disposal (#196)
        Harness.Case("provider effects outlive a dependent's async cleanup", async () =>
        {
            var root = new Context();
            var gate = Gate();
            var timeline = new List<string>();
            var provider = await root.Apply("provider", ctx =>
            {
                var closed = false;
                ctx.Provide("foo", new Action(() =>
                {
                    lock (timeline)
                    {
                        timeline.Add($"read: {(closed ? "closed" : "open")}");
                    }
                }));
                ctx.Effect(() => () =>
                {
                    closed = true;
                    lock (timeline)
                    {
                        timeline.Add("provider closed");
                    }
                });
            });
            await root.Inject(["foo"], ctx =>
            {
                var read = (Action)ctx.Get("foo")!;
                ctx.EffectAsync(() => new ValueTask<Disposer?>(async () =>
                {
                    await gate.Task;
                    read();
                }));
            });

            var task = provider.DisposeAsync().AsTask();
            await Task.Delay(30);
            var closedDuringDrain = timeline.Contains("provider closed");
            gate.SetResult();
            await task;
            Assert.True(!closedDuringDrain, "provider closed before its dependent finished");
            Assert.Equal("read: open,provider closed", string.Join(',', timeline));
        });

        Harness.Case("withdrawing a service waits for a disposing consumer", async () =>
        {
            var root = new Context();
            var gate = Gate();
            var timeline = new List<string>();
            var provider = await root.Apply("provider", _ => { });
            var withdraw = provider.Ctx.Provide("foo", new object());
            var consumer = await root.Inject(["foo"], ctx =>
                ctx.EffectAsync(() => new ValueTask<Disposer?>(async () =>
                {
                    await gate.Task;
                    lock (timeline)
                    {
                        timeline.Add("consumer finished");
                    }
                })));

            var disposing = consumer.DisposeAsync().AsTask();
            var withdrawing = withdraw.DisposeAsync().AsTask().ContinueWith(_ =>
            {
                lock (timeline)
                {
                    timeline.Add("withdrawn");
                }
            });
            await Task.Delay(30);
            var early = withdrawing.IsCompleted;
            gate.SetResult();
            await Task.WhenAll(disposing, withdrawing);
            Assert.True(!early, "withdrawn while the consumer was still cleaning up");
            Assert.Equal("consumer finished,withdrawn", string.Join(',', timeline));
        });

        Harness.Case("child of a provider that injects the parent service is drained first", async () =>
        {
            var root = new Context();
            var gate = Gate();
            var timeline = new List<string>();
            var provider = await root.Use<OpenCloseService>();
            OpenCloseService.Timeline = timeline;
            var child = provider.Ctx.Inject([OpenCloseService.Key], ctx =>
            {
                var service = ctx.Get<OpenCloseService>();
                ctx.EffectAsync(() => new ValueTask<Disposer?>(async () =>
                {
                    await gate.Task;
                    service.Read();
                }));
            });
            await child;

            var task = provider.DisposeAsync().AsTask();
            await Task.Delay(30);
            gate.SetResult();
            await task;
            Assert.Equal("read: open,closed", string.Join(',', timeline));
        });

        // -------------------------------------------------------------- lookup (#209)
        Harness.Case("lookup stops at an isolation boundary inside a component", async () =>
        {
            var root = new Context();
            root.Provide("db", "shared");
            object? outer = null;
            Exception? inner = null;
            await root.Inject(["db"], ctx =>
            {
                outer = ctx.Get("db");
                try
                {
                    ctx.Isolate("db").Get("db");
                }
                catch (Exception error)
                {
                    inner = error;
                }
            });

            Assert.Equal("shared", outer);
            Assert.True(inner is ServiceNotInjectedException, $"got {inner?.GetType().Name ?? "the outer service"}");
        });

        Harness.Case("a fiber injects and re-provides the same name in another realm", async () =>
        {
            var root = new Context();
            root.Provide("foo", "outer");
            object? seenOuter = null;
            object? seenInner = null;
            var consumer = await root.Inject(["foo"], ctx =>
            {
                var isolated = ctx.Isolate("foo");
                isolated.Provide("foo", $"wraps {ctx.Get("foo")}");
                seenOuter = ctx.Get("foo");
                seenInner = isolated.Get("foo");
            });

            Assert.Equal("outer", seenOuter);
            Assert.Equal("wraps outer", seenInner);
            await consumer.DisposeAsync().AsTask().WaitAsync(TimeSpan.FromSeconds(2));
        });

        // --------------------------------------------------------- intercepts (#206)
        Harness.Case("context intercepts take priority over inject metadata", async () =>
        {
            var root = new Context();
            root.Provide("labelled", new object());
            string? plain = null;
            string? intercepted = null;
            await root.Use<MetadataComponent>();
            plain = MetadataComponent.Seen;
            await root.Intercept("labelled", "from-intercept").Use<MetadataComponent>();
            intercepted = MetadataComponent.Seen;

            Assert.Equal("from-inject", plain);
            Assert.Equal("from-intercept", intercepted);
        });

        // ------------------------------------------------------------ replace (#266)
        Harness.Case("registry.Replace swaps the implementation behind a fiber in place", async () =>
        {
            var root = new Context();
            ReplaceV1.Loads = ReplaceV2.Loads = 0;
            var fiber = await root.Use<ReplaceV1>(new ReplaceV1.Options { Text = "kept" });
            var moved = root.Registry.Replace(typeof(ReplaceV1), typeof(ReplaceV2));
            Assert.Equal(1, moved.Count);
            await fiber;

            Assert.True(fiber.Instance is ReplaceV2, $"instance is {fiber.Instance?.GetType().Name}");
            Assert.Equal("kept", ((ReplaceV2)fiber.Instance!).Text);
            Assert.Equal(1, ReplaceV1.Loads);
            Assert.Equal(1, ReplaceV2.Loads);

            // the old component is forwarded
            var forwarded = await root.Use<ReplaceV1>();
            Assert.True(forwarded.Instance is ReplaceV2, "old component was not forwarded");
            Assert.True(ReferenceEquals(root.Registry.Get(typeof(ReplaceV1))?.Key, typeof(ReplaceV2)), "registry lookup is not forwarded");
        });

        Harness.Case("registry.Replace takes the dependencies of the new component and restarts a failed fiber", async () =>
        {
            (var root, var _) = Quiet();
            var fiber = root.Use<FailingComponent>();
            try
            {
                await fiber;
            }
            catch (InvalidOperationException)
            {
            }

            Assert.Equal(FiberState.Failed, fiber.State);

            root.Registry.Replace(typeof(FailingComponent), typeof(NeedsCounter));
            await fiber;
            Assert.Equal(FiberState.Pending, fiber.State);

            await root.Use<CounterService>();
            await fiber;
            Assert.Equal(FiberState.Active, fiber.State);
        });

        // ---------------------------------------------------------------- events
        Harness.Case("a listener removed mid-dispatch is skipped (#242)", async () =>
        {
            var root = new Context();
            var seen = new List<string>();
            EffectHandle? second = null;
            await root.Apply("a", ctx => ctx.On(TestEvents.Tick, async (_, _) =>
            {
                seen.Add("a");
                await second!.DisposeAsync();
            }));
            await root.Apply("b", ctx => second = ctx.On(TestEvents.Tick, (_, _) => seen.Add("b")));

            await root.EmitAsync(TestEvents.Tick, 0);
            Assert.Equal("a", string.Join(',', seen));
        });

        Harness.Case("waterfall rejects calling next twice (#44)", async () =>
        {
            var root = new Context();
            await root.Apply("twice", ctx => ctx.On(TestEvents.Transform, async (_, value, next) =>
            {
                await next(value);
                return await next(value);
            }));

            var error = await Assert.ThrowsAsync<InvalidOperationException>(async () =>
                await root.WaterfallAsync(TestEvents.Transform, "x", value => new ValueTask<string>(value)));
            Assert.Equal("next() called multiple times", error.Message);
        });

        Harness.Case("parallel failures name the event and flatten in the logger (#205, #222)", async () =>
        {
            (var root, var logs) = Quiet();
            await root.Apply("a", ctx => ctx.On(TestEvents.Tick, (Action<Context, int>)((_, _) => throw new InvalidOperationException("first"))));
            await root.Apply("b", ctx => ctx.On(TestEvents.Tick, (Action<Context, int>)((_, _) => throw new InvalidOperationException("second"))));

            var error = await Assert.ThrowsAsync<AggregateError>(async () => await root.ParallelAsync(TestEvents.Tick, 1));
            Assert.True(error.Message.StartsWith("one or more listeners of event \"tick\" failed", StringComparison.Ordinal), error.Message);
            Assert.Equal(2, error.InnerExceptions.Count);

            root.Logger.Error(error);
            root.Logger.Error(new AggregateException("empty"));
            Assert.Equal("first,second,empty", string.Join(',', logs.Messages.Select(m => m.Text)));
        });
    }

    private static TaskCompletionSource Gate() => new(TaskCreationOptions.RunContinuationsAsynchronously);

    private static (Context Root, MemoryExporter Logs) Quiet()
    {
        var root = new Context();
        root.Loggers.ClearExporters();
        var logs = new MemoryExporter();
        root.Loggers.AddExporter(logs);
        return (root, logs);
    }
}
