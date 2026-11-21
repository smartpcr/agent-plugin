//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/attachments"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/blob"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

const ssaResultBytes = 5 << 20

var (
	ssaBG     = context.Background()
	ssaOuterT *testing.T
)

type ssaState struct {
	cleanups []func()
	home     home.Layout

	// large result
	db        *sqlite.Store
	sp        *local.Store
	sessionID string
	full      []byte
	headBound int
	head      []byte
	ref       *event.Spill
	logged    *event.ToolResultPayload
	rowBytes  int

	// attachment
	att     *attachments.Store
	file    attachments.FileRef
	readErr error

	// privacy
	spillFile string
}

func (s *ssaState) cleanup(f func()) { s.cleanups = append(s.cleanups, f) }

func (s *ssaState) close() {
	for i := len(s.cleanups) - 1; i >= 0; i-- {
		s.cleanups[i]()
	}
	s.cleanups = nil
}

// newHome makes the scenario's home: under t.TempDir() (embedded gate path) or
// a fresh directory under DSH_E2E_STORE_HOME (compose path).
func (s *ssaState) newHome() error {
	var base string
	if v := os.Getenv("DSH_E2E_STORE_HOME"); v != "" {
		dir, err := os.MkdirTemp(v, "e2e-ssa-")
		if err != nil {
			return err
		}
		s.cleanup(func() { _ = os.RemoveAll(dir) })
		base = dir
	} else if ssaOuterT != nil {
		base = ssaOuterT.TempDir()
	} else {
		return errors.New("no outer *testing.T; run through TestE2E_machine_state_views_and_spill_spill_store_and_attachments")
	}
	s.home = home.New(filepath.Join(base, "home"))
	return home.Ensure(s.home)
}

func (s *ssaState) newSpill(opts ...local.Option) error {
	sp, err := local.New(s.home, local.DefaultConfig, opts...)
	if err != nil {
		return fmt.Errorf("open spill store: %w", err)
	}
	s.sp = sp
	return nil
}

// --- Scenario: Large result spilled and recoverable ---

func (s *ssaState) aLargeToolResultInASession() error {
	if err := s.newHome(); err != nil {
		return err
	}
	db, err := sqlite.OpenHome(ssaBG, s.home)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	s.cleanup(func() { _ = db.Close(ssaBG) })
	s.db = db
	if err := s.newSpill(local.WithLineage(local.ForkLineage(db))); err != nil {
		return err
	}
	s.full = make([]byte, ssaResultBytes)
	if _, err := rand.Read(s.full); err != nil {
		return err
	}
	for i := range s.full {
		s.full[i] = 'a' + s.full[i]%26
	}
	return nil
}

func (s *ssaState) itIsStoredThroughTheSpillStore(bound int) error {
	s.headBound = bound
	// A multi-byte rune straddling the bound: the head must not split it.
	copy(s.full[bound-1:], "\u00e9")

	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	s.cleanup(root.Dispose)
	svc := session.NewService(root, s.db, session.Config{})
	h, err := svc.Create(ssaBG, nil, session.CreateOptions{Scope: scope.Key{}})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	s.sessionID = string(h.ID())

	head, ref, err := spill.Save(ssaBG, s.sp, s.sessionID, "out.log", s.full, bound)
	if err != nil {
		return fmt.Errorf("spill.Save: %w", err)
	}
	s.head, s.ref = head, ref
	if ref == nil {
		return errors.New("a 5 MB result was not spilled")
	}

	msg := func(id string, role vocab.Role, b vocab.Block) (vocab.Message, error) {
		return vocab.RestoreMessage(id, role, nil, b)
	}
	user, err := msg("u1", vocab.RoleUser, vocab.TextBlock("run it"))
	if err != nil {
		return err
	}
	call, err := msg("a1", vocab.RoleAssistant, vocab.ToolUseBlock("c1", "shell", json.RawMessage(`{}`)))
	if err != nil {
		return err
	}
	result, err := msg("r1", vocab.RoleTool, vocab.ToolResultBlock("c1", false, vocab.TextPart(string(head))))
	if err != nil {
		return err
	}
	drafts := []func() (event.Draft, error){
		func() (event.Draft, error) {
			return event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: user})
		},
		func() (event.Draft, error) {
			return event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{Message: call})
		},
		func() (event.Draft, error) {
			return event.ToolResult.Draft(event.Append(), event.ToolResultPayload{CallID: "c1", Message: result, Spill: ref})
		},
	}
	for _, mk := range drafts {
		d, err := mk()
		if err != nil {
			return err
		}
		if _, err := h.Append(ssaBG, d); err != nil {
			return fmt.Errorf("append: %w", err)
		}
	}
	return h.Close(ssaBG)
}

