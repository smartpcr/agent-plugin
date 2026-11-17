// -----------------------------------------------------------------------
// <copyright file="Harness.cs" company="Smartpcr">
//     Copyright (c) Smartpcr. All rights reserved.
// </copyright>
// -----------------------------------------------------------------------

namespace Cordis.Tests;

/// <summary>Minimal test runner: cases are registered with <see cref="Case"/> and executed by <see cref="RunAsync"/>.</summary>
public static class Harness
{
    private static readonly List<(string Name, Func<Task> Body)> SCases = [];

    /// <summary>Registers a test case to run later.</summary>
    /// <param name="name">The case name printed in the results.</param>
    /// <param name="body">The asynchronous test body; it fails by throwing.</param>
    public static void Case(string name, Func<Task> body) => SCases.Add((name, body));

    /// <summary>Runs every registered case in order, each with a 10 second timeout, and prints a pass/fail report.</summary>
    /// <returns>A task whose result is the process exit code: 0 when every case passed, otherwise 1.</returns>
    public static async Task<int> RunAsync()
    {
        var failed = 0;
        foreach ((var name, var body) in SCases)
        {
            try
            {
                await body().WaitAsync(TimeSpan.FromSeconds(10));
                Console.WriteLine($"  PASS  {name}");
            }
            catch (Exception error)
            {
                failed++;
                Console.WriteLine($"  FAIL  {name}");
                Console.WriteLine($"        {error.GetType().Name}: {error.Message}");
                if (error.StackTrace is { } stack)
                {
                    Console.WriteLine(string.Join('\n', stack.Split('\n').Take(3).Select(line => "        " + line.Trim())));
                }
            }
        }

        Console.WriteLine();
        Console.WriteLine(failed == 0 ? $"{SCases.Count} passed" : $"{SCases.Count - failed} passed, {failed} failed");
        return failed == 0 ? 0 : 1;
    }
}
