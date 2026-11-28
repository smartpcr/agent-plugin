//go:build e2e

package e2e

// Step definitions of
// dh_s_18_acceptance_on_real_operating_systems_durable_replace_under_power_loss.feature
// (DH-S-18 Stage 8.9, impl.md 18.11: DH-E-NODEFS-1, TC-21s5).
//
// Each scenario runs the real TestDurableReplaceSurvivesPowerLoss
// (internal/testkit/scenarios, -tags scenario) as its controller and ack
// recorder on this host, the hypervisor host, against one guest OS, and then
// judges the recorder's append-only version log (<report>.detail) itself:
// 100 rounds, each cut after a new acknowledgement and read after its restart,
// every read version complete, no older than the acknowledged bound recomputed
// from the log's own ack lines, and no fail line anywhere in the run.
//
// The guests (proof service:vm-power-cut) are team-kept hypervisors (Linux on
// KVM/libvirt, Windows on Hyper-V; architecture.md: named, pre-existing
// infrastructure, reached only from acceptance rows behind their preflight).
// A real power cut of a real guest cannot be stood up in-process, so each guest
// OS is the provided instance: DSH_E2E_NODEFS_LINUX_GUEST and
// DSH_E2E_NODEFS_WINDOWS_GUEST each name a KEY=VALUE file (runbook-power-cut.md
// step 4) holding that guest's DSH_SCENARIO_VMS (one guest),
// DSH_SCENARIO_NODEFS_LISTEN, _START, _POWEROFF, _POWERON and _READ, plus
//
//	DSH_E2E_NODEFS_PREFLIGHT  creates, cuts and deletes a throw-away guest
//	DSH_E2E_NODEFS_OVERLAY    creates {vm} fresh from a disposable overlay
//	DSH_E2E_NODEFS_CLEANUP    deletes {vm}'s overlay, definition and disk
//
// and optionally DSH_SCENARIO_REPORT (where the report and version log are
// kept; a fresh temporary directory otherwise). A guest OS with no file fails
// its preflight and its scenario (DH-O-15: a scenario that cannot run never
// skips). A passing guest's overlay is deleted; a failing one's is kept and
// named, with the version log, which is always kept.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

const (
	drpScenarioID   = "DH-E-NODEFS-1"
	drpScenarioTest = "TestDurableReplaceSurvivesPowerLoss"
	drpScenarioPkg  = "./internal/testkit/scenarios/"
	drpRounds       = 100

	drpPreflightKey = "DSH_E2E_NODEFS_PREFLIGHT"
	drpOverlayKey   = "DSH_E2E_NODEFS_OVERLAY"
	drpCleanupKey   = "DSH_E2E_NODEFS_CLEANUP"

	// drpMaxRun is the runbook's bound for one run (-timeout 175m).
	drpMaxRun = 175 * time.Minute
	// drpMinRun: less than this cannot hold 100 power cuts and restarts.
	drpMinRun = 20 * time.Minute
	// drpReserve is kept back from the outer go test deadline for the checks.
	drpReserve = 2 * time.Minute
	// drpCommandWithin bounds one preflight, overlay or cleanup command.
	drpCommandWithin = 15 * time.Minute
)

// drpGuests maps the scenario's guest OS to the variable naming its controller.
var drpGuests = map[string]struct{ env, hypervisor string }{
	"linux":   {"DSH_E2E_NODEFS_LINUX_GUEST", "KVM/libvirt"},
	"windows": {"DSH_E2E_NODEFS_WINDOWS_GUEST", "Hyper-V"},
}

// drpRequired are the controller inputs TestDurableReplaceSurvivesPowerLoss
// needs, besides DSH_SCENARIO_REPORT, which the suite sets.
var drpRequired = []string{
	"DSH_SCENARIO_VMS",
	"DSH_SCENARIO_NODEFS_LISTEN",
	"DSH_SCENARIO_NODEFS_START",
	"DSH_SCENARIO_NODEFS_POWEROFF",
	"DSH_SCENARIO_NODEFS_POWERON",
	"DSH_SCENARIO_NODEFS_READ",
	drpPreflightKey,
	drpOverlayKey,
	drpCleanupKey,
}

var drpOuterT *testing.T

type drpState struct {
	guest   string
	cfgPath string
	cfg     map[string]string
	vm      string
	overlay bool // the overlay was created and is the suite's to delete

	report string // DSH_SCENARIO_REPORT given to the run
	detail string // the recorder's version log: report + ".detail"
	runOut string
	runErr error
	log    []drpEvent // the version log's events of this run
}

