//go:build e2e

package e2e

// Step definitions of
// dh_s_18_acceptance_on_real_operating_systems_durable_replace_under_power_loss.feature
// (DH-S-18 Stage 8.9, impl.md 18.11: DH-E-NODEFS-1, TC-21s5).
//
// Each scenario runs the real TestDurableReplaceSurvivesPowerLoss
// (internal/testkit/scenarios, -tags scenario) as its controller and ack
// recorder on this host, the hypervisor controller, against one guest OS, and
// then judges the recorder's append-only version log (<report>.detail) itself:
// 100 rounds, each cut after a new acknowledgement and read after its restart,
// every read version complete, no older than the acknowledged bound recomputed
// from the log's own ack lines, and no fail line anywhere in the run.
//
// The guests (proof service:vm-power-cut) are team-kept hypervisors (Linux on
// KVM/libvirt, Windows on Hyper-V; architecture.md: named, pre-existing
// infrastructure, reached only from acceptance rows behind their preflight).
// A hard power cut of a real guest cannot be stood up in-process, so each guest
// OS is the provided instance, bound by the same per-hypervisor inputs as the
// vm:* s18 jobs of harness-scenarios.yml (runbook-power-cut.md step 2 and
// "Through the Gherkin suite"): DSH_S18_KVM_* for the Linux guest and
// DSH_S18_HYPERV_* for the Windows guest, each with NODEFS_LISTEN,
// NODEFS_START, NODEFS_POWEROFF, NODEFS_POWERON, NODEFS_READ, BASE_IMAGES,
// PROBE_CREATE, PROBE_SHOW, PROBE_ACK and PROBE_DELETE. The Given step runs the
// job's power-cut preflight itself (base images checksummed, a throw-away guest
// created from an overlay, shown by the hypervisor, cut, restarted, its test
// acknowledgment recorded and read back by harness-gate ack-probe, deleted, the
// base images unchanged), then creates the run's guest the same way: a fresh
// overlay, cut and restarted so the writer is not running. A guest OS whose
// inputs are not set fails its preflight and its scenario (DH-O-15: a scenario
// that cannot run never skips). A passing guest is deleted with PROBE_DELETE; a
// failing one is kept and named, with the version log, which is always kept
// (under DSH_E2E_NODEFS_ARTIFACTS when set).

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

	// drpArtifactsEnv optionally names where each guest's report, version log
	// and run output are kept (a fresh temporary directory otherwise).
	drpArtifactsEnv = "DSH_E2E_NODEFS_ARTIFACTS"

	// drpMaxRun is the runbook's bound for one run (-timeout 175m).
	drpMaxRun = 175 * time.Minute
	// drpMinRun: less than this cannot hold 100 power cuts and restarts.
	drpMinRun = 20 * time.Minute
	// drpReserve is kept back from the outer go test deadline for the checks.
	drpReserve = 2 * time.Minute
	// drpCommandWithin bounds one hypervisor command or the scenario build.
	drpCommandWithin = 15 * time.Minute
	// drpAckWithin is the ack-probe timeout of the vm:* jobs.
	drpAckWithin = 2 * time.Minute
)

// drpGuests maps the scenario's guest OS to its hypervisor's inputs.
var drpGuests = map[string]struct {
	target, hypervisor string
	images             []string // base image extensions the hypervisor takes
}{
	"linux":   {"KVM", "KVM/libvirt", []string{".qcow2", ".img", ".raw"}},
	"windows": {"HYPERV", "Hyper-V", []string{".vhdx", ".vhd"}},
}

// drpShown is the vm:* job's check that PROBE_SHOW shows guest on target.
func drpShown(target, guest, shown string) bool {
	q := regexp.QuoteMeta(guest)
	var pats []string
	switch target {
	case "KVM": // virsh dominfo
		pats = []string{`(?m)^Name: +` + q + `\s*$`, `(?m)^OS Type: +hvm`}
	case "HYPERV": // Get-VM | Format-List Name,Generation,State
		pats = []string{`(?m)^Name +: +` + q + `\s*$`, `(?m)^Generation +: +[12]`}
	}
	for _, p := range pats {
		if !regexp.MustCompile(p).MatchString(shown) {
			return false
		}
	}
	return len(pats) > 0
}

