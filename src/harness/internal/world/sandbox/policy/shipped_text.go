package policy

import (
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// loadTexts reads the shipped policy texts from fsys, the contents of
// world/sandbox/policy/text/: every entry must be a regular file <platform>.md
// for a platform of Platforms, and each must render (Render) in every mode, so
// a text that names an unknown field or does not parse never ships. The result
// is keyed by platform; an empty fsys is no text.
//
// The tree on main holds no text (M2-C-6). The branch s17/policy-text adds the
// three texts with text_embed.go, which embeds text/*.md and sets texts from
// this function in init (runbook-s17-wsl.md, "DH-E-SANDBOX-7 on
// s17/policy-text"); it merges only with a passed DH-E-SANDBOX-7 run on their
// exact bytes (harness-gate policy-text).
func loadTexts(fsys fs.FS) (map[string]string, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("policy: read the policy texts: %w", err)
	}
	out := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		platform, ok := strings.CutSuffix(name, ".md")
		if !e.Type().IsRegular() || !ok || !slices.Contains(Platforms, platform) {
			return nil, fmt.Errorf("policy: %q is not a policy text: the text directory holds only <platform>.md for one of %s", name, strings.Join(Platforms, ", "))
		}
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("policy: read the %s policy text: %w", platform, err)
		}
		text := string(b)
		for _, m := range []sandbox.Mode{sandbox.ReadOnly, sandbox.WorkspaceWrite, sandbox.DangerFullAccess} {
			if _, err := Render(platform, Resolved{Mode: m}, text); err != nil {
				return nil, fmt.Errorf("policy: the %s policy text does not render in mode %s: %w", platform, m, err)
			}
		}
		out[platform] = text
	}
	return out, nil
}
