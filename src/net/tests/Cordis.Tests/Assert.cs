// -----------------------------------------------------------------------
// <copyright file="Assert.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.Tests;

/// <summary>
/// Minimal assertion helpers used by the test cases; each failed assertion throws an
/// <see cref="InvalidOperationException"/> that the <see cref="Harness"/> reports as a failure.
/// </summary>
public static class Assert
{
    /// <summary>Asserts that a condition holds.</summary>
    /// <param name="condition">The condition that must be <see langword="true"/>.</param>
    /// <param name="message">The failure message used when the condition is <see langword="false"/>.</param>
    /// <exception cref="InvalidOperationException">Thrown when <paramref name="condition"/> is <see langword="false"/>.</exception>
    public static void True(bool condition, string message = "expected true")
    {
        if (!condition)
        {
            throw new InvalidOperationException(message);
        }
    }

    /// <summary>Asserts that two values are equal according to <see cref="EqualityComparer{T}.Default"/>.</summary>
    /// <typeparam name="T">The type of the values being compared.</typeparam>
    /// <param name="expected">The expected value.</param>
    /// <param name="actual">The actual value.</param>
    /// <param name="message">An optional failure message; when <see langword="null"/>, a message showing both values is used.</param>
    /// <exception cref="InvalidOperationException">Thrown when the values are not equal.</exception>
    public static void Equal<T>(T expected, T actual, string? message = null)
    {
        if (!EqualityComparer<T>.Default.Equals(expected, actual))
        {
            throw new InvalidOperationException(message ?? $"expected <{expected}>, got <{actual}>");
        }
    }

    /// <summary>Asserts that an asynchronous body throws an exception of the given type.</summary>
    /// <typeparam name="TException">The exception type the body is expected to throw.</typeparam>
    /// <param name="body">The asynchronous code under test.</param>
    /// <returns>A task whose result is the caught exception, so the caller can make further assertions on it.</returns>
    /// <exception cref="InvalidOperationException">Thrown when the body throws a different exception type or nothing at all.</exception>
    public static async Task<TException> ThrowsAsync<TException>(Func<Task> body)
        where TException : Exception
    {
        try
        {
            await body();
        }
        catch (TException error)
        {
            return error;
        }
        catch (Exception error)
        {
            throw new InvalidOperationException($"expected {typeof(TException).Name}, got {error.GetType().Name}");
        }

        throw new InvalidOperationException($"expected {typeof(TException).Name}, nothing thrown");
    }

    /// <summary>Polls until a condition holds, so tests never depend on a fixed sleep.</summary>
    /// <param name="condition">The condition to poll, checked about every 5 milliseconds.</param>
    /// <param name="message">The failure message used when the condition does not hold before the timeout.</param>
    /// <param name="timeoutMs">The maximum time to wait, in milliseconds.</param>
    /// <returns>A task that completes when the condition holds.</returns>
    /// <exception cref="InvalidOperationException">Thrown when the condition does not hold within <paramref name="timeoutMs"/>.</exception>
    public static async Task Eventually(Func<bool> condition, string message = "condition never held", int timeoutMs = 2000)
    {
        var deadline = DateTime.UtcNow.AddMilliseconds(timeoutMs);
        while (DateTime.UtcNow < deadline)
        {
            if (condition())
            {
                return;
            }

            await Task.Delay(5);
        }

        throw new InvalidOperationException(message);
    }
}
