package fsx

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/procwait"
)

// publisher is one way of publishing a file: plain or rooted, replace or create.
type publisher struct {
	name      string
	exclusive bool
	noLink    bool // the rooted create takes its reservation path
	run       func(t *testing.T, dir, base string, write func(io.Writer) error, opt Options) error
}

func publishers() []publisher {
	rooted := func(fn func(*os.Root, string, func(io.Writer) error, Options) error) func(*testing.T, string, string, func(io.Writer) error, Options) error {
		return func(t *testing.T, dir, base string, write func(io.Writer) error, opt Options) error {
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			return fn(root, base, write, opt)
		}
	}
	plain := func(fn func(string, func(io.Writer) error, Options) error) func(*testing.T, string, string, func(io.Writer) error, Options) error {
		return func(_ *testing.T, dir, base string, write func(io.Writer) error, opt Options) error {
			return fn(filepath.Join(dir, base), write, opt)
		}
	}
	return []publisher{
		{name: "Replace", run: plain(Replace)},
		{name: "CreateIfAbsent", exclusive: true, run: plain(CreateIfAbsent)},
		{name: "ReplaceIn", run: rooted(ReplaceIn)},
		{name: "CreateIfAbsentIn", exclusive: true, run: rooted(CreateIfAbsentIn)},
		{name: "CreateIfAbsentIn without links", exclusive: true, noLink: true, run: rooted(CreateIfAbsentIn)},
	}
}

// noLinks makes the rooted create take its reservation path for the rest of t.
func noLinks(t *testing.T) {
	saved := rootLink
	t.Cleanup(func() { rootLink = saved })
	rootLink = func(*os.Root, string, string) error { return errors.ErrUnsupported }
}

// TestFailureAtEachStep: a failure injected at every step of a publication (staging,
// writing, setting the mode, syncing, closing, publishing) returns that failure,
// leaves the old content (or no file) in place and no staged file behind, for every
// way of publishing.
//
//dsh:test DH-T-FS-1, DH-T-NODEFS-1
func TestFailureAtEachStep(t *testing.T) {
	steps := []string{stepStage, "write", stepMode, stepSync, stepClose, stepPublish}
	for _, p := range publishers() {
		for _, step := range steps {
			t.Run(p.name+"/"+step, func(t *testing.T) {
				if p.noLink {
					noLinks(t)
				}
				dir := t.TempDir()
				target := filepath.Join(dir, "f")
				if !p.exclusive {
					if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				boom := errors.New("injected at " + step)
				saved := faultAt
				t.Cleanup(func() { faultAt = saved })
				reached := false
				faultAt = func(s string) error {
					if s == step {
						reached = true
						return boom
					}
					return nil
				}
				write := Bytes([]byte("new"))
				if step == "write" {
					reached = true
					write = func(w io.Writer) error {
						_, _ = io.WriteString(w, "partial")
						return boom
					}
				}
				err := p.run(t, dir, "f", write, Options{Perm: 0o644, Durable: true})
				if !reached || !errors.Is(err, boom) {
					t.Fatalf("reached %v, err %v; want the injected failure", reached, err)
				}
				got, rerr := os.ReadFile(target)
				switch {
				case p.exclusive && !errors.Is(rerr, fs.ErrNotExist):
					t.Fatalf("a failed create left %q (%v)", got, rerr)
				case !p.exclusive && string(got) != "old":
					t.Fatalf("target is %q, want the old content", got)
				}
				assertNoStaged(t, dir)
			})
		}
	}
}

// TestRootedCreateOneWinner: fifty goroutines race the rooted create-if-absent on
// one name, with links and through the reservation path: exactly one wins, the file
// holds its content, every loser sees fs.ErrExist and nothing staged is left.
//
//dsh:test DH-T-FS-1, DH-T-NODEFS-2
func TestRootedCreateOneWinner(t *testing.T) {
	for _, noLink := range []bool{false, true} {
		t.Run("noLink="+strconv.FormatBool(noLink), func(t *testing.T) {
			if noLink {
				noLinks(t)
			}
			dir := t.TempDir()
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { root.Close() })
			const n = 50
			errs := make([]error, n)
			gate := make(chan struct{})
			var wg sync.WaitGroup
			for i := range n {
				wg.Go(func() {
					<-gate
					errs[i] = CreateIfAbsentIn(root, "claim", Bytes([]byte("writer "+strconv.Itoa(i))), Options{Perm: 0o644})
				})
			}
			close(gate)
			wg.Wait()
			winner := oneWinner(t, errs)
			if got, _ := os.ReadFile(filepath.Join(dir, "claim")); string(got) != "writer "+strconv.Itoa(winner) {
				t.Fatalf("content %q is not the winner's", got)
			}
			assertNoStaged(t, dir)
		})
	}
}

