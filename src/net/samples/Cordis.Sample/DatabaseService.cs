// -----------------------------------------------------------------------
// <copyright file="DatabaseService.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

using Cordis;

/// <summary>
/// Sample service registered under the <c>database</c> key that simulates a connection: it loads a
/// greeting row during asynchronous initialization and clears its rows on disposal.
/// </summary>
/// <param name="ctx">The context that provides the service.</param>
[ServiceName("database")]
public sealed class DatabaseService(Context ctx) : Service(ctx, "database"), IAsyncInit, IAsyncDisposable
{
    private readonly Dictionary<string, string> rows = [];

    /// <inheritdoc/>
    public async ValueTask InitAsync()
    {
        await Task.Delay(50); // pretend to open a connection
        this.rows["greeting"] = "hello from the database";
        this.Ctx.Logger.Info("connected");
    }

    /// <summary>Reads a row by key.</summary>
    /// <param name="key">The key of the row to read.</param>
    /// <returns>The row value, or <see langword="null"/> when no row has that key.</returns>
    public string? Read(string key) => this.rows.GetValueOrDefault(key);

    /// <inheritdoc/>
    public ValueTask DisposeAsync()
    {
        this.Ctx.Logger.Info("disconnected");
        this.rows.Clear();
        return default;
    }
}
