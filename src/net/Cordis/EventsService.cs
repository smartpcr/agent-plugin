// -----------------------------------------------------------------------
// <copyright file="EventsService.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// The event bus shared by a root context and all of its descendants. Listeners are keyed by
/// reference-identity event keys (<see cref="EventKey{TArgs}"/>, <see cref="BailKey{TArgs, TResult}"/>
/// and <see cref="WaterfallKey{TArgs}"/>) and each registration is an effect owned by the
/// registering context's fiber, so it is unregistered automatically when that fiber unloads.
/// </summary>
/// <param name="root">The root context whose synchronization root guards the listener table.</param>
public sealed class EventsService(Context root)
{
    private readonly Dictionary<object, List<Hook>> hooks = new(ReferenceEqualityComparer.Instance);

    // ------------------------------------------------------------ registration

    /// <summary>
    /// Registers an asynchronous listener for a notification event. The registration is an effect
    /// of <paramref name="ctx"/>'s fiber and is removed when the fiber unloads or the returned
    /// handle is disposed.
    /// </summary>
    /// <typeparam name="TArgs">The type of the event arguments.</typeparam>
    /// <param name="ctx">The context registering the listener; its fiber owns the registration.</param>
    /// <param name="key">The event to listen to.</param>
    /// <param name="handler">The listener, invoked with the registering context and the event arguments.</param>
    /// <param name="prepend">Whether to insert the listener before existing listeners instead of after them.</param>
    /// <param name="global">Whether the listener is invoked even when a dispatch filter would exclude its context.</param>
    /// <returns>An <see cref="EffectHandle"/> that unregisters the listener when disposed.</returns>
    public EffectHandle On<TArgs>(
        Context ctx,
        EventKey<TArgs> key,
        Func<Context, TArgs, ValueTask> handler,
        bool prepend = false,
        bool global = false)
        => this.Register(ctx, key, handler, prepend, global);

    /// <summary>
    /// Registers a synchronous listener for a notification event. The registration is an effect
    /// of <paramref name="ctx"/>'s fiber and is removed when the fiber unloads or the returned
    /// handle is disposed.
    /// </summary>
    /// <typeparam name="TArgs">The type of the event arguments.</typeparam>
    /// <param name="ctx">The context registering the listener; its fiber owns the registration.</param>
    /// <param name="key">The event to listen to.</param>
    /// <param name="handler">The listener, invoked with the registering context and the event arguments.</param>
    /// <param name="prepend">Whether to insert the listener before existing listeners instead of after them.</param>
    /// <param name="global">Whether the listener is invoked even when a dispatch filter would exclude its context.</param>
    /// <returns>An <see cref="EffectHandle"/> that unregisters the listener when disposed.</returns>
    public EffectHandle On<TArgs>(
        Context ctx,
        EventKey<TArgs> key,
        Action<Context, TArgs> handler,
        bool prepend = false,
        bool global = false)
    {
        Func<Context, TArgs, ValueTask> wrapped = (c, a) =>
        {
            handler(c, a);
            return default;
        };
        return this.Register(ctx, key, wrapped, prepend, global);
    }

    /// <summary>
    /// Registers a listener for a bail event. During <see cref="BailAsync{TArgs, TResult}"/> the
    /// first listener to return a non-null result ends the dispatch. The registration is an effect
    /// of <paramref name="ctx"/>'s fiber and is removed when the fiber unloads or the returned
    /// handle is disposed.
    /// </summary>
    /// <typeparam name="TArgs">The type of the event arguments.</typeparam>
    /// <typeparam name="TResult">The type of the result a listener may produce.</typeparam>
    /// <param name="ctx">The context registering the listener; its fiber owns the registration.</param>
    /// <param name="key">The event to listen to.</param>
    /// <param name="handler">The listener; it returns <see langword="null"/> to defer to the next listener.</param>
    /// <param name="prepend">Whether to insert the listener before existing listeners instead of after them.</param>
    /// <param name="global">Whether the listener is invoked even when a dispatch filter would exclude its context.</param>
    /// <returns>An <see cref="EffectHandle"/> that unregisters the listener when disposed.</returns>
    public EffectHandle On<TArgs, TResult>(
        Context ctx,
        BailKey<TArgs, TResult> key,
        Func<Context, TArgs, ValueTask<TResult?>> handler,
        bool prepend = false,
        bool global = false)
        => this.Register(ctx, key, handler, prepend, global);

