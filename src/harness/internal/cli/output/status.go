package output

import (
	"context"
	"errors"
	"fmt"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// The exit statuses: one closed table for every command (app-cli.md §6.2.4,
// tech-spec §5.5). Statuses 1 to 8 are result classes that mean the same in every
// command; the others are process-level.
//
// Usage is 2 as Stage 6.3 of the implementation plan fixes it; app-cli.md §6.2.4
// proposes 64 so that 2 stays with the Go runtime's panic exit. The table is the
// one place to change if that is decided the other way.
const (
	OK          = 0
	Failed      = 1
	Usage       = 2
	Denied      = 3
	Unavailable = 4
	Invalid     = 5
	Busy        = 6
	Limit       = 7
	Cancelled   = 8
	Fatal       = 70
	Config      = 78
	Forced      = 124
	Stalled     = 125
	Interrupted = 130
)

// StatusInfo describes one exit status for the command reference.
type StatusInfo struct {
	Code    int    `json:"code"`
	Name    string `json:"name"`
	Meaning string `json:"meaning"`
}

// Statuses is the closed exit table, in code order.
func Statuses() []StatusInfo {
	return []StatusInfo{
		{OK, "ok", "Success; also help and version"},
		{Failed, "failed", "The thing asked for ran and failed"},
		{Usage, "usage", "Usage error: unknown command or flag, a broken rule between flags, a missing argument"},
		{Denied, "denied", "Denied by policy, a guard or a person"},
		{Unavailable, "unavailable", "Unavailable: no host, no endpoint, a capability that is missing"},
		{Invalid, "invalid", "Invalid input or not found: an unknown session, tool or profile"},
		{Busy, "busy", "Busy or conflict"},
		{Limit, "limit", "A limit or a timeout ended the work"},
		{Cancelled, "cancelled", "Cancelled, or the outcome is unknown"},
		{Fatal, "fatal", "Fatal error with a report written"},
		{Config, "config", "The configuration cannot be composed, or a required entry failed to load"},
		{Forced, "forced", "Forced exit: the exit deadline passed during shutdown"},
		{Stalled, "stalled", "A stdio client stopped reading"},
		{Interrupted, "interrupted", "Interrupted by Ctrl-C"},
	}
}

// codes maps every error code the harness declares to its status. A code missing
// here is status Failed; TestEveryReasonHasAStatus keeps the table complete.
var codes = map[herr.Reason]int{
	ReasonUsage:        Usage,
	ReasonFailed:       Failed,
	ReasonUnavailable:  Unavailable,
	ReasonClosed:       Failed,
	ReasonTwoDocuments: Fatal,

	"profile/unknown":    Invalid,
	"profile/bad-layer":  Config,
	"profile/bad-bundle": Config,

	"boot/required":         Config,
	"boot/host-preparation": Fatal,
	"boot/panic":            Fatal,

	"config/busy":         Busy,
	"config/computed":     Invalid,
	"config/not-writable": Config,
	"config/rejected":     Config,
	"config/shadowed":     Busy,
	"config/stale":        Busy,
	"config/stopping":     Busy,

	"egress/ca-unreadable":       Config,
	"egress/not-permitted":       Denied,
	"egress/refused-proxy-value": Config,

	"home/io":            Failed,
	"home/not-directory": Invalid,
	"home/not-private":   Denied,
	"home/remote":        Denied,

	"homedir/no-user-home": Unavailable,

	"launch/bad-env-file":   Config,
	"launch/facts":          Failed,
	"launch/forbidden-name": Invalid,
	"launch/read":           Failed,

	"secret/bad-reference":  Invalid,
	"secret/empty":          Invalid,
	"secret/not-configured": Unavailable,
	"secret/source":         Unavailable,
	"secret/use-failed":     Failed,

	"settings/not-live":   Invalid,
	"settings/overridden": Busy,
	"settings/unsaved":    Failed,

	"storage/authority":      Config,
	"storage/busy":           Busy,
	"storage/invalid":        Invalid,
	"storage/record-version": Config,
	"storage/undecodable":    Failed,

	"workspace/exists":          Busy,
	"workspace/invalid":         Invalid,
	"workspace/not-a-directory": Invalid,
	"workspace/not-found":       Invalid,

	"sharing/claimed":      Config,
	"sharing/disabled":     Denied,
	"sharing/no-endpoint":  Unavailable,
	"sharing/off":          Denied,
	"sharing/released":     Cancelled,
	"sharing/unregistered": Invalid,
	"sharing/withdrawn":    Cancelled,

	"subprocess/bad-argument":     Invalid,
	"subprocess/batch-refused":    Denied,
	"subprocess/empty-argv":       Invalid,
	"subprocess/line-too-long":    Limit,
	"subprocess/not-found":        Unavailable,
	"subprocess/relative-program": Invalid,
	"subprocess/start-failed":     Failed,
	"subprocess/tree-owner":       Failed,
	"subprocess/tree-survivors":   Failed,
}

// Status maps err to its exit status: nil is OK, a cancelled context Cancelled, an
// expired deadline Limit, and a coded error the status of its innermost code.
func Status(err error) int {
	if err == nil {
		return OK
	}
	var exit exitError
	if errors.As(err, &exit) {
		return int(exit)
	}
	if r, ok := herr.ReasonOf(err); ok {
		if status, ok := codes[r]; ok {
			return status
		}
		return Failed
	}
	switch {
	case errors.Is(err, context.Canceled):
		return Cancelled
	case errors.Is(err, context.DeadlineExceeded):
		return Limit
	}
	return Failed
}

// Known reports whether the table lists the code.
func Known(r herr.Reason) bool {
	_, ok := codes[r]
	return ok
}

// exitError is a status whose diagnostics were already printed.
type exitError int

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// Exit returns status as an error that Status maps back to it and Result.Fail
// does not print: the run that returned it (a profile launch) already printed its
// own diagnostics. Status 0 is nil.
func Exit(status int) error {
	if status == OK {
		return nil
	}
	return exitError(status)
}

// reported reports whether err is an Exit status.
func reported(err error) bool {
	var exit exitError
	return errors.As(err, &exit)
}