// drpInputs are the per-hypervisor inputs, as DSH_S18_<TARGET>_<key>.
var drpInputs = []string{
	"NODEFS_LISTEN", "NODEFS_START", "NODEFS_POWEROFF", "NODEFS_POWERON", "NODEFS_READ",
	"BASE_IMAGES", "PROBE_CREATE", "PROBE_SHOW", "PROBE_ACK", "PROBE_DELETE",
}

var drpOuterT *testing.T

type drpState struct {
	guest  string
	target string
	in     map[string]string // drpInputs -> value
	vm     string            // the run's guest, created from a fresh overlay
	made   bool              // the run's guest exists and is the suite's to delete

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

func drpName(prefix string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%d-%s", prefix, time.Now().Unix(), hex.EncodeToString(b))
}

// drpLocalProbe says what this host itself offers, for the preflight failure.
func drpLocalProbe(guest string) string {
	if guest == "linux" {
		if p, err := exec.LookPath("virsh"); err == nil {
			return "virsh is at " + p + ", but no KVM inputs are set"
		}
		return "no virsh on this host"
	}
	if runtime.GOOS == "windows" {
		if _, err := os.Stat(filepath.Join(os.Getenv("SystemRoot"), "System32", "vmms.exe")); err == nil {
			return "Hyper-V is installed here, but no Hyper-V inputs are set"
		}
	}
	return "no Hyper-V on this host"
}

// --- shell ---

// sub fills a command template as the vm:* job does.
func (s *drpState) sub(template, guest, token string) string {
	r := strings.NewReplacer("{vm}", guest, "{probe}", guest, "{addr}", s.in["NODEFS_LISTEN"], "{token}", token)
	return r.Replace(template)
}

// run runs line through sh -c, as the vm:* job runs the same inputs.
func drpRun(what, line string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), drpCommandWithin)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", line)
	cmd.WaitDelay = 10 * time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s (%s): %w\n%s", what, line, err, out)
	}
	return string(out), nil
}

func (s *drpState) do(key, guest string) (string, error) {
	return drpRun(fmt.Sprintf("DSH_S18_%s_%s", s.target, key), s.sub(s.in[key], guest, ""))
}

// --- Given ---

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

