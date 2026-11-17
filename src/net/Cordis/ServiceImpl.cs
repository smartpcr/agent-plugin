// -----------------------------------------------------------------------
// <copyright file="ServiceImpl.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// One registered binding of a service name in one isolate realm. Mirrors cordis' <c>Impl</c>:
/// each <c>Provide</c> call creates a fresh binding with its own <see cref="Uid"/>, so dependents
/// can tell a re-provided service apart from the one they loaded against.
/// </summary>
public sealed class ServiceImpl
{
    internal ServiceImpl(int uid, string name, Context ctx, object? value, Func<bool>? check, IsolateKey isolate)
    {
        this.Uid = uid;
        this.Name = name;
        this.Ctx = ctx;
        this.Value = value;
        this.Check = check;
        this.Isolate = isolate;
    }

    /// <summary>Gets identity of this binding. Part of every dependent's epoch.</summary>
    public int Uid { get; }

    /// <summary>Gets the service name this binding is provided under.</summary>
    public string Name { get; }

    /// <summary>Gets the context that provided this binding.</summary>
    public Context Ctx { get; }

    /// <summary>Gets the fiber that owns this binding: the fiber of <see cref="Ctx"/>.</summary>
    public Fiber Fiber => this.Ctx.Fiber;

    /// <summary>Gets the service instance. Swapped in place by <see cref="ServiceStore.Set"/>.</summary>
    public object? Value { get; internal set; }

    /// <summary>
    /// Gets the optional availability check. When it returns <see langword="false"/> or throws,
    /// dependents do not load against this binding.
    /// </summary>
    public Func<bool>? Check { get; }

    /// <summary>Gets the realm this binding is registered under.</summary>
    public IsolateKey Isolate { get; }

    /// <summary>Gets fibers that loaded against this binding and have not finished unloading.</summary>
    internal HashSet<Fiber> Dependents { get; } = [];

    /// <summary>Gets or sets completed when the last dependent has unloaded after this binding was withdrawn.</summary>
    internal TaskCompletionSource? Release { get; set; }
}
