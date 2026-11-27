package cli

// dsh secret set (DH-D-CLI-9, M2-C-11): a value reaches the managed store from
// standard input only. It is never an argument, never printed and never quoted
// in an error, so a value typed in the wrong place does not reach a stream.

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/cmdline"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret/file"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// maxSecretValue bounds the value read from standard input.
const maxSecretValue = 64 << 10

// secretRef reads a secret name: a reference (secret://deepseek/api-key), its
// path (deepseek/api-key) or its environment name (DEEPSEEK_API_KEY), the first
// underscore separating owner from name. The name is never quoted back: it may be
// a value pasted in the wrong place.
func secretRef(name string) (secret.Ref, error) {
	switch {
	case strings.HasPrefix(name, secret.Scheme):
	case strings.Contains(name, "/"):
		name = secret.Scheme + name
	default:
		owner, rest, ok := strings.Cut(strings.ToLower(name), "_")
		if !ok {
			return secret.Ref{}, cmdline.Usagef("dsh secret set", "a secret name has an owner and a name: OWNER_NAME (DEEPSEEK_API_KEY) or secret://owner/name")
		}
		name = secret.Scheme + owner + "/" + strings.ReplaceAll(rest, "_", "-")
	}
	ref, err := secret.Parse(name)
	if err != nil {
		return secret.Ref{}, cmdline.Usagef("dsh secret set", "%s", err.Error())
	}
	return ref, nil
}

// secretPrompt is written to standard error before a value is typed on a terminal.
const secretPrompt = "secret value (input hidden, Enter ends it): "

// fdReader is standard input when it is a file: a terminal, a pipe or a file.
type fdReader interface {
	io.Reader
	Fd() uintptr
}

// readSecretValue reads the value from in, without one trailing line end. On a
// terminal the echo is off while the one line is typed, Enter ends it, and the
// terminal is put back as it was; a pipe or a file is read to its end.
func readSecretValue(ctx context.Context, in io.Reader, prompt io.Writer) (secret.Value, error) {
	if in == nil {
		in = strings.NewReader("")
	}
	if f, ok := in.(fdReader); ok {
		restore, tty, err := hideEcho(f.Fd())
		if tty {
			if err != nil {
				return secret.Value{}, herr.Wrap(secret.ReasonSource, err, "switch the terminal's echo off")
			}
			return readHidden(ctx, f, prompt, restore)
		}
	}
	data, err := io.ReadAll(io.LimitReader(in, maxSecretValue+3))
	if err != nil {
		return secret.Value{}, herr.Wrap(secret.ReasonSource, err, "read the secret value from standard input")
	}
	return secretValueOf(string(data))
}

// readHidden reads one line typed on the terminal in, whose echo is off, then
// restores the terminal: when the line ends, and when ctx ends first.
func readHidden(ctx context.Context, in io.Reader, prompt io.Writer, restore func() error) (secret.Value, error) {
	if prompt == nil {
		prompt = io.Discard
	}
	_, _ = io.WriteString(prompt, secretPrompt)
	type line struct {
		text string
		err  error
	}
	done := make(chan line, 1)
	go func() {
		text, err := bufio.NewReader(io.LimitReader(in, maxSecretValue+2)).ReadString('\n')
		done <- line{text, err}
	}()
	var l line
	select {
	case l = <-done:
	case <-ctx.Done():
		l.err = ctx.Err()
	}
	rerr := restore()
	_, _ = io.WriteString(prompt, "\n")
	if l.err != nil && !(errors.Is(l.err, io.EOF) && l.text != "") {
		if errors.Is(l.err, io.EOF) {
			return secretValueOf("")
		}
		return secret.Value{}, herr.Wrap(secret.ReasonSource, l.err, "read the secret value from the terminal")
	}
	if rerr != nil {
		return secret.Value{}, herr.Wrap(secret.ReasonSource, rerr, "restore the terminal")
	}
	return secretValueOf(l.text)
}

// secretValueOf is the value read, without one trailing line end: refused when
// empty or longer than maxSecretValue.
func secretValueOf(data string) (secret.Value, error) {
	v := strings.TrimSuffix(strings.TrimSuffix(data, "\n"), "\r")
	if len(v) > maxSecretValue {
		return secret.Value{}, herr.Newf(secret.ReasonSource, "the secret value on standard input is longer than %d bytes", maxSecretValue)
	}
	if v == "" {
		return secret.Value{}, herr.New(secret.ReasonEmpty, "no secret value on standard input: type it, or pipe it in, e.g. `printf %s \"$KEY\" | dsh secret set DEEPSEEK_API_KEY`")
	}
	return secret.NewValue(v), nil
}

// secretSetDoc is the JSON form of `dsh secret set --json`.
type secretSetDoc struct {
	Ref      string `json:"ref"`
	Source   string `json:"source"`
	Revision string `json:"revision"`
}

func runSecretSet(st *state, v *cmdline.Values) error {
	ref, err := secretRef(v.Args()[0])
	if err != nil {
		return err
	}
	t, err := st.openAdmin()
	if err != nil {
		return err
	}
	defer t.close(st.env.Stderr)
	value, err := readSecretValue(st.ctx, st.env.Stdin, st.env.Stderr)
	if err != nil {
		return err
	}
	store, err := adminService(t, file.Name, secret.Key)
	if err != nil {
		return err
	}
	rev, err := store.Set(st.ctx, ref, value, secret.AnyRevision)
	if err != nil {
		return err
	}
	if st.res.JSON() {
		return st.res.Value(secretSetDoc{Ref: ref.String(), Source: file.Origin, Revision: rev})
	}
	return st.res.Line("stored " + ref.String() + " (revision " + rev + ")")
}
