// -----------------------------------------------------------------------
// <copyright file="GatedValue.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.Tests;

/// <summary>
/// Test component that records loads and disposals; a load with <see cref="Options.Value"/> = 1 signals
/// <see cref="Started"/> and then waits for <see cref="Release"/>.
/// </summary>
public sealed class GatedValue : IAsyncInit, IDisposable
{
    private readonly int value;

    /// <summary>Initializes a new instance of the <see cref="GatedValue"/> class.</summary>
    /// <param name="ctx">The context the component is loaded into.</param>
    /// <param name="config">The configuration whose value is recorded in <see cref="Applied"/>.</param>
    public GatedValue(Context ctx, Options config)
    {
        this.value = config.Value;
        lock (Applied)
        {
            Applied.Add(this.value);
        }
    }

    /// <summary>Gets or sets the configuration values of every instance constructed, in construction order.</summary>
    public static List<int> Applied { get; set; } = [];

    /// <summary>Gets or sets the configuration values of every instance disposed, in disposal order.</summary>
    public static List<int> Disposed { get; set; } = [];

    /// <summary>Gets or sets the signal completed when an instance with value 1 starts initializing.</summary>
    public static TaskCompletionSource Started { get; set; } = new();

    /// <summary>Gets or sets the gate that the test completes to let an instance with value 1 finish initializing.</summary>
    public static TaskCompletionSource Release { get; set; } = new();

    /// <summary>Clears the recorded values and replaces <see cref="Started"/> and <see cref="Release"/> with fresh signals.</summary>
    public static void Reset()
    {
        Applied = [];
        Disposed = [];
        Started = new(TaskCreationOptions.RunContinuationsAsynchronously);
        Release = new(TaskCreationOptions.RunContinuationsAsynchronously);
    }

    /// <inheritdoc/>
    public async ValueTask InitAsync()
    {
        if (this.value != 1)
        {
            return;
        }

        Started.SetResult();
        await Release.Task;
    }

    /// <inheritdoc/>
    public void Dispose()
    {
        lock (Disposed)
        {
            Disposed.Add(this.value);
        }
    }

    /// <summary>Configuration for <see cref="GatedValue"/>.</summary>
    public sealed class Options
    {
        /// <summary>Gets or sets the value recorded on load and disposal; 1 makes initialization wait for <see cref="Release"/>.</summary>
        public int Value { get; set; }
    }
}