// drpEvent is one version log line: "<sequence> <UTC time> <event> key=value...".
type drpEvent struct {
	seq  int
	ev   string
	kv   map[string]string
	line string
}

// --- configuration ---

func drpLoadConfig(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("%s:%d: want KEY=VALUE, got %q", path, n, line)
		}
		cfg[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return cfg, sc.Err()
}

// drpLocalProbe says what this host itself offers, for the preflight failure.
func drpLocalProbe(guest string) string {
	switch guest {
	case "linux":
		if p, err := exec.LookPath("virsh"); err == nil {
			return "virsh is at " + p + ", but no controller file names a guest, its overlay and its preflight"
		}
		return "no virsh on this host and no KVM/libvirt host is named"
	default:
		if runtime.GOOS == "windows" {
			if _, err := os.Stat(filepath.Join(os.Getenv("SystemRoot"), "System32", "vmms.exe")); err == nil {
				return "Hyper-V is installed here, but no controller file names a guest, its overlay and its preflight"
			}
		}
		return "no Hyper-V on this host and no Hyper-V host is named"
	}
}

// --- shell ---

// drpShellArgv mirrors the scenario package's platform shell: sh -c, or cmd
// on a batch file on Windows.
func drpShellArgv(line string) ([]string, func(), error) {
	if runtime.GOOS != "windows" {
		return []string{"sh", "-c", line}, func() {}, nil
	}
	f, err := os.CreateTemp("", "dsh-drp-*.cmd")
	if err != nil {
		return nil, nil, err
	}
	_, err = f.WriteString("@echo off\r\n" + line + "\r\n@exit /b %errorlevel%\r\n")
	if err = errors.Join(err, f.Close()); err != nil {
		_ = os.Remove(f.Name())
		return nil, nil, err
	}
	return []string{"cmd", "/d", "/c", f.Name()}, func() { _ = os.Remove(f.Name()) }, nil
}

func (s *drpState) shell(key string) (string, error) {
	line := strings.ReplaceAll(s.cfg[key], "{vm}", s.vm)
	argv, done, err := drpShellArgv(line)
	if err != nil {
		return "", err
	}
	defer done()
	ctx, cancel := context.WithTimeout(context.Background(), drpCommandWithin)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.WaitDelay = 10 * time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s (%s): %w\n%s", key, line, err, out)
	}
	return string(out), nil
}

// --- Given ---

func (s *drpState) givenGuest(guest string) error {
	s.guest = guest
	g := drpGuests[guest]
	s.cfgPath = os.Getenv(g.env)
	if s.cfgPath == "" {
		return fmt.Errorf("not run (vm-power-cut preflight failed): %s is not set, so no %s guest is reachable from this host (%s); "+
			"name the %s controller file of runbook-power-cut.md step 4 (%s) in %s",
			g.env, g.hypervisor, drpLocalProbe(guest), g.hypervisor, strings.Join(drpRequired, ", "), g.env)
	}
	cfg, err := drpLoadConfig(s.cfgPath)
	if err != nil {
		return fmt.Errorf("not run (vm-power-cut preflight failed): the controller file %s: %w", s.cfgPath, err)
	}
	var missing []string
	for _, k := range drpRequired {
		if cfg[k] == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("not run (vm-power-cut preflight failed): %s lacks %s", s.cfgPath, strings.Join(missing, ", "))
	}
	if r := cfg["DSH_SCENARIO_NODEFS_ROUNDS"]; r != "" && r != "all" {
		return fmt.Errorf("%s sets DSH_SCENARIO_NODEFS_ROUNDS=%s: this scenario runs all %d cuts in one run (a shard is the runbook's manual run)", s.cfgPath, r, drpRounds)
	}
	s.cfg = cfg
	var vms []string
	for _, vm := range strings.Split(cfg["DSH_SCENARIO_VMS"], ";") {
		if vm = strings.TrimSpace(vm); vm != "" {
			vms = append(vms, vm)
		}
	}
	if len(vms) != 1 {
		return fmt.Errorf("%s: DSH_SCENARIO_VMS lists %q, want exactly the one %s guest of this scenario", s.cfgPath, vms, g.hypervisor)
	}
	s.vm = vms[0]
	off := cfg["DSH_SCENARIO_NODEFS_POWEROFF"]
	switch guest {
	case "linux":
		if !strings.Contains(off, "virsh destroy") {
			return fmt.Errorf("%s: DSH_SCENARIO_NODEFS_POWEROFF %q is not a libvirt hard power-off (virsh destroy)", s.cfgPath, off)
		}
	case "windows":
		if !strings.Contains(off, "Stop-VM") || !strings.Contains(off, "-TurnOff") {
			return fmt.Errorf("%s: DSH_SCENARIO_NODEFS_POWEROFF %q is not a Hyper-V hard power-off (Stop-VM -TurnOff)", s.cfgPath, off)
		}
	}
	if _, err := s.shell(drpPreflightKey); err != nil {
		return fmt.Errorf("not run (vm-power-cut preflight failed): %w", err)
	}
	if _, err := s.shell(drpOverlayKey); err != nil {
		return fmt.Errorf("the disposable overlay of %s: %w", s.vm, err)
	}
	s.overlay = true
	return nil
}

