package policy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"text/template"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// Platforms are the platforms a policy text is written for.
var Platforms = []string{"linux", "darwin", "windows"}

// ErrNoText is Text's error for a platform no sandbox policy text ships for. The
// prompt builder then leaves the sandbox policy section out.
var ErrNoText = errors.New("policy: no sandbox policy text ships for this platform")

// ErrUnknownPlatform is the error for a platform not in Platforms.
var ErrUnknownPlatform = errors.New("policy: unknown platform")

// texts are the shipped per-platform policy texts, keyed by platform. None ships
// on main: under M2-C-6 a text merges only with a passed DH-E-SANDBOX-7 run on it
// (Stage 7.6), and harness-gate policy-text refuses any tree with a text file
// that has none. The branch s17/policy-text moves testdata/draft_text/*.md to
// text/ and adds text_embed.go, which sets texts from loadTexts in init.
var texts = map[string]string{}

// Text is the shipped policy text template of platform: ErrNoText while none
// ships, ErrUnknownPlatform for a platform not in Platforms.
func Text(platform string) (string, error) {
	if !slices.Contains(Platforms, platform) {
		return "", fmt.Errorf("%w: %q", ErrUnknownPlatform, platform)
	}
	t, ok := texts[platform]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrNoText, platform)
	}
	return t, nil
}

// TextVersion is the version a policy records for text: "sha256:" and the hex
// SHA-256 of it.
func TextVersion(text string) string {
	sum := sha256.Sum256([]byte(text))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// FullAccessLabel is how the policy section names danger-full-access. It is the
// unconfirmed default of impl.md sec. 9.1 (I2 Q3): in full-access mode a command
// may take over the harness, and the label says so. An owner change is this one
// line: the render goldens hold the token <<policy.FullAccessLabel>> in its place
// (render_test.go), so they do not change with it.
const FullAccessLabel = "full access: commands run unconfined and can take over the harness, including its stored keys and sessions"

// EscalationHint is the policy section's instruction for asking one wider call.
const EscalationHint = "If the sandbox denies an action you need, retry that exact call once with sandbox_permissions set to a wider mode and a justification; a person decides, and only that one call is widened."

// ModeLabel is the label the policy section gives mode m.
func ModeLabel(m sandbox.Mode) string {
	switch m {
	case sandbox.ReadOnly:
		return "read-only: commands and file tools may read but not write"
	case sandbox.WorkspaceWrite:
		return "workspace-write: commands and file tools may write only below the write roots"
	case sandbox.DangerFullAccess:
		return FullAccessLabel
	}
	return string(m)
}

// Resolved is what the policy section states of a resolved policy. It is plain
// data, so a golden can render another platform's paths on any host.
type Resolved struct {
	Mode       sandbox.Mode
	WriteRoots []sandbox.WriteRoot
	Network    bool
}

// Describe is the Resolved of p.
func Describe(p sandbox.Policy) Resolved {
	return Resolved{Mode: p.Mode(), WriteRoots: p.WriteRoots(), Network: p.Network()}
}

// renderData is the template's data.
type renderData struct {
	Platform       string
	Mode           sandbox.Mode
	ModeLabel      string
	WriteRoots     []sandbox.WriteRoot
	Network        string
	EscalationHint string
	FullAccess     bool
}

// Render fills the policy text template tmpl for platform with res: the mode and
// its label, the write roots with their sources, the network setting and the
// escalation hint (absent under full access, which has nothing wider).
func Render(platform string, res Resolved, tmpl string) (string, error) {
	if !slices.Contains(Platforms, platform) {
		return "", fmt.Errorf("%w: %q", ErrUnknownPlatform, platform)
	}
	if !res.Mode.Valid() {
		return "", fmt.Errorf("policy: render: %q is not a sandbox mode", res.Mode)
	}
	t, err := template.New("policy-" + platform).Option("missingkey=error").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("policy: parse the %s policy text: %w", platform, err)
	}
	d := renderData{Platform: platform, Mode: res.Mode, ModeLabel: ModeLabel(res.Mode),
		WriteRoots: slices.Clone(res.WriteRoots), Network: "denied", FullAccess: res.Mode == sandbox.DangerFullAccess}
	if res.Network {
		d.Network = "allowed"
	}
	if !d.FullAccess {
		d.EscalationHint = EscalationHint
	}
	var b bytes.Buffer
	if err := t.Execute(&b, d); err != nil {
		return "", fmt.Errorf("policy: render the %s policy text: %w", platform, err)
	}
	return b.String(), nil
}

// PromptSection is the system-prompt section that states the step's sandbox
// policy to the model, rendered from the platform's shipped text. While no text
// ships (ErrNoText) it is empty, so the prompt builder leaves it out.
func (r *Resolver) PromptSection() prompt.Section {
	return prompt.Section{Name: prompt.SectionSandboxPolicy, Order: prompt.OrderSandbox, Literal: true,
		Func: func(in prompt.Input) (string, error) {
			platform := r.cfg.platform()
			tmpl, err := Text(platform)
			if errors.Is(err, ErrNoText) {
				return "", nil
			}
			if err != nil {
				return "", err
			}
			p, err := r.Resolve(context.Background(), sandbox.Request{Session: in.Target().Agent.Session})
			if err != nil {
				return "", err
			}
			return Render(platform, Describe(p), tmpl)
		}}
}

// RegisterPromptSection registers PromptSection in ps, owned by owner.
func (r *Resolver) RegisterPromptSection(owner *cordis.Context, ps *prompt.Service) (*cordis.EffectHandle, error) {
	return ps.Section(owner, r.PromptSection())
}