func drpImageSums(images []string) (string, error) {
	var b strings.Builder
	for _, p := range images {
		f, err := os.Open(p)
		if err != nil {
			return "", fmt.Errorf("base image %s is not readable: %w", p, err)
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		_ = f.Close()
		if err != nil {
			return "", fmt.Errorf("base image %s: %w", p, err)
		}
		fmt.Fprintf(&b, "%x  %s\n", h.Sum(nil), p)
	}
	return b.String(), nil
}

// create makes guest from an overlay and checks the hypervisor shows it.
func (s *drpState) create(guest string) error {
	if _, err := s.do("PROBE_CREATE", guest); err != nil {
		return fmt.Errorf("the guest %s was not created from an overlay: %w", guest, err)
	}
	shown, err := s.do("PROBE_SHOW", guest)
	if err != nil {
		return fmt.Errorf("the guest %s is not shown by %s: %w", guest, drpGuests[s.guest].hypervisor, err)
	}
	if !drpShown(s.target, guest, shown) {
		return fmt.Errorf("%s is not a %s guest:\n%s", guest, drpGuests[s.guest].hypervisor, shown)
	}
	return nil
}

// cycle cuts guest hard and restarts it, waiting until it is reachable.
func (s *drpState) cycle(guest string) error {
	if _, err := s.do("NODEFS_POWEROFF", guest); err != nil {
		return fmt.Errorf("%s was not powered off: %w", guest, err)
	}
	if _, err := s.do("NODEFS_POWERON", guest); err != nil {
		return fmt.Errorf("%s did not restart: %w", guest, err)
	}
	return nil
}

// preflight is the vm:* job's power-cut check (service:vm-power-cut and
// service:ack-recorder; runbook-power-cut.md step 2).
func (s *drpState) preflight() (err error) {
	g := drpGuests[s.guest]
	var images []string
	for _, p := range strings.Split(s.in["BASE_IMAGES"], ";") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		ok := false
		for _, ext := range g.images {
			ok = ok || strings.HasSuffix(strings.ToLower(p), ext)
		}
		if !ok {
			return fmt.Errorf("base image %s is not a %s disk image", p, g.hypervisor)
		}
		images = append(images, p)
	}
	if len(images) == 0 {
		return fmt.Errorf("DSH_S18_%s_BASE_IMAGES lists no image", s.target)
	}
	before, err := drpImageSums(images)
	if err != nil {
		return err
	}
	probe := drpName("dsh-e2e-s18-probe")
	deleted := false
	defer func() {
		if !deleted {
			if _, derr := s.do("PROBE_DELETE", probe); derr != nil {
				err = errors.Join(err, fmt.Errorf("delete %s by hand: %w", probe, derr))
			}
		}
	}()
	if err := s.create(probe); err != nil {
		return err
	}
	if err := s.cycle(probe); err != nil {
		return err
	}
	mod, err := drpModuleDir()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "dsh-e2e-ack-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	token := probe + "-ack"
	ctx, cancel := context.WithTimeout(context.Background(), drpCommandWithin)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "-C", filepath.Join(mod, "..", "..", "tools", "harness-gate"), "run", ".", "ack-probe",
		"--listen", s.in["NODEFS_LISTEN"], "--token", token, "--log", filepath.Join(dir, "s18-ack.log"),
		"--timeout", drpAckWithin.String(), "--send", s.sub(s.in["PROBE_ACK"], probe, token))
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("no acknowledgment from %s was recorded and read back (harness-gate ack-probe): %w\n%s", probe, err, out)
	}
	if _, err := s.do("PROBE_DELETE", probe); err != nil {
		return fmt.Errorf("the probe guest %s was not deleted: %w", probe, err)
	}
	deleted = true
	after, err := drpImageSums(images)
	if err != nil {
		return err
	}
	if after != before {
		return fmt.Errorf("a base image changed:\n%s", after)
	}
	return nil
}

func (s *drpState) givenGuest(guest string) error {
	g := drpGuests[guest]
	s.guest, s.target, s.in = guest, g.target, map[string]string{}
	var missing []string
	for _, k := range drpInputs {
		name := "DSH_S18_" + g.target + "_" + k
		if s.in[k] = strings.TrimSpace(os.Getenv(name)); s.in[k] == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("not run (power-cut preflight failed): no %s guest is reachable from this host (%s); not set: %s (runbook-power-cut.md step 2, environment dsh-m2-s18)",
			g.hypervisor, drpLocalProbe(guest), strings.Join(missing, ", "))
	}
	off := s.in["NODEFS_POWEROFF"]
	switch guest {
	case "linux":
		if !strings.Contains(off, "virsh destroy") {
			return fmt.Errorf("DSH_S18_KVM_NODEFS_POWEROFF %q is not a libvirt hard power-off (virsh destroy)", off)
		}
	case "windows":
		if !strings.Contains(off, "Stop-VM") || !strings.Contains(off, "-TurnOff") {
			return fmt.Errorf("DSH_S18_HYPERV_NODEFS_POWEROFF %q is not a Hyper-V hard power-off (Stop-VM -TurnOff)", off)
		}
	}
	if err := s.preflight(); err != nil {
		return fmt.Errorf("not run (power-cut preflight failed): %w", err)
	}
	// The run's guest: a fresh overlay of the base image, cut once and restarted
	// so it is reachable with no writer running (one must not start at boot).
	s.vm = drpName("dsh-e2e-nodefs-" + guest)
	s.made = true
	if err := s.create(s.vm); err != nil {
		return err
	}
	return s.cycle(s.vm)
}

