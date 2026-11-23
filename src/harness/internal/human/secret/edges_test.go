package secret

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// DH-T-HUMAN-10: the Stringer and GoStringer forms of a Value are redacted when
// called directly, not only through fmt; the masked use error is the same through
// GoString and carries the reason of the error it masks.
//
//dsh:test DH-T-HUMAN-10
func TestDirectPrintedFormsAreRedacted(t *testing.T) {
	v := NewValue(testValue)
	if v.String() != Redacted || v.GoString() != Redacted {
		t.Fatalf("String %q, GoString %q; want %q", v.String(), v.GoString(), Redacted)
	}

	ref := MustParse("secret://deepseek/api-key")
	const reasonRejected herr.Reason = "provider/rejected"
	store := NewStore(&fakeSource{value: testValue})
	err := store.Use(context.Background(), ref, func(v Value) error {
		return herr.Newf(reasonRejected, "key %s refused", v.Reveal())
	})
	var ue *useError
	if !errors.As(err, &ue) {
		t.Fatalf("Use error %T; want the masked use error", err)
	}
	if ue.GoString() != err.Error() || strings.Contains(ue.GoString(), testValue) {
		t.Fatalf("GoString %q; want the masked text %q", ue.GoString(), err.Error())
	}
	if ue.Reason() != string(reasonRejected) {
		t.Fatalf("Reason %q; want the cause's %q", ue.Reason(), reasonRejected)
	}
	// A cause with no reason of its own is reported as a failed use.
	err = store.Use(context.Background(), ref, func(Value) error { return errors.New("plain") })
	if !errors.As(err, &ue) || ue.Reason() != string(ReasonUse) {
		t.Fatalf("Reason of a plain cause = %v; want %q", err, ReasonUse)
	}
}

// The zero Ref names nothing and prints nothing; Resolve and Use refuse it; a
// malformed literal panics in MustParse without quoting it.
//
//dsh:test DH-T-HUMAN-10
func TestZeroRefAndMustParse(t *testing.T) {
	var zero Ref
	if !zero.IsZero() || zero.String() != "" || zero.Segments() != nil {
		t.Fatalf("zero Ref: IsZero %v, String %q, Segments %v", zero.IsZero(), zero.String(), zero.Segments())
	}
	store := NewStore(&fakeSource{value: testValue})
	if _, _, err := store.Resolve(context.Background(), zero); !herr.Has(err, ReasonBadRef) {
		t.Fatalf("Resolve(zero) = %v; want %s", err, ReasonBadRef)
	}
	used := false
	if err := store.Use(context.Background(), zero, func(Value) error { used = true; return nil }); !herr.Has(err, ReasonBadRef) || used {
		t.Fatalf("Use(zero) = %v (fn called %v); want %s and no call", err, used, ReasonBadRef)
	}
	if store.References() != nil {
		t.Fatalf("a refused reference was recorded: %v", store.References())
	}

	defer func() {
		p := recover()
		err, ok := p.(error)
		if !ok || !herr.Has(err, ReasonBadRef) || strings.Contains(err.Error(), testValue) {
			t.Fatalf("MustParse panicked with %v; want a %s error that does not quote the input", p, ReasonBadRef)
		}
	}()
	MustParse(testValue)
}

// References lists the named references once each, in naming order, and the
// zero Ref is never named.
func TestReferencesKeepNamingOrder(t *testing.T) {
	a, b := MustParse("secret://acme/login"), MustParse("secret://deepseek/api-key")
	store := NewStore()
	store.Name(b, Ref{}, a, b)
	got := store.References()
	if len(got) != 2 || got[0] != b || got[1] != a {
		t.Fatalf("References = %v; want [%s %s]", got, b, a)
	}
	got[0] = Ref{}
	if store.References()[0] != b {
		t.Fatal("References returned the store's own slice")
	}
}

// An env source without a lookup function has no value, so the next source
// answers; a missing secrets directory is an absent value, not an error.
//
//dsh:test DH-T-HUMAN-11
func TestSourcesWithNothingToGive(t *testing.T) {
	ref := MustParse("secret://deepseek/api-key")
	store := NewStore(EnvSource(nil), FileSource(filepath.Join(t.TempDir(), "absent")), &fakeSource{value: testValue})
	v, from, err := store.Resolve(context.Background(), ref)
	if err != nil || from != "fake" || v.Reveal() != testValue {
		t.Fatalf("Resolve = %v from %q, %v; want the fake source's value", v, from, err)
	}
	_, _, err = NewStore(EnvSource(nil), FileSource(filepath.Join(t.TempDir(), "absent"))).Resolve(context.Background(), ref)
	if !herr.Has(err, ReasonNotConfigured) {
		t.Fatalf("with nothing configured = %v; want %s", err, ReasonNotConfigured)
	}
}

// The file source refuses a directory where the secret file belongs and a file
// over the size bound, naming the reference but no path or content.
//
//dsh:test DH-T-HUMAN-10
func TestFileSourceRefusesDirectoryAndOversizedFile(t *testing.T) {
	l := home.New(t.TempDir())
	if err := home.Ensure(l); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(l.Secrets(), "deepseek", "api-key"), 0o700); err != nil {
		t.Fatal(err)
	}
	store := NewStore(FileSource(l.Secrets()))
	_, _, err := store.Resolve(context.Background(), MustParse("secret://deepseek/api-key"))
	if !herr.Has(err, ReasonSource) || !strings.Contains(err.Error(), "not a regular file") || strings.Contains(err.Error(), l.Secrets()) {
		t.Fatalf("directory as secret = %v; want %s naming no path", err, ReasonSource)
	}

	dir := filepath.Join(l.Secrets(), "big")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := bytes.Repeat([]byte("k"), maxSecretFile+1)
	if err := os.WriteFile(filepath.Join(dir, "key"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = store.Resolve(context.Background(), MustParse("secret://big/key"))
	if !herr.Has(err, ReasonSource) || !strings.Contains(err.Error(), fmt.Sprint(maxSecretFile)) || strings.Contains(err.Error(), "kkkk") {
		t.Fatalf("oversized secret = %v; want %s naming the bound and no content", err, ReasonSource)
	}
	// One byte under the bound is read whole.
	if err := os.WriteFile(filepath.Join(dir, "key"), content[:maxSecretFile], 0o600); err != nil {
		t.Fatal(err)
	}
	if v, _, err := store.Resolve(context.Background(), MustParse("secret://big/key")); err != nil || len(v.Reveal()) != maxSecretFile {
		t.Fatalf("secret at the bound = %d bytes, %v; want %d", len(v.Reveal()), err, maxSecretFile)
	}
}

// Mask masks a released value in bytes, and also its quoted (escaped) form, so a
// value with a quote or a newline is masked inside %q output too.
//
//dsh:test DH-T-HUMAN-12
func TestMaskCoversBytesAndTheQuotedForm(t *testing.T) {
	const tricky = "sk\"line\nend"
	store := NewStore(&fakeSource{value: tricky})
	if _, _, err := store.Resolve(context.Background(), MustParse("secret://deepseek/api-key")); err != nil {
		t.Fatal(err)
	}
	quoted := fmt.Sprintf("error: %q", "bad key "+tricky)
	got := string(store.Mask([]byte("raw " + tricky + " | " + quoted)))
	if strings.Contains(got, tricky) || strings.Contains(got, `sk\"line\nend`) {
		t.Fatalf("Mask left the value: %s", got)
	}
	if strings.Count(got, Redacted) != 2 {
		t.Fatalf("Mask = %s; want both forms replaced by %s", got, Redacted)
	}
}