    /// <summary>
    /// Registers a middleware listener for a waterfall event. The listener receives the current
    /// value and a <c>next</c> delegate that passes a (possibly transformed) value down the chain;
    /// it may call <c>next</c> at most once, or return without calling it to short-circuit the
    /// chain. The registration is an effect of <paramref name="ctx"/>'s fiber and is removed when
    /// the fiber unloads or the returned handle is disposed.
    /// </summary>
    /// <typeparam name="TArgs">The type of the value flowing through the chain.</typeparam>
    /// <param name="ctx">The context registering the listener; its fiber owns the registration.</param>
    /// <param name="key">The event to listen to.</param>
    /// <param name="handler">The middleware, invoked with the registering context, the current value and <c>next</c>.</param>
    /// <param name="prepend">Whether to insert the listener before existing listeners instead of after them.</param>
    /// <param name="global">Whether the listener is invoked even when a dispatch filter would exclude its context.</param>
    /// <returns>An <see cref="EffectHandle"/> that unregisters the listener when disposed.</returns>
    public EffectHandle On<TArgs>(
        Context ctx,
        WaterfallKey<TArgs> key,
        Func<Context, TArgs, Func<TArgs, ValueTask<TArgs>>, ValueTask<TArgs>> handler,
        bool prepend = false,
        bool global = false)
        => this.Register(ctx, key, handler, prepend, global);

    /// <summary>
    /// Registers an asynchronous listener that handles a notification event at most once. On the
    /// first dispatch the registration is disposed before <paramref name="handler"/> runs.
    /// </summary>
    /// <typeparam name="TArgs">The type of the event arguments.</typeparam>
    /// <param name="ctx">The context registering the listener; its fiber owns the registration.</param>
    /// <param name="key">The event to listen to.</param>
    /// <param name="handler">The listener, invoked with the registering context and the event arguments.</param>
    /// <returns>An <see cref="EffectHandle"/> that unregisters the listener if disposed before the event fires.</returns>
    public EffectHandle Once<TArgs>(Context ctx, EventKey<TArgs> key, Func<Context, TArgs, ValueTask> handler)
    {
        EffectHandle? handle = null;
        handle = this.On(ctx, key, async (c, a) =>
        {
            if (handle is not null)
            {
                await handle.DisposeAsync().ConfigureAwait(false);
            }

            await handler(c, a).ConfigureAwait(false);
        });
        return handle;
    }

    // -------------------------------------------------------------- dispatch

    /// <summary>
    /// Dispatches a notification event to its listeners sequentially, in registration order. The
    /// first failure propagates and stops the dispatch. Listeners removed after the dispatch
    /// started are skipped.
    /// </summary>
    /// <typeparam name="TArgs">The type of the event arguments.</typeparam>
    /// <param name="key">The event to dispatch.</param>
    /// <param name="args">The event arguments passed to every listener.</param>
    /// <param name="filter">An optional predicate over each listener's context; non-global listeners whose context does not match are skipped.</param>
    /// <returns>A task that completes when every listener has run.</returns>
    public async ValueTask EmitAsync<TArgs>(EventKey<TArgs> key, TArgs args, Func<Context, bool>? filter = null)
    {
        foreach (var hook in this.Snapshot(key, filter))
        {
            if (hook.Removed)
            {
                continue;
            }

            await ((Func<Context, TArgs, ValueTask>)hook.Callback)(hook.Ctx, args).ConfigureAwait(false);
        }
    }

    /// <summary>
    /// Dispatches a notification event to all of its listeners concurrently. Every listener runs
    /// to completion; if any fail, their exceptions are thrown together as an
    /// <see cref="AggregateError"/>.
    /// </summary>
    /// <typeparam name="TArgs">The type of the event arguments.</typeparam>
    /// <param name="key">The event to dispatch.</param>
    /// <param name="args">The event arguments passed to every listener.</param>
    /// <param name="filter">An optional predicate over each listener's context; non-global listeners whose context does not match are skipped.</param>
    /// <returns>A task that completes when every listener has finished.</returns>
    /// <exception cref="AggregateError">One or more listeners failed.</exception>
    public async ValueTask ParallelAsync<TArgs>(EventKey<TArgs> key, TArgs args, Func<Context, bool>? filter = null)
    {
        var tasks = this.Snapshot(key, filter)
            .Select(hook => hook.Removed
                ? Task.CompletedTask
                : Invoke((Func<Context, TArgs, ValueTask>)hook.Callback, hook.Ctx, args))
            .ToArray();
        if (tasks.Length == 0)
        {
            return;
        }

        try
        {
            await Task.WhenAll(tasks).ConfigureAwait(false);
        }
        catch
        {
            var errors = tasks
                .Where(task => task.IsFaulted)
                .SelectMany(task => task.Exception!.InnerExceptions)
                .ToArray();
            throw new AggregateError($"one or more listeners of event \"{key.Name}\" failed", errors);
        }

        // a synchronous throw becomes a faulted task, like an async one
        static async Task Invoke(Func<Context, TArgs, ValueTask> callback, Context ctx, TArgs args)
            => await callback(ctx, args).ConfigureAwait(false);
    }