func (s *drpState) givenRecorder() error {
	if s.in == nil {
		return errors.New("no guest inputs")
	}
	addr := s.in["NODEFS_LISTEN"]
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("not run (ack-recorder preflight failed): DSH_S18_%s_NODEFS_LISTEN %s is not local and free on this host: %w", s.target, addr, err)
	}
	return ln.Close()
}

// --- When ---

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
	if s.vm == "" {
		return errors.New("no guest")
	}
	mod, err := drpModuleDir()
	if err != nil {
		return err
	}
	budget, err := s.budget()
	if err != nil {
		return err
	}
	dir := os.Getenv(drpArtifactsEnv)
	if dir != "" {
		dir = filepath.Join(dir, s.guest)
		err = os.MkdirAll(dir, 0o755)
	} else {
		dir, err = os.MkdirTemp("", "dsh-nodefs-"+s.guest+"-")
	}
	if err != nil {
		return err
	}
	s.report = filepath.Join(dir, "report.jsonl")
	s.detail = s.report + ".detail"

	// A built test binary, run directly: its result is never a cached one.
	bin := filepath.Join(dir, "scenarios.test")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	bctx, bcancel := context.WithTimeout(context.Background(), drpCommandWithin)
	defer bcancel()
	build := exec.CommandContext(bctx, "go", "test", "-c", "-tags", "scenario", "-o", bin, drpScenarioPkg)
	build.Dir, build.Env = mod, append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("go test -c -tags scenario %s: %w\n%s", drpScenarioPkg, err, out)
	}

	env := []string{}
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(strings.ToUpper(k), "DSH_SCENARIO_") || strings.EqualFold(k, "DSH_REQUIRE_SCENARIOS") {
			continue
		}
		env = append(env, kv)
	}
	for _, k := range []string{"NODEFS_LISTEN", "NODEFS_START", "NODEFS_POWEROFF", "NODEFS_POWERON", "NODEFS_READ"} {
		env = append(env, "DSH_SCENARIO_"+k+"="+s.in[k])
	}
	env = append(env, "DSH_SCENARIO_VMS="+s.vm, "DSH_SCENARIO_REPORT="+s.report, "DSH_REQUIRE_SCENARIOS="+drpScenarioID)

	// The child's own -test.timeout fires before the kill.
	child := budget - time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-test.run", "^"+drpScenarioTest+"$", "-test.v", "-test.timeout", child.Round(time.Second).String())
	cmd.Dir, cmd.Env, cmd.WaitDelay = filepath.Join(mod, filepath.FromSlash(drpScenarioPkg)), env, 30*time.Second
	out, runErr := cmd.CombinedOutput()
	s.runOut, s.runErr = string(out), runErr
	_ = os.WriteFile(s.report+".log", out, 0o644)
	_ = os.Remove(bin)
	if drpOuterT != nil {
		drpOuterT.Logf("DH-E-NODEFS-1 %s guest %s: report %s, version log %s, output %s.log (exit %v)", s.guest, s.vm, s.report, s.detail, s.report, runErr)
	}
	data, err := os.ReadFile(s.detail)
	if err != nil {
		return errors.Join(fmt.Errorf("the recorder's version log %s: %w", s.detail, err), s.runFailure())
	}
	s.log = drpLastRun(drpParse(data))
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

// drpLastRun keeps the events of the log's last run: from its last "start
// rounds=1-..." line (a log kept under a provided DSH_SCENARIO_REPORT may hold
// earlier runs).
func drpLastRun(evs []drpEvent) []drpEvent {
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
		if !s.made {
			return c, nil
		}
		if err != nil {
			if drpOuterT != nil {
				drpOuterT.Logf("DH-E-NODEFS-1 %s guest failed: keeping the guest %s and its overlay, and the version log %s", s.guest, s.vm, s.detail)
			}
			return c, nil
		}
		if _, cerr := s.do("PROBE_DELETE", s.vm); cerr != nil {
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