func (s *ssaState) theLoggedToolResultHoldsABoundedHeadAndTheReference(bound int) error {
	if len(s.head) > bound || !bytes.HasPrefix(s.full, s.head) {
		return fmt.Errorf("head of %d bytes is not a prefix within %d", len(s.head), bound)
	}
	if s.ref.Bytes != int64(len(s.full)) {
		return fmt.Errorf("reference records %d bytes, want %d", s.ref.Bytes, len(s.full))
	}
	r, err := s.db.OpenRead(ssaBG, s.sessionID)
	if err != nil {
		return err
	}
	defer r.Close(ssaBG)
	recs, err := r.Read(ssaBG, 0, 0, store.ReadOptions{})
	if err != nil {
		return err
	}
	for _, rec := range recs {
		if rec.Type != "tool/result" {
			continue
		}
		s.logged = new(event.ToolResultPayload)
		if err := json.Unmarshal([]byte(rec.Payload), s.logged); err != nil {
			return err
		}
		s.rowBytes = len(rec.Payload)
	}
	if s.logged == nil {
		return errors.New("no tool/result row in the log")
	}
	// The row is the head plus a small envelope; it must never carry the full text.
	if s.rowBytes > bound+4096 {
		return fmt.Errorf("the logged row is %d bytes, the head bound is %d", s.rowBytes, bound)
	}
	if s.logged.Spill == nil || *s.logged.Spill != *s.ref {
		return fmt.Errorf("logged spill %+v, want %+v", s.logged.Spill, s.ref)
	}
	blocks := s.logged.Message.Blocks()
	if len(blocks) != 1 || blocks[0].ToolResult == nil {
		return fmt.Errorf("logged result has %d blocks, want one tool_result", len(blocks))
	}
	var persisted strings.Builder
	for _, p := range blocks[0].ToolResult.Parts {
		if p.Text == nil {
			return errors.New("logged tool result has a non-text part")
		}
		persisted.WriteString(p.Text.Text)
	}
	// The persisted head itself: within the bound, the exact prefix Save
	// returned, and not the full text.
	got := persisted.String()
	if len(got) > bound {
		return fmt.Errorf("persisted head is %d bytes, bound %d", len(got), bound)
	}
	if got != string(s.head) || !bytes.HasPrefix(s.full, []byte(got)) {
		return fmt.Errorf("persisted head (%d bytes) is not the %d-byte prefix returned by Save", len(got), len(s.head))
	}
	// The 2-byte rune planted at bound-1..bound is the only multi-byte rune,
	// so the longest rune-aligned head is exactly bound-1 bytes and ends
	// just before it.
	if !utf8.ValidString(got) || len(got) != bound-1 || !bytes.HasPrefix(s.full[len(got):], []byte("\u00e9")) {
		return fmt.Errorf("persisted head of %d bytes is not the %d-byte rune-aligned prefix ending before the planted rune", len(got), bound-1)
	}
	if dir, _, err := spill.ParseID(s.ref.Path); err != nil || dir != sqlite.SpillName(s.sessionID) {
		return fmt.Errorf("owner dir %q (%v), the session store removes %q", dir, err, sqlite.SpillName(s.sessionID))
	}
	return nil
}

func (s *ssaState) readingTheReferenceReturnsAllByteIdentical() error {
	r, err := s.sp.Open(ssaBG, s.sessionID, s.logged.Spill.Path)
	if err != nil {
		return fmt.Errorf("open spill %s: %w", s.logged.Spill.Path, err)
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if len(got) != ssaResultBytes || !bytes.Equal(got, s.full) {
		return fmt.Errorf("read back %d bytes, not byte-identical to the %d saved", len(got), len(s.full))
	}
	return nil
}

// --- Scenario: Corrupted attachment detected ---

func (s *ssaState) anAttachmentStoredInBlobs() error {
	if err := s.newHome(); err != nil {
		return err
	}
	b, err := blob.New(s.home)
	if err != nil {
		return fmt.Errorf("open blob store: %w", err)
	}
	att, err := attachments.New(b, attachments.Config{})
	if err != nil {
		return fmt.Errorf("open attachment store: %w", err)
	}
	s.att = att
	fr, err := att.SaveFile(ssaBG, "notes.txt", strings.NewReader("the original attachment bytes"))
	if err != nil {
		return fmt.Errorf("SaveFile: %w", err)
	}
	s.file = fr
	path, ok := att.HostPath(fr)
	if !ok {
		return errors.New("stored attachment has no host path")
	}
	rel, err := filepath.Rel(s.home.Blobs(), path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("attachment %s is not under %s", path, s.home.Blobs())
	}
	// A clean read verifies before the corruption.
	r, err := att.Open(ssaBG, fr.Ref)
	if err != nil {
		return err
	}
	got, err := io.ReadAll(r)
	r.Close()
	if err != nil || string(got) != "the original attachment bytes" {
		return fmt.Errorf("clean read = %q, %v", got, err)
	}
	return nil
}

func (s *ssaState) oneByteIsFlippedAndItIsReadBack() error {
	path, _ := s.att.HostPath(s.file)
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	b[0] ^= 0x20
	// Objects are read-only; make it writable to simulate on-disk corruption.
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return err
	}
	r, err := s.att.Open(ssaBG, s.file.Ref)
	if err == nil {
		_, err = io.ReadAll(r)
		r.Close()
	}
	s.readErr = err
	return nil
}

