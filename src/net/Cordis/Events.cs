namespace Cordis;

/// <summary>Fire-and-forget or awaited notification. Handlers: <c>Func&lt;Context, TArgs, ValueTask&gt;</c>.</summary>
public sealed class EventKey<TArgs>(string name)
{
    public string Name { get; } = name;
    public override string ToString() => Name;
}

/// <summary>First non-null result wins. Handlers: <c>Func&lt;Context, TArgs, ValueTask&lt;TResult?&gt;&gt;</c>.</summary>
public sealed class BailKey<TArgs, TResult>(string name)
{
    public string Name { get; } = name;
    public override string ToString() => Name;
}

/// <summary>Middleware chain over a value. Handlers receive <c>next</c>.</summary>
public sealed class WaterfallKey<TArgs>(string name)
{
    public string Name { get; } = name;
    public override string ToString() => Name;
}

internal sealed class Hook(Context ctx, Delegate callback, bool global)
{
    public Context Ctx { get; } = ctx;
    public Delegate Callback { get; } = callback;
    public bool Global { get; } = global;

    /// <summary>Set on unregistration, so a dispatch already in progress skips the hook.</summary>
    public volatile bool Removed;
}

public sealed class EventsService(Context root)
{
    private readonly Dictionary<object, List<Hook>> _hooks = new(ReferenceEqualityComparer.Instance);

    // ------------------------------------------------------------ registration

    public EffectHandle On<TArgs>(
        Context ctx, EventKey<TArgs> key, Func<Context, TArgs, ValueTask> handler,
        bool prepend = false, bool global = false)
        => Register(ctx, key, handler, prepend, global);

    public EffectHandle On<TArgs>(
        Context ctx, EventKey<TArgs> key, Action<Context, TArgs> handler,
        bool prepend = false, bool global = false)
        => Register(ctx, key, (Func<Context, TArgs, ValueTask>)((c, a) => { handler(c, a); return default; }), prepend, global);

    public EffectHandle On<TArgs, TResult>(
        Context ctx, BailKey<TArgs, TResult> key, Func<Context, TArgs, ValueTask<TResult?>> handler,
        bool prepend = false, bool global = false)
        => Register(ctx, key, handler, prepend, global);

    public EffectHandle On<TArgs>(
        Context ctx, WaterfallKey<TArgs> key,
        Func<Context, TArgs, Func<TArgs, ValueTask<TArgs>>, ValueTask<TArgs>> handler,
        bool prepend = false, bool global = false)
        => Register(ctx, key, handler, prepend, global);

    public EffectHandle Once<TArgs>(Context ctx, EventKey<TArgs> key, Func<Context, TArgs, ValueTask> handler)
    {
        EffectHandle? handle = null;
        handle = On(ctx, key, async (c, a) =>
        {
            if (handle is not null) await handle.DisposeAsync().ConfigureAwait(false);
            await handler(c, a).ConfigureAwait(false);
        });
        return handle;
    }

    private EffectHandle Register(Context ctx, object key, Delegate handler, bool prepend, bool global)
    {
        return ctx.Fiber.EffectSync(() =>
        {
            var hook = new Hook(ctx, handler, global);
            lock (root.SyncRoot)
            {
                if (!_hooks.TryGetValue(key, out List<Hook>? list)) _hooks[key] = list = [];
                if (prepend) list.Insert(0, hook); else list.Add(hook);
            }
            return () =>
            {
                lock (root.SyncRoot)
                {
                    hook.Removed = true;
                    if (_hooks.TryGetValue(key, out List<Hook>? list) && list.Remove(hook) && list.Count == 0) _hooks.Remove(key);
                }
                return default;
            };
        }, $"ctx.On({key})");
    }

    private List<Hook> Snapshot(object key, Func<Context, bool>? filter)
    {
        lock (root.SyncRoot)
        {
            if (!_hooks.TryGetValue(key, out List<Hook>? list)) return [];
            if (filter is null) return [.. list];
            return [.. list.Where(hook => hook.Global || filter(hook.Ctx))];
        }
    }

    // -------------------------------------------------------------- dispatch

    /// <summary>Sequential dispatch; the first failure propagates.</summary>
    public async ValueTask EmitAsync<TArgs>(EventKey<TArgs> key, TArgs args, Func<Context, bool>? filter = null)
    {
        foreach (Hook hook in Snapshot(key, filter))
        {
            if (hook.Removed) continue;
            await ((Func<Context, TArgs, ValueTask>)hook.Callback)(hook.Ctx, args).ConfigureAwait(false);
        }
    }