// oneWinner returns the index of the only nil error and fails t unless every other
// error matches fs.ErrExist.
func oneWinner(t *testing.T, errs []error) int {
	t.Helper()
	winner := -1
	for i, err := range errs {
		switch {
		case err == nil && winner >= 0:
			t.Fatalf("writers %d and %d both created the file", winner, i)
		case err == nil:
			winner = i
		case !errors.Is(err, fs.ErrExist):
			t.Errorf("writer %d: %v, want fs.ErrExist", i, err)
		}
	}
	if winner < 0 {
		t.Fatal("no writer created the file")
	}
	return winner
}

// The environment of a child process of TestCreateIfAbsentAcrossProcesses.
const (
	raceDirEnv  = "FSX_TEST_RACE_DIR"
	raceModeEnv = "FSX_TEST_RACE_MODE"
	raceIDEnv   = "FSX_TEST_RACE_ID"
	racePrefix  = "fsx-race: "
	raceWriters = 25
)

// TestCreateIfAbsentAcrossProcesses: two processes (this test binary, re-executed)
// each race 25 goroutines on create-if-absent of one name, plain, rooted, and rooted
// with linking disabled: exactly one writer of the fifty wins across both processes,
// the file holds its content, every loser sees fs.ErrExist and no staged file
// remains.
//
//dsh:test DH-T-FS-1, DH-T-NODEFS-2
func TestCreateIfAbsentAcrossProcesses(t *testing.T) {
	if dir := os.Getenv(raceDirEnv); dir != "" {
		raceChild(t, dir, os.Getenv(raceModeEnv), os.Getenv(raceIDEnv))
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"plain", "rooted", "nolink"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			t.Cleanup(cancel)
			children := make([]*raceProc, 2)
			for k := range children {
				children[k] = startRaceChild(t, ctx, exe, dir, mode, k)
			}
			procwait.Until(ctx, t, func() bool {
				for k := range children {
					if _, err := os.Stat(filepath.Join(dir, "ready-"+strconv.Itoa(k))); err != nil {
						return false
					}
				}
				return true
			})
			if err := os.WriteFile(filepath.Join(dir, "go"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
			var won []string
			for k, c := range children {
				lines, err := c.wait()
				if err != nil {
					t.Fatalf("child %d: %v\n%s", k, err, c.out.String())
				}
				for _, l := range lines {
					if w, ok := strings.CutPrefix(l, "won "); ok {
						won = append(won, w)
					}
				}
				if len(lines) != raceWriters {
					t.Fatalf("child %d reported %d writers, want %d:\n%s", k, len(lines), raceWriters, c.out.String())
				}
			}
			if len(won) != 1 {
				t.Fatalf("%d winners across the processes: %q", len(won), won)
			}
			if got, _ := os.ReadFile(filepath.Join(dir, "claim")); string(got) != won[0] {
				t.Fatalf("content %q is not the winner's %q", got, won[0])
			}
			assertNoStaged(t, dir)
		})
	}
}

// raceProc is one child process of the cross-process race.
type raceProc struct {
	cmd    *exec.Cmd
	out    bytes.Buffer
	once   sync.Once
	result error
}

func startRaceChild(t *testing.T, ctx context.Context, exe, dir, mode string, k int) *raceProc {
	t.Helper()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestCreateIfAbsentAcrossProcesses$", "-test.count=1")
	c := &raceProc{cmd: cmd}
	cmd.Env = append(os.Environ(), raceDirEnv+"="+dir, raceModeEnv+"="+mode, raceIDEnv+"="+strconv.Itoa(k))
	cmd.Stdout, cmd.Stderr = &c.out, &c.out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// P-C: the cleanup kills a child the test did not wait for, then joins it.
	t.Cleanup(func() {
		c.once.Do(func() {
			_ = cmd.Process.Kill()
			c.result = cmd.Wait()
		})
	})
	return c
}

