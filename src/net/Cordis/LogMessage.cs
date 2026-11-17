// -----------------------------------------------------------------------
// <copyright file="LogMessage.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>One log entry, as delivered to every <see cref="ILogExporter"/>.</summary>
/// <param name="Timestamp">The local time at which the entry was written.</param>
/// <param name="Name">The name of the logger that wrote the entry.</param>
/// <param name="Level">The severity of the entry.</param>
/// <param name="Text">The message text; for an exception, its message.</param>
/// <param name="Error">The logged exception, or <see langword="null"/> for a plain text entry.</param>
public sealed record LogMessage(DateTimeOffset Timestamp, string Name, LogLevel Level, string Text, Exception? Error);
