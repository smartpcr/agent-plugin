//go:build scenario

package scenarios

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/scenario"
	"github.com/smartpcr/agent-plugin/src/sdk/sdk"
)

// TestProfilesSendOnlyModelRequests is the DH-E-BUNDLE-3 scenario
// (docs/harness/areas/bundle-preset.md; DH-D-BUNDLE-2, DH-D-BUNDLE-3; tech-spec
// sec. 4.5: authored in DH-S-16, run in DH-S-22): the web, headless and sdk
// profiles send nothing but model requests.
//
// Given the built dsh behind a recording proxy that is the only route out
// (DSH_SCENARIO_BUNDLE3_PROXY starts it over {log}), an empty home, a real model
// API, the Go SDK client for sdk and a real browser for web,
// When each of the three profiles starts, runs one prompt that uses a shell tool
// and stops,
// Then the proxy log holds requests to the model provider only.
// Cleanup: the home is removed by scenario.Run; the proxy log is kept in the
// test log on failure.
//
// Inputs: DSH_SCENARIO_BUNDLE3_PROXY, a command over {log} running the recording
// proxy in the foreground, one requested host per log line;
// DSH_SCENARIO_BUNDLE3_PROXY_URL, its address; DSH_SCENARIO_BUNDLE3_MODEL_HOST, the
// provider's host; DSH_SCENARIO_BUNDLE3_LAYER, a layer template file
// ({workspace}) on the signed-in Claude Code route (no API key: tech-spec sec.
// 4.3); DSH_SCENARIO_BUNDLE3_WEB_PROMPT, a command over
// {origin} and {prompt} sending one prompt through the browser and waiting for
// its answer.
//
//dsh:scenario DH-E-BUNDLE-3
func TestProfilesSendOnlyModelRequests(t *testing.T) {
	scenario.Run(t, "DH-E-BUNDLE-3", scenario.Needs{
		Bin: true, Deadline: 30 * time.Minute,
		Env: []string{"DSH_SCENARIO_BUNDLE3_PROXY", "DSH_SCENARIO_BUNDLE3_PROXY_URL", "DSH_SCENARIO_BUNDLE3_MODEL_HOST", "DSH_SCENARIO_BUNDLE3_LAYER", "DSH_SCENARIO_BUNDLE3_WEB_PROMPT"},
	}, func(s *scenario.S) {
		log := s.TempDir() + string(os.PathSeparator) + "proxy.log"
		startShellTree(t, expand(need(t, "DSH_SCENARIO_BUNDLE3_PROXY", "the recording proxy"), map[string]string{"log": log}))
		proxy := need(t, "DSH_SCENARIO_BUNDLE3_PROXY_URL", "the proxy's address")
		model := need(t, "DSH_SCENARIO_BUNDLE3_MODEL_HOST", "the model provider's host")
		layer := layerInput(t, "DSH_SCENARIO_BUNDLE3_LAYER", "a layer on the signed-in Claude Code route", map[string]string{"workspace": s.Workspace})
		for _, p := range []string{"web", "headless", "sdk"} {
			profileLayer(t, s.Home, p, layer)
		}
		env := []string{"DSH_HOME=" + s.Home, "HTTPS_PROXY=" + proxy, "HTTP_PROXY=" + proxy, "ALL_PROXY=" + proxy, "NO_PROXY="}
		prompt := "Run the shell command echo bundle3 and tell me what it printed."

		h := startDshOnTerminal(t, env, s.Bin, "--home", s.Home, "web")
		link := h.waitFor(t, 2*time.Minute, "a launch link", func(out string) string { return launchLink.FindString(plainText(out)) })
		operatorStep(t, "DSH_SCENARIO_BUNDLE3_WEB_PROMPT", "sends a prompt through the browser", map[string]string{"origin": originOf(link), "prompt": prompt})
		if err := terminate(t, h); err != nil || !h.await(time.Minute) {
			t.Errorf("web: the host did not stop: %v", err)
		}

		if r := dshIn(t, s, 10*time.Minute, env, "headless", prompt); r.code != 0 {
			t.Errorf("headless: exit %d:\n%s", r.code, r.stderr)
		}

		ctx := s.Context()
		c, err := sdk.Start(ctx, sdk.Options{Bin: s.Bin, Home: s.Home, Dir: s.Workspace, Env: env[1:]})
		if err != nil {
			t.Fatalf("sdk: %v", err)
		}
		sess, err := c.Create(ctx, sdk.CreateOptions{Cwd: s.Workspace})
		if err == nil {
			_, err = sess.Run(ctx, sdk.Input{Text: prompt})
		}
		if err != nil {
			t.Errorf("sdk: the prompt failed: %v", err)
		}
		if err := c.Close(ctx); err != nil {
			t.Errorf("sdk: close: %v", err)
		}

		data, err := os.ReadFile(log)
		if err != nil || len(strings.TrimSpace(string(data))) == 0 {
			t.Fatalf("the proxy logged nothing (%v): the profiles did not route through it", err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if !strings.Contains(line, model) {
				t.Errorf("the proxy log holds a request to something other than %s: %s", model, line)
			}
		}
		if t.Failed() {
			t.Logf("proxy log:\n%s", data)
		}
	})
}
