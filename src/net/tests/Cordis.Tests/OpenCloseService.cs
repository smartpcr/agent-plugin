// -----------------------------------------------------------------------
// <copyright file="OpenCloseService.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.Tests;

/// <summary>
/// Test service that opens a simulated connection on initialization and records reads and the
/// connection close in <see cref="Timeline"/>, so tests can check dependents stop before the provider closes.
/// </summary>
[ServiceName(Key)]
public sealed class OpenCloseService : Service, IAsyncInit
{
    /// <summary>The service key the service is registered under.</summary>
    public const string Key = "open-close";

    private bool closed;

    /// <summary>Initializes a new instance of the <see cref="OpenCloseService"/> class.</summary>
    /// <param name="ctx">The context that provides the service.</param>
    public OpenCloseService(Context ctx)
        : base(ctx, Key)
    {
    }

    /// <summary>Gets or sets the ordered record of reads and the connection close.</summary>
    public static List<string> Timeline { get; set; } = [];

    /// <inheritdoc/>
    public ValueTask InitAsync()
    {
        this.Ctx.Effect(
            () => () =>
            {
                this.closed = true;
                lock (Timeline)
                {
                    Timeline.Add("closed");
                }
            },
            "connection");
        return default;
    }

    /// <summary>Records a read in <see cref="Timeline"/>, noting whether the connection was open or closed.</summary>
    public void Read()
    {
        lock (Timeline)
        {
            Timeline.Add($"read: {(this.closed ? "closed" : "open")}");
        }
    }
}