    /// <summary>Concurrent dispatch; failures are aggregated.</summary>
    public async ValueTask ParallelAsync<TArgs>(EventKey<TArgs> key, TArgs args, Func<Context, bool>? filter = null)
    {
        Task[] tasks = Snapshot(key, filter)
            .Select(hook => hook.Removed
                ? Task.CompletedTask
                : Invoke((Func<Context, TArgs, ValueTask>)hook.Callback, hook.Ctx, args))
            .ToArray();
        if (tasks.Length == 0) return;

        try
        {
            await Task.WhenAll(tasks).ConfigureAwait(false);
        }
        catch
        {
            Exception[] errors = tasks
                .Where(task => task.IsFaulted)
                .SelectMany(task => task.Exception!.InnerExceptions)
                .ToArray();
            throw new AggregateError($"one or more listeners of event \"{key.Name}\" failed", errors);
        }

        // a synchronous throw becomes a faulted task, like an async one
        static async Task Invoke(Func<Context, TArgs, ValueTask> callback, Context ctx, TArgs args)
            => await callback(ctx, args).ConfigureAwait(false);
    }

    /// <summary>Returns the first non-null result.</summary>
    public async ValueTask<TResult?> BailAsync<TArgs, TResult>(
        BailKey<TArgs, TResult> key, TArgs args, Func<Context, bool>? filter = null)
    {
        foreach (Hook hook in Snapshot(key, filter))
        {
            if (hook.Removed) continue;
            TResult? result = await ((Func<Context, TArgs, ValueTask<TResult?>>)hook.Callback)(hook.Ctx, args).ConfigureAwait(false);
            if (result is not null) return result;
        }
        return default;
    }

    /// <summary>
    /// Middleware chain terminated by <paramref name="terminal"/>. Each handler may call its
    /// <c>next</c> at most once; removed handlers are skipped even mid-dispatch.
    /// </summary>
    public ValueTask<TArgs> WaterfallAsync<TArgs>(
        WaterfallKey<TArgs> key, TArgs args, Func<TArgs, ValueTask<TArgs>> terminal, Func<Context, bool>? filter = null)
    {
        List<Hook> hooks = Snapshot(key, filter);
        int index = 0;
        return Dispatch(args);

        ValueTask<TArgs> Dispatch(TArgs value)
        {
            while (index < hooks.Count && hooks[index].Removed) index++;
            if (index >= hooks.Count) return terminal(value);
            Hook hook = hooks[index++];
            var callback = (Func<Context, TArgs, Func<TArgs, ValueTask<TArgs>>, ValueTask<TArgs>>)hook.Callback;
            bool called = false;
            return callback(hook.Ctx, value, next =>
            {
                if (called) throw new InvalidOperationException("next() called multiple times");
                called = true;
                return Dispatch(next);
            });
        }
    }
}

public sealed class AggregateError(string message, IReadOnlyList<Exception> errors)
    : AggregateException(message, errors);

/// <summary>
/// Synchronous lifecycle notifications used by the framework and by the loader.
/// Analogue of cordis' <c>internal/*</c> events.
/// </summary>
public sealed class InternalEvents
{
    /// <summary>A fiber was created (Uid set) or disposed (Uid null).</summary>
    public event Action<Fiber>? PluginChanged;

    public event Action<Fiber, FiberState>? StatusChanged;

    public event Action<string, object?>? ServiceChanged;

    /// <summary>
    /// Raised by <see cref="Fiber.UpdateAsync"/> (unless <c>noSave</c>) with the original,
    /// unvalidated config, so a loader can persist it. Analogue of <c>internal/commit</c>.
    /// </summary>
    public event Action<Fiber, object?>? Committing;

    /// <summary>A fiber was moved to a new component by <see cref="RegistryService.Replace"/>.</summary>
    public event Action<Fiber>? Replaced;

    internal void RaisePluginChanged(Fiber fiber) => PluginChanged?.Invoke(fiber);

    internal void RaiseStatusChanged(Fiber fiber, FiberState old) => StatusChanged?.Invoke(fiber, old);

    internal void RaiseServiceChanged(string name, object? value) => ServiceChanged?.Invoke(name, value);

    internal void RaiseCommitting(Fiber fiber, object? config) => Committing?.Invoke(fiber, config);

    internal void RaiseReplaced(Fiber fiber) => Replaced?.Invoke(fiber);
}
