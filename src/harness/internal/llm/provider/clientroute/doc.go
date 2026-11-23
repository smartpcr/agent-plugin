// Package clientroute holds what both signed-in client routes share
// (DH-D-LLM-12, DH-ADR-LLM-10): the harness reaches a vendor model by starting
// the user's installed, signed-in command-line client (Claude Code, the GitHub
// Copilot CLI) as a child process, one process per attempt, and never reads a
// token (M1-D-PIPE-3 A). The guarantee holds at the client-invocation boundary
// (M1-D-PIPE-4 A): what the harness logs and can rebuild is the argument list,
// the standard input, the files it wrote for the attempt, the environment names
// and the client version.
//
// The pieces, in the order an adapter uses them:
//
//   - Locate finds the program (a configured absolute path, else the bare name
//     on the child's PATH; a Windows .cmd shim is reported so the adapter starts
//     it with subprocess.AllowChecked).
//   - EnvSpec.Build derives the child environment from the host's through
//     subprocess.BuildEnv: every credential and provider-override variable is
//     removed, so the client's own sign-in is what is used, and the route's
//     settings are added.
//   - Probe reads the client's version once per prepared call, in that
//     environment, with a deadline and a bounded line reader; a version below
//     the floor fails before any request text is written.
//   - Render turns a session.Request into the system prompt file, the tool
//     schema file and the transcript for standard input. It is a pure function
//     of the request: print and prompt modes take no earlier assistant turns, so
//     the history is rendered as text.
//   - AttemptDir is the attempt's private, empty working directory holding the
//     files Render produced; it is removed when the attempt ends.
//   - Start runs the client once with the request on standard input and reads
//     its standard output as bounded lines; Run.Stop stops the whole process
//     tree (the client and the MCP listing server it started).
//   - NewInvocation and Recorder build the invocation record the attempt
//     settles (event.ClientInvocation), with the attempt directory written as
//     {attempt} so the record is the same on every host.
//
// The listing-only MCP server the clients start lives in package
// clientroute/listing, so the harness binary can serve it without linking the
// model runtime.
package clientroute