    /// <summary>
    /// Dispatches a bail event to its listeners sequentially and stops at the first listener that
    /// returns a non-null result.
    /// </summary>
    /// <typeparam name="TArgs">The type of the event arguments.</typeparam>
    /// <typeparam name="TResult">The type of the result a listener may produce.</typeparam>
    /// <param name="key">The event to dispatch.</param>
    /// <param name="args">The event arguments passed to each listener.</param>
    /// <param name="filter">An optional predicate over each listener's context; non-global listeners whose context does not match are skipped.</param>
    /// <returns>A task whose result is the first non-null listener result, or the default value when no listener produced one.</returns>
    public async ValueTask<TResult?> BailAsync<TArgs, TResult>(
        BailKey<TArgs, TResult> key, TArgs args, Func<Context, bool>? filter = null)
    {
        foreach (var hook in this.Snapshot(key, filter))
        {
            if (hook.Removed)
            {
                continue;
            }

            var result = await ((Func<Context, TArgs, ValueTask<TResult?>>)hook.Callback)(hook.Ctx, args).ConfigureAwait(false);
            if (result is not null)
            {
                return result;
            }
        }

        return default;
    }

    /// <summary>
    /// Middleware chain terminated by <paramref name="terminal"/>. Each handler may call its
    /// <c>next</c> at most once; removed handlers are skipped even mid-dispatch.
    /// </summary>
    /// <typeparam name="TArgs">The type of the value flowing through the chain.</typeparam>
    /// <param name="key">The event to dispatch.</param>
    /// <param name="args">The initial value passed to the first handler.</param>
    /// <param name="terminal">The function invoked with the final value when the last handler calls <c>next</c>.</param>
    /// <param name="filter">An optional predicate over each listener's context; non-global listeners whose context does not match are skipped.</param>
    /// <returns>A task whose result is the value returned by the outermost handler, or by <paramref name="terminal"/> when there are no handlers.</returns>
    /// <exception cref="InvalidOperationException">A handler called <c>next</c> more than once.</exception>
    public ValueTask<TArgs> WaterfallAsync<TArgs>(
        WaterfallKey<TArgs> key, TArgs args, Func<TArgs, ValueTask<TArgs>> terminal, Func<Context, bool>? filter = null)
    {
        var hooks = this.Snapshot(key, filter);
        var index = 0;
        return Dispatch(args);

        ValueTask<TArgs> Dispatch(TArgs value)
        {
            while (index < hooks.Count && hooks[index].Removed)
            {
                index++;
            }

            if (index >= hooks.Count)
            {
                return terminal(value);
            }

            var hook = hooks[index++];
            var callback = (Func<Context, TArgs, Func<TArgs, ValueTask<TArgs>>, ValueTask<TArgs>>)hook.Callback;
            var called = false;
            return callback(hook.Ctx, value, next =>
            {
                if (called)
                {
                    throw new InvalidOperationException("next() called multiple times");
                }

                called = true;
                return Dispatch(next);
            });
        }
    }

    private EffectHandle Register(Context ctx, object key, Delegate handler, bool prepend, bool global)
    {
        return ctx.Fiber.EffectSync(
            () =>
            {
                var hook = new Hook(ctx, handler, global);
                lock (root.SyncRoot)
                {
                    if (!this.hooks.TryGetValue(key, out var list))
                    {
                        this.hooks[key] = list = [];
                    }

                    if (prepend)
                    {
                        list.Insert(0, hook);
                    }
                    else
                    {
                        list.Add(hook);
                    }
                }

                return () =>
                {
                    lock (root.SyncRoot)
                    {
                        hook.Removed = true;
                        if (this.hooks.TryGetValue(key, out var list) && list.Remove(hook) && list.Count == 0)
                        {
                            this.hooks.Remove(key);
                        }
                    }

                    return default;
                };
            },
            $"ctx.On({key})");
    }

    private List<Hook> Snapshot(object key, Func<Context, bool>? filter)
    {
        lock (root.SyncRoot)
        {
            if (!this.hooks.TryGetValue(key, out var list))
            {
                return [];
            }

            if (filter is null)
            {
                return [.. list];
            }

            return [.. list.Where(hook => hook.Global || filter(hook.Ctx))];
        }
    }
}
