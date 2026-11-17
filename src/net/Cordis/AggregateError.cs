// -----------------------------------------------------------------------
// <copyright file="AggregateError.cs" company="Microsoft Corp.">
//     Copyright (c) Microsoft Corp. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis;

/// <summary>
/// The exception thrown when several operations fail together, such as listeners of
/// <see cref="EventsService.ParallelAsync{TArgs}"/>. The analogue of JavaScript's <c>AggregateError</c>.
/// </summary>
/// <param name="message">The message that describes the combined failure.</param>
/// <param name="errors">The individual failures, exposed as <see cref="AggregateException.InnerExceptions"/>.</param>
public sealed class AggregateError(string message, IReadOnlyList<Exception> errors)
    : AggregateException(message, errors);
