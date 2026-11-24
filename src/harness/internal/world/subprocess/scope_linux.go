//go:build linux

package subprocess

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// scopeRequest is the unit name of the scope request under way, nil otherwise.
// The request's own launch (systemd-run) is recognised by that name in its
// arguments and owned by a plain process group, while every other start waits
// for the choice (requestingOwner).
var scopeRequest atomic.Pointer[string]

// scopeLaunch is the sandbox plan of the scope request's launch.
var scopeLaunch = sandbox.Trusted("subprocess: systemd-run and busctl from the system, asked once at provider start for the host's delegated scope")

// prepareOwner makes the provider start's one choice of the parent group: when
// the host's own control group is not writable, the user service manager is
// asked for a delegated scope first (DH-ADR-EXEC-2, U-K1-1).
func prepareOwner(ctx context.Context, o ProviderOptions) {
	um := o.UserManager
	if um == nil {
		um = SystemdUserManager{}
	}
	cgroupOnce.Do(func() {
		chooseBase(func() (string, error) { return requestScope(ctx, um, o.ScopeTimeout) })
	})
}

func delegatedScope() string {
	probeCgroup()
	return cgroupScope
}

// requestScope asks um once, within timeout, for a delegated scope holding this
// process, and returns its unit name once this process is inside it.
func requestScope(ctx context.Context, um UserManager, timeout time.Duration) (string, error) {
	var nonce [6]byte
	_, _ = rand.Read(nonce[:])
	unit := fmt.Sprintf("dsh-%d-%s.scope", os.Getpid(), hex.EncodeToString(nonce[:]))
	scopeRequest.Store(&unit)
	defer scopeRequest.Store(nil)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := um.DelegateScope(ctx, unit, os.Getpid())
	if err == nil {
		err = awaitScope(ctx, unit)
	}
	switch {
	case err == nil:
		return unit, nil
	case ctx.Err() != nil:
		return "", fmt.Errorf("the user service manager did not grant a delegated scope within %s (U-K1-1): %w", timeout, err)
	default:
		return "", fmt.Errorf("the user service manager refused a delegated scope (U-K1-1): %w", err)
	}
}

// awaitScope waits until this process's control group is the unit's: the
// manager moves the process when it runs the start job, which may come just after
// its answer.
func awaitScope(ctx context.Context, unit string) error {
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	for {
		dir, err := ownCgroupDir()
		if err == nil && filepath.Base(dir) == unit {
			return nil
		}
		select {
		case <-ctx.Done():
			if err == nil {
				err = fmt.Errorf("this process is in %s, not in %s", dir, unit)
			}
			return errors.Join(context.Cause(ctx), err)
		case <-t.C:
		}
	}
}

// SystemdUserManager is the user's systemd instance. DelegateScope runs, through
// Run as a trusted launch,
//
//	systemd-run --user --scope --quiet --collect --unit=<unit> -p Delegate=yes -- \
//	    busctl --user call org.freedesktop.systemd1 /org/freedesktop/systemd1 \
//	    org.freedesktop.systemd1.Manager AttachProcessesToUnit ssau <unit> "" 1 <pid>
//
// systemd-run can put only itself in a new scope, so the command it runs there
// moves pid into the same scope; the scope then outlives the command and holds
// the host. Nothing is wrapped in systemd-run per command. Env is the
// environment both programs get; nil keeps only PATH, XDG_RUNTIME_DIR and
// DBUS_SESSION_BUS_ADDRESS of the host's, with XDG_RUNTIME_DIR set to
// /run/user/<uid> when the host has none and that directory exists.
type SystemdUserManager struct{ Env Env }

// DelegateScope asks the user's systemd instance for the scope.
func (m SystemdUserManager) DelegateScope(ctx context.Context, unit string, pid int) error {
	env := m.Env
	if env == nil {
		env = userBusEnv()
	}
	run, err := Lookup("systemd-run", env)
	if err != nil {
		return err
	}
	bus, err := Lookup("busctl", env)
	if err != nil {
		return err
	}
	argv := []string{run, "--user", "--scope", "--quiet", "--collect", "--unit=" + unit, "-p", "Delegate=yes", "--",
		bus, "--user", "call", "org.freedesktop.systemd1", "/org/freedesktop/systemd1",
		"org.freedesktop.systemd1.Manager", "AttachProcessesToUnit", "ssau", unit, "", "1", strconv.Itoa(pid)}
	res, err := Run(ctx, Spec{Argv: argv, Env: env, Launch: scopeLaunch}, RunOptions{MaxOutput: 64 << 10})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(string(res.Stderr.Bytes))
		if msg == "" {
			msg = strings.TrimSpace(string(res.Stdout.Bytes))
		}
		return fmt.Errorf("systemd-run exited with %d: %s", res.ExitCode, msg)
	}
	return nil
}

// userBusEnv is the host's environment reduced to what reaches the user bus.
func userBusEnv() Env {
	var env Env
	for _, name := range []string{"PATH", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS"} {
		if v, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+v)
		}
	}
	if !env.Has("XDG_RUNTIME_DIR") {
		dir := "/run/user/" + strconv.Itoa(os.Getuid())
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			env = append(env, "XDG_RUNTIME_DIR="+dir)
		}
	}
	return env
}

// requestingOwner is the owner handed out while the scope request is under way.
// The request's own launch, which names the unit, gets a process group; any
// other start waits for the choice and then gets the chosen rung.
type requestingOwner struct {
	unit string
	treeOwner
}

func (o *requestingOwner) start(build func() *exec.Cmd) (*exec.Cmd, error) {
	if slices.Contains(build().Args, o.unit) {
		o.treeOwner = &groupOwner{}
	} else {
		o.treeOwner = chosenOwner()
	}
	return o.treeOwner.start(build)
}

func (o *requestingOwner) release() {
	if o.treeOwner != nil {
		o.treeOwner.release()
	}
}

// ownerCgroup is the control group directory of a started tree; "" on the
// process-group rung.
func ownerCgroup(o treeOwner) string {
	if r, ok := o.(*requestingOwner); ok {
		o = r.treeOwner
	}
	if c, ok := o.(*cgroupOwner); ok && !c.fell {
		return c.dir
	}
	return ""
}