// wait joins the child and returns its report lines.
func (c *raceProc) wait() ([]string, error) {
	c.once.Do(func() { c.result = c.cmd.Wait() })
	if c.result != nil {
		return nil, c.result
	}
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(c.out.Bytes()))
	for sc.Scan() {
		if l, ok := strings.CutPrefix(sc.Text(), racePrefix); ok {
			lines = append(lines, l)
		}
	}
	return lines, nil
}

// raceChild is the child's side: announce readiness, wait for the parent's start
// file, release 25 writers at once and report each outcome on standard output.
func raceChild(t *testing.T, dir, mode, id string) {
	var create func(data []byte) error
	switch mode {
	case "plain":
		create = func(data []byte) error {
			return CreateIfAbsent(filepath.Join(dir, "claim"), Bytes(data), Options{Perm: 0o644})
		}
	case "rooted", "nolink":
		if mode == "nolink" {
			rootLink = func(*os.Root, string, string) error { return errors.ErrUnsupported }
		}
		root, err := os.OpenRoot(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		create = func(data []byte) error {
			return CreateIfAbsentIn(root, "claim", Bytes(data), Options{Perm: 0o644})
		}
	default:
		t.Fatalf("unknown race mode %q", mode)
	}
	if err := os.WriteFile(filepath.Join(dir, "ready-"+id), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	procwait.Until(ctx, t, func() bool {
		_, err := os.Stat(filepath.Join(dir, "go"))
		return err == nil
	})
	errs := make([]error, raceWriters)
	contents := make([]string, raceWriters)
	gate := make(chan struct{})
	var wg sync.WaitGroup
	for i := range raceWriters {
		contents[i] = "proc " + id + " writer " + strconv.Itoa(i)
		wg.Go(func() {
			<-gate
			errs[i] = create([]byte(contents[i]))
		})
	}
	close(gate)
	wg.Wait()
	for i, err := range errs {
		switch {
		case err == nil:
			fmt.Printf("%swon %s\n", racePrefix, contents[i])
		case errors.Is(err, fs.ErrExist):
			fmt.Printf("%slost\n", racePrefix)
		default:
			t.Errorf("writer %d: %v, want nil or fs.ErrExist", i, err)
		}
	}
}

// TestVersionStaleness: with digests, a change of modification time alone is not
// stale, and a rewrite that restores the size and the times is; a version without a
// digest falls back to the metadata token, which does see a time change.
//
//dsh:test DH-T-FS-3
func TestVersionStaleness(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("abcd"), 0o644); err != nil {
		t.Fatal(err)
	}
	read := func() Version {
		t.Helper()
		v, err := Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		v, err = v.WithDigest(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	v0 := read()
	touched := time.Unix(0, v0.ModNs).Add(time.Hour)
	if err := os.Chtimes(path, touched, touched); err != nil {
		t.Fatal(err)
	}
	v1 := read()
	if v1.ModNs == v0.ModNs {
		t.Fatal("the touch did not change the modification time")
	}
	if !v0.Same(v1) {
		t.Fatal("with digests, a change of modification time alone is stale")
	}

	// Rewrite in place with the same size, then put the times back.
	if err := os.WriteFile(path, []byte("wxyz"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, touched, touched); err != nil {
		t.Fatal(err)
	}
	v2 := read()
	if v2.ID != v1.ID || v2.Size != v1.Size || v2.ModNs != v1.ModNs {
		t.Fatalf("the rewrite did not restore identity, size and time: %v then %v", v1, v2)
	}
	if v1.Same(v2) {
		t.Fatal("a rewrite with size and times restored is not stale")
	}

	// Without a digest on either side, the metadata decides.
	meta := func(v Version) Version { v.Digest = ""; return v }
	if !meta(v1).Same(v1) || !v1.Same(meta(v1)) {
		t.Fatal("one side without a digest does not fall back to the metadata")
	}
	if meta(v0).Same(meta(v1)) {
		t.Fatal("the metadata token missed a change of modification time")
	}
	if runtime.GOOS != "windows" && meta(v1).Same(meta(v2)) {
		t.Fatal("the metadata token missed a rewrite (the status-change time moved)")
	}
}

// TestVersionTokenChanges: the metadata token changes after an in-place write and
// after a replacement, and not after a read.
//
//dsh:test DH-T-NODEFS-3
func TestVersionTokenChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	token := func() string {
		t.Helper()
		v, err := Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return v.String()
	}
	before := token()
	if _, err := ReadFile(path); err != nil {
		t.Fatal(err)
	}
	if got := token(); got != before {
		t.Fatalf("a read changed the token: %s then %s", before, got)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(" more"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	inPlace := token()
	if inPlace == before {
		t.Fatal("an in-place write kept the token")
	}
	id, _ := ID(path)
	if err := WriteFile(path, []byte("one more"), Options{}); err != nil {
		t.Fatal(err)
	}
	if got := token(); got == inPlace {
		t.Fatal("a replacement kept the token")
	}
	if after, _ := ID(path); after == id {
		t.Fatal("a replacement kept the file identity")
	}
}

// TestUTF16RoundTrip is the encoding clause of DH-T-FS-27 at fsx level: a UTF-16
// file with a byte-order mark, little- and big-endian, with CRLF endings, decodes,
// takes an edit whose old text matches only in the decoded text, and is written back
// with the same mark, byte order and endings; the version digest is over the bytes on
// disk; UTF-16 without a mark, and a mark followed by an invalid surrogate, are not
// text. The clauses on the read, edit and apply_patch tools are DH-S-12's
// (M1-G-FND-21.1).
//
//dsh:test DH-T-FS-27
func TestUTF16RoundTrip(t *testing.T) {
	for _, enc := range []Encoding{UTF16LE, UTF16BE} {
		t.Run(strconv.Itoa(int(enc)), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "notes.txt")
			text := "first line\r\nsecond ünïcode 🙂\r\nthird\r\n"
			if err := WriteFile(path, Encode(text, enc), Options{Perm: 0o644}); err != nil {
				t.Fatal(err)
			}
			raw, err := ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			mark := raw[:2]
			if !IsText(raw) {
				t.Fatal("UTF-16 with a mark is not text")
			}
			decoded, gotEnc, err := Decode(raw)
			if err != nil || gotEnc != enc || decoded != text {
				t.Fatalf("Decode = %q, %d, %v", decoded, gotEnc, err)
			}
			const old = "second ünïcode"
			if bytes.Contains(raw, []byte(old)) {
				t.Fatal("the old text matches the raw bytes; the case proves nothing")
			}
			eol := DominantEOL(decoded)
			edited := strings.Replace(NormalizeEOL(decoded), old, "second edited", 1)
			if edited == NormalizeEOL(decoded) {
				t.Fatal("the old text did not match the decoded text")
			}
			out := Encode(RestoreEOL(edited, eol), enc)
			if err := WriteFile(path, out, Options{}); err != nil {
				t.Fatal(err)
			}
			back, err := ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			again, againEnc, err := Decode(back)
			want := "first line\r\nsecond edited 🙂\r\nthird\r\n"
			if err != nil || againEnc != enc || !bytes.Equal(back[:2], mark) || again != want {
				t.Fatalf("written back as %q, %d, mark % x, %v", again, againEnc, back[:2], err)
			}
			v, err := Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			v, err = v.WithDigest(bytes.NewReader(back))
			if err != nil {
				t.Fatal(err)
			}
			onDisk, _ := Digest(bytes.NewReader(out))
			ofText, _ := Digest(strings.NewReader(again))
			if v.Digest != onDisk || v.Digest == ofText {
				t.Fatalf("digest %s is not over the bytes on disk (%s; text %s)", v.Digest, onDisk, ofText)
			}
		})
	}
	refused := map[string][]byte{
		"no mark":                   Encode("hi there", UTF16LE)[2:],
		"LE mark, lone low":         {0xFF, 0xFE, 'a', 0, 0x00, 0xDC},
		"BE mark, unpaired high":    {0xFE, 0xFF, 0xD8, 0x00, 0x00, 'a'},
		"LE mark, high at the end":  {0xFF, 0xFE, 0x00, 0xD8},
		"BE mark, odd final length": {0xFE, 0xFF, 0x00, 'a', 'b'},
	}
	for name, b := range refused {
		if IsText(b) {
			t.Errorf("%s: judged text", name)
		}
	}
	for _, b := range [][]byte{refused["LE mark, lone low"], refused["BE mark, unpaired high"]} {
		if _, _, err := Decode(b); !errors.Is(err, ErrBadUTF16) {
			t.Errorf("Decode(% x) = %v, want ErrBadUTF16", b, err)
		}
	}
}

// TestReaderAndEditorVerdict: the reader and the editor read through ReadFile and
// judge with IsText, so a NUL at byte 9,000 and invalid UTF-8 are "not text" on both;
// an edit restores the file's dominant line ending, CRLF or LF.
//
//dsh:test DH-T-FS-2
func TestReaderAndEditorVerdict(t *testing.T) {
	dir := t.TempDir()
	nul := append(bytes.Repeat([]byte("a"), 9000), 0, 'b')
	for name, body := range map[string][]byte{"nul-at-9000": nul, "invalid-utf8": []byte("ok \xff\xfe ok\n")} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
		read, err := ReadFile(path) // the read tool
		if err != nil {
			t.Fatal(err)
		}
		edit, err := os.ReadFile(path) // the edit tool's view is the same bytes
		if err != nil {
			t.Fatal(err)
		}
		if IsText(read) || IsText(edit) {
			t.Errorf("%s: judged text on a path", name)
		}
	}
	for _, c := range []struct{ name, body, eol string }{
		{"crlf", "a\r\nb\r\nc\r\n", "\r\n"},
		{"lf", "a\nb\nc\n", "\n"},
		{"mostly crlf", "a\r\nb\nc\r\n", "\r\n"},
		{"mostly lf", "a\r\nb\nc\n", "\n"},
	} {
		path := filepath.Join(dir, c.name)
		if err := os.WriteFile(path, []byte(c.body), 0o644); err != nil {
			t.Fatal(err)
		}
		b, err := ReadFile(path)
		if err != nil || !IsText(b) {
			t.Fatalf("%s: %v", c.name, err)
		}
		eol := DominantEOL(string(b))
		edited := strings.Replace(NormalizeEOL(string(b)), "b\n", "B\nnew\n", 1)
		if err := WriteFile(path, []byte(RestoreEOL(edited, eol)), Options{}); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(path)
		lines := strings.Count(string(got), "\n")
		if crlf := strings.Count(string(got), "\r\n"); eol != c.eol || (c.eol == "\r\n") != (crlf == lines) {
			t.Errorf("%s: written %q with dominant %q, want every line ending %q", c.name, got, eol, c.eol)
		}
	}
}

// TestContainment is the containment table: "..", a sibling with a common prefix,
// another volume and a link that points outside; host paths round-trip through the
// forward-slash form, drive and network-share paths included, and stay contained.
//
//dsh:test DH-T-NODEFS-4
func TestContainment(t *testing.T) {
	base := t.TempDir()
	ws := filepath.Join(base, "ws")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{ws, outside, ws + "-sibling"} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	root, err := Canonical(ws)
	if err != nil {
		t.Fatal(err)
	}
	check := func(name, p string, want bool) {
		t.Helper()
		c, err := Canonical(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := Inside(root, c); got != want {
			t.Errorf("%s: Inside(%q, %q) = %v, want %v", name, root, c, got, want)
		}
	}
	check("the root", ws, true)
	check("a new file", filepath.Join(ws, "new", "f"), true)
	check("dot-dot", filepath.Join(ws, "..", "outside"), false)
	check("dot-dot inside", filepath.Join(ws, "a", "..", "b"), true)
	check("sibling with a common prefix", ws+"-sibling", false)
	if err := os.Symlink(outside, filepath.Join(ws, "out")); err != nil {
		t.Logf("no symbolic links here (%v); the link row is not run", err)
	} else {
		check("a link that points outside", filepath.Join(ws, "out", "f"), false)
	}

	type row struct {
		host, slash string
	}
	rows := []row{{"/a/b/c", "/a/b/c"}, {"/", "/"}, {"rel/x", "rel/x"}}
	if runtime.GOOS == "windows" {
		rows = []row{
			{`C:\a\b\c`, "C:/a/b/c"},
			{`C:\`, "C:/"},
			{`\\server\share\dir\f`, "//server/share/dir/f"},
			{`rel\x`, "rel/x"},
		}
		if Inside(`C:\a`, `D:\a\b`) {
			t.Error("another volume is inside")
		}
		if !Inside(`\\server\share`, `\\server\share\dir\f`) || Inside(`\\server\share`, `\\server\other\f`) {
			t.Error("network-share containment")
		}
	}
	for _, r := range rows {
		if got := filepath.ToSlash(r.host); got != r.slash {
			t.Errorf("ToSlash(%q) = %q, want %q", r.host, got, r.slash)
		}
		if got := filepath.FromSlash(r.slash); got != r.host {
			t.Errorf("FromSlash(%q) = %q, want %q", r.slash, got, r.host)
		}
		if !Inside(r.host, filepath.FromSlash(r.slash)) {
			t.Errorf("%q is not inside itself after the round trip", r.host)
		}
	}
}