func (s *drpState) givenRecorder() error {
	if s.cfg == nil {
		return errors.New("no guest configuration")
	}
	addr := s.cfg["DSH_SCENARIO_NODEFS_LISTEN"]
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("not run (ack-recorder preflight failed): DSH_SCENARIO_NODEFS_LISTEN %s is not local and free on this host: %w", addr, err)
	}
	return ln.Close()
}

// --- When ---

func drpModuleDir() (string, error) {
	dir, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return "", fmt.Errorf("the harness module at %s: %w", dir, err)
	}
	return dir, nil
}

func (s *drpState) budget() (time.Duration, error) {
	budget := drpMaxRun
	if drpOuterT != nil {
		if d, ok := drpOuterT.Deadline(); ok {
			budget = min(budget, time.Until(d)-drpReserve)
		}
	}
	if budget < drpMinRun {
		return 0, fmt.Errorf("only %v left before the go test deadline for %d power cuts of %s, below %v: rerun with a larger -timeout (the runbook uses 175m)",
			budget.Round(time.Second), drpRounds, s.vm, drpMinRun)
	}
	return budget, nil
}

func (s *drpState) whenCuts() error {
	if s.cfg == nil {
		return errors.New("no guest configuration")
	}
	mod, err := drpModuleDir()
	if err != nil {
		return err
	}
	budget, err := s.budget()
	if err != nil {
		return err
	}
	if s.report = s.cfg["DSH_SCENARIO_REPORT"]; s.report == "" {
		dir, err := os.MkdirTemp("", "dsh-nodefs-"+s.guest+"-")
		if err != nil {
			return err
		}
		s.report = filepath.Join(dir, "report.jsonl")
	}
	s.detail = s.report + ".detail"

	env := []string{}
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(strings.ToUpper(k), "DSH_SCENARIO_") || strings.EqualFold(k, "DSH_REQUIRE_SCENARIOS") || strings.EqualFold(k, "GOWORK") {
			continue
		}
		env = append(env, kv)
	}
	for k, v := range s.cfg {
		if strings.HasPrefix(k, "DSH_SCENARIO_") && k != "DSH_SCENARIO_REPORT" && k != "DSH_SCENARIO_NODEFS_ROUNDS" {
			env = append(env, k+"="+v)
		}
	}
	env = append(env, "DSH_SCENARIO_REPORT="+s.report, "DSH_REQUIRE_SCENARIOS="+drpScenarioID, "GOWORK=off")

	// The child's own -timeout fires before the kill.
	child := budget - time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-tags", "scenario", "-p", "1", "-count=1",
		"-timeout", child.Round(time.Second).String(), "-v", "-run", "^"+drpScenarioTest+"$", drpScenarioPkg)
	cmd.Dir, cmd.Env, cmd.WaitDelay = mod, env, 30*time.Second
	out, runErr := cmd.CombinedOutput()
	s.runOut, s.runErr = string(out), runErr
	if f, err := os.Create(s.report + ".log"); err == nil {
		_, _ = f.Write(out)
		_ = f.Close()
	}
	if drpOuterT != nil {
		drpOuterT.Logf("DH-E-NODEFS-1 %s guest %s: report %s, version log %s, output %s.log (exit %v)", s.guest, s.vm, s.report, s.detail, s.report, runErr)
	}
	data, err := os.ReadFile(s.detail)
	if err != nil {
		return errors.Join(fmt.Errorf("the recorder's version log %s: %w", s.detail, err), s.runFailure())
	}
	s.log = drpRun(drpParse(data))
	return nil
}

func (s *drpState) runFailure() error {
	if s.runErr == nil {
		return nil
	}
	tail := s.runOut[max(0, len(s.runOut)-4000):]
	return fmt.Errorf("%s on %s: %v (keep the overlay of %s)\n...\n%s", drpScenarioTest, s.vm, s.runErr, s.vm, tail)
}