func (s *ssaState) theReadFailsWithADigestMismatchError() error {
	if s.readErr == nil {
		return errors.New("a flipped byte was served without error")
	}
	if !herr.Has(s.readErr, blob.ReasonCorrupt) || !strings.Contains(s.readErr.Error(), "digest mismatch") {
		return fmt.Errorf("read error = %v; want %s digest mismatch", s.readErr, blob.ReasonCorrupt)
	}
	return nil
}

// --- Scenario: Spill files are private ---

func (s *ssaState) aSpilledResultInATempHome() error {
	if err := s.newHome(); err != nil {
		return err
	}
	if err := s.newSpill(); err != nil {
		return err
	}
	_, ref, err := spill.Save(ssaBG, s.sp, "s1", "out.txt", []byte("0123456789"), 2)
	if err != nil {
		return err
	}
	if ref == nil {
		return errors.New("result was not spilled")
	}
	file, ok := s.sp.HostPath(ref.Path)
	if !ok {
		return errors.New("spilled result has no host path")
	}
	s.spillFile = file
	return nil
}

func (s *ssaState) theSpillFilesPermissionsAreInspected() error {
	if _, err := os.Stat(s.spillFile); err != nil {
		return err
	}
	return nil
}

func (s *ssaState) theFileAndItsSpillDirectoriesArePrivate() error {
	dirs := []string{filepath.Dir(s.spillFile), s.home.Spill()}
	if runtime.GOOS == "windows" {
		// Protected DACL whose only ACEs are the owner SID, SYSTEM and
		// Administrators (no other SID, not even the process user when it is
		// not the owner), read back with GetNamedSecurityInfo.
		for _, p := range append([]string{s.spillFile}, dirs...) {
			if err := ssaAssertOwnerOnly(p); err != nil {
				return err
			}
		}
		return nil
	}
	info, err := os.Lstat(s.spillFile)
	if err != nil {
		return err
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		return fmt.Errorf("%s: mode %o, want 0600", s.spillFile, perm)
	}
	for _, d := range dirs {
		if err := lfhsAssertPrivate(d); err != nil {
			return err
		}
	}
	return nil
}

func InitializeScenario_machine_state_views_and_spill_spill_store_and_attachments(ctx *godog.ScenarioContext) {
	s := &ssaState{}
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.close()
		*s = ssaState{}
		return c, err
	})
	ctx.Step(`^a 5 MB tool result in a session of a temp home$`, s.aLargeToolResultInASession)
	ctx.Step(`^it is stored through the spill store with a head bound of (\d+) bytes$`, s.itIsStoredThroughTheSpillStore)
	ctx.Step(`^the logged tool result holds a head of at most (\d+) bytes and the spill reference$`, s.theLoggedToolResultHoldsABoundedHeadAndTheReference)
	ctx.Step(`^reading the reference returns all 5 MB byte-identical$`, s.readingTheReferenceReturnsAllByteIdentical)
	ctx.Step(`^an attachment stored in blobs$`, s.anAttachmentStoredInBlobs)
	ctx.Step(`^one byte of the stored file is flipped on disk and it is read back$`, s.oneByteIsFlippedAndItIsReadBack)
	ctx.Step(`^the read fails with a digest-mismatch error$`, s.theReadFailsWithADigestMismatchError)
	ctx.Step(`^a spilled result in a temp home$`, s.aSpilledResultInATempHome)
	ctx.Step(`^the spill file's permissions are inspected$`, s.theSpillFilesPermissionsAreInspected)
	ctx.Step(`^the file and its spill directories are private to their owner$`, s.theFileAndItsSpillDirectoriesArePrivate)
}

func TestE2E_machine_state_views_and_spill_spill_store_and_attachments(t *testing.T) {
	ssaOuterT = t
	defer func() { ssaOuterT = nil }()
	suite := godog.TestSuite{
		Name:                "machine_state_views_and_spill_spill_store_and_attachments",
		ScenarioInitializer: InitializeScenario_machine_state_views_and_spill_spill_store_and_attachments,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"machine_state_views_and_spill_spill_store_and_attachments.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog scenarios failed")
	}
}
