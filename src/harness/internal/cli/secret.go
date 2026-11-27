package cli

// dsh secret set (DH-D-CLI-9, M2-C-11): a value reaches the managed store from
// standard input only. It is never an argument, never printed and never quoted
// in an error, so a value typed in the wrong place does not reach a stream.

import (
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

// readSecretValue reads the value from in, without one trailing line end.
func readSecretValue(in io.Reader) (secret.Value, error) {
	if in == nil {
		in = strings.NewReader("")
	}
	data, err := io.ReadAll(io.LimitReader(in, maxSecretValue+1))
	if err != nil {
		return secret.Value{}, herr.Wrap(secret.ReasonSource, err, "read the secret value from standard input")
	}
	if len(data) > maxSecretValue {
		return secret.Value{}, herr.Newf(secret.ReasonSource, "the secret value on standard input is longer than %d bytes", maxSecretValue)
	}
	v := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if v == "" {
		return secret.Value{}, herr.New(secret.ReasonEmpty, "no secret value on standard input: pipe it in, e.g. `printf %s \"$KEY\" | dsh secret set DEEPSEEK_API_KEY`")
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
	value, err := readSecretValue(st.env.Stdin)
	if err != nil {
		return err
	}
	t.build("human.secrets")
	store := secret.NewStore(file.New(t.home.Secrets(), file.Options{}))
	rev, err := store.Set(st.ctx, ref, value, secret.AnyRevision)
	if err != nil {
		return err
	}
	if st.res.JSON() {
		return st.res.Value(secretSetDoc{Ref: ref.String(), Source: file.Origin, Revision: rev})
	}
	return st.res.Line("stored " + ref.String() + " (revision " + rev + ")")
}