// drpParse reads the version log's event lines; others are ignored.
func drpParse(data []byte) []drpEvent {
	var out []drpEvent
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 {
			continue
		}
		seq, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		if _, err := time.Parse(time.RFC3339Nano, f[1]); err != nil {
			continue
		}
		kv := map[string]string{}
		for _, field := range f[3:] {
			if k, v, ok := strings.Cut(field, "="); ok {
				kv[k] = strings.TrimSuffix(v, ":")
			}
		}
		out = append(out, drpEvent{seq: seq, ev: f[2], kv: kv, line: sc.Text()})
	}
	return out
}

// drpRun keeps the events of the log's last run: from its last "start
// rounds=1-..." line (a log kept under a provided DSH_SCENARIO_REPORT may hold
// earlier runs).
func drpRun(evs []drpEvent) []drpEvent {
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].ev == "start" && strings.HasPrefix(evs[i].kv["rounds"], "1-") {
			return evs[i:]
		}
	}
	return evs
}

// --- Then ---

func (s *drpState) thenPassed(id, want string) error {
	if s.report == "" {
		return errors.New("the scenario did not run")
	}
	if err := s.runFailure(); err != nil {
		return err
	}
	if !strings.Contains(s.runOut, "--- PASS: "+drpScenarioTest) || strings.Contains(s.runOut, "--- SKIP: "+drpScenarioTest) {
		return fmt.Errorf("%s did not pass (skipped or not run):\n%s", drpScenarioTest, s.runOut[max(0, len(s.runOut)-4000):])
	}
	data, err := os.ReadFile(s.report)
	if err != nil {
		return fmt.Errorf("the report %s: %w", s.report, err)
	}
	got := ""
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		var row map[string]any
		if json.Unmarshal(sc.Bytes(), &row) == nil && row["kind"] == "scenario" && row["id"] == id {
			got, _ = row["result"].(string)
		}
	}
	if got != want {
		return fmt.Errorf("report %s: %s row reads %q, want %q", s.report, id, got, want)
	}
	return nil
}

// drpRound is one round of the guest as the version log records it.
type drpRound struct {
	round, cutAcked, readVersion, readAcked int
	before                                  int // the acknowledged version when the round began
	cut, read                               bool
}

// rounds walks the run's events and recomputes the acknowledged bound from
// the ack lines alone.
func (s *drpState) rounds() (map[int]*drpRound, []string, error) {
	if len(s.log) == 0 {
		return nil, nil, fmt.Errorf("the version log %s holds no event of this run", s.detail)
	}
	if s.log[0].ev != "start" || !strings.Contains(";"+s.log[0].kv["vms"]+";", ";"+s.vm+";") {
		return nil, nil, fmt.Errorf("the version log %s does not start a run of %s: %q", s.detail, s.vm, s.log[0].line)
	}
	var bad []string
	rounds := map[int]*drpRound{}
	acked, cur, prevSeq := 0, (*drpRound)(nil), 0
	for _, e := range s.log {
		if e.seq <= prevSeq {
			bad = append(bad, fmt.Sprintf("sequence not increasing at %q", e.line))
		}
		prevSeq = e.seq
		if e.ev == "fail" || e.ev == "junk" {
			bad = append(bad, e.line)
			continue
		}
		if vm := e.kv["vm"]; vm != s.vm {
			continue
		}
		n := func(k string) int { v, _ := strconv.Atoi(e.kv[k]); return v }
		switch e.ev {
		case "seed":
			acked = max(acked, n("acked"))
		case "ack":
			acked = max(acked, n("version"))
		case "round":
			r := n("round")
			if rounds[r] != nil {
				bad = append(bad, fmt.Sprintf("round %d begun twice", r))
			}
			cur = &drpRound{round: r, before: acked}
			rounds[r] = cur
		case "cut":
			if cur == nil || cur.round != n("round") || cur.cut {
				bad = append(bad, fmt.Sprintf("cut out of order: %q", e.line))
				continue
			}
			cur.cut, cur.cutAcked = true, n("acked")
			if cur.cutAcked != acked {
				bad = append(bad, fmt.Sprintf("round %d cut records acked=%d, the log's acks reach %d", cur.round, cur.cutAcked, acked))
			}
		case "read":
			if cur == nil || cur.round != n("round") || !cur.cut || cur.read {
				bad = append(bad, fmt.Sprintf("read without its cut: %q", e.line))
				continue
			}
			cur.read, cur.readVersion, cur.readAcked = true, n("version"), n("acked")
			if cur.readAcked != acked {
				bad = append(bad, fmt.Sprintf("round %d judged against acked=%d, the log's acks reach %d", cur.round, cur.readAcked, acked))
			}
		}
	}
	return rounds, bad, nil
}

func (s *drpState) thenRounds(n int) error {
	rounds, bad, err := s.rounds()
	if err != nil {
		return err
	}
	if len(bad) > 0 {
		return fmt.Errorf("the version log %s of %s: %s (keep the overlay of %s)", s.detail, s.vm, strings.Join(bad, "; "), s.vm)
	}
	if len(rounds) != n {
		return fmt.Errorf("the version log %s records %d rounds of %s, want %d", s.detail, len(rounds), s.vm, n)
	}
	for r := 1; r <= n; r++ {
		x := rounds[r]
		switch {
		case x == nil:
			return fmt.Errorf("round %d of %s is missing from %s", r, s.vm, s.detail)
		case !x.cut || !x.read:
			return fmt.Errorf("round %d of %s: cut %v, read after its restart %v", r, s.vm, x.cut, x.read)
		case x.cutAcked <= x.before:
			return fmt.Errorf("round %d of %s was cut at acked=%d with no new acknowledgement since %d: the fsx.Replace loop was not running", r, s.vm, x.cutAcked, x.before)
		}
	}
	return nil
}

func (s *drpState) thenNeverOlder() error {
	rounds, bad, err := s.rounds()
	if err != nil {
		return err
	}
	if len(bad) > 0 {
		return fmt.Errorf("the version log %s of %s: %s", s.detail, s.vm, strings.Join(bad, "; "))
	}
	for r := 1; r <= drpRounds; r++ {
		x := rounds[r]
		if x == nil || !x.read {
			return fmt.Errorf("round %d of %s has no judged read", r, s.vm)
		}
		// A read line is written only for a file that is one complete version
		// (header, length and SHA-256 of its payload match); here it must be no
		// older than the bound and at most the one version in flight past it.
		if x.readVersion < x.readAcked || x.readVersion > x.readAcked+1 {
			return fmt.Errorf("round %d of %s read version %d against the acknowledged %d", r, s.vm, x.readVersion, x.readAcked)
		}
	}
	return nil
}

// --- registration ---

func InitializeScenario_dh_s_18_acceptance_on_real_operating_systems_durable_replace_under_power_loss(ctx *godog.ScenarioContext) {
	s := &drpState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = drpState{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if !s.overlay {
			return c, nil
		}
		if err != nil {
			if drpOuterT != nil {
				drpOuterT.Logf("DH-E-NODEFS-1 %s guest failed: keeping the overlay of %s and the version log %s", s.guest, s.vm, s.detail)
			}
			return c, nil
		}
		if _, cerr := s.shell(drpCleanupKey); cerr != nil {
			return c, fmt.Errorf("deleting the passing overlay of %s: %w", s.vm, cerr)
		}
		return c, nil
	})

	ctx.Step(`^a libvirt guest from a disposable overlay, whose vm-power-cut preflight creates, cuts and deletes a throw-away guest$`,
		func() error { return s.givenGuest("linux") })
	ctx.Step(`^a Hyper-V guest from a disposable overlay, whose vm-power-cut preflight creates, cuts and deletes a throw-away guest$`,
		func() error { return s.givenGuest("windows") })
	ctx.Step(`^the ack recorder's address on this host, the hypervisor host, is local and free$`, s.givenRecorder)
	ctx.Step(`^it is powered off (\d+) times during fsx\.Replace loops$`, func(n int) error {
		if n != drpRounds {
			return fmt.Errorf("DH-E-NODEFS-1 cuts %d times, not %d", drpRounds, n)
		}
		return s.whenCuts()
	})
	ctx.Step(`^the same (\d+) cuts run with Stop-VM -TurnOff$`, func(n int) error {
		if n != drpRounds {
			return fmt.Errorf("DH-E-NODEFS-1 cuts %d times, not %d", drpRounds, n)
		}
		return s.whenCuts()
	})
	ctx.Step(`^the scenario run passed and its report row for "([^"]+)" reads "([^"]+)"$`, s.thenPassed)
	ctx.Step(`^the version log records (\d+) rounds of the guest, each cut after a new acknowledgement and read after its restart$`, s.thenRounds)
	ctx.Step(`^(?:the same holds: )?after every restart the file is complete and never older than the recorder's last acknowledged version$`, s.thenNeverOlder)
}

func TestE2E_dh_s_18_acceptance_on_real_operating_systems_durable_replace_under_power_loss(t *testing.T) {
	drpOuterT = t
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_18_acceptance_on_real_operating_systems_durable_replace_under_power_loss,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_18_acceptance_on_real_operating_systems_durable_replace_under_power_loss.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("DH-S-18 Stage 8.9 durable replace under power loss scenarios failed")
	}
}
