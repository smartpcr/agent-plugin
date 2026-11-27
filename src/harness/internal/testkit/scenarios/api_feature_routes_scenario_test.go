//go:build scenario

package scenarios

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/scenario"
)

// TestDisablingFeatureRemovesRoutes is the DH-E-API-5 scenario
// (docs/harness/areas/api.md; DH-D-API-1; tech-spec sec. 4.5: authored in
// DH-S-16, run in DH-S-48): disabling a feature removes its routes from a running
// product.
//
// Given the built dsh with the jobs routes mounted and a real browser showing a
// session with a running job in the roster (DSH_SCENARIO_API5_JOB starts it),
// When the writable configuration layer is edited to disable the jobs route
// component, then edited to enable it again,
// Then without a restart the roster region is replaced by "unavailable", a direct
// request to a jobs URL answers 404 and the conversation keeps streaming; after
// re-enabling, the region is live again.
// Cleanup: the layer is restored and the job stopped (DSH_SCENARIO_API5_STOP), the
// host stopped; the home is removed by scenario.Run.
//
// Inputs: DSH_SCENARIO_API5_LAYER and DSH_SCENARIO_API5_DISABLED, layer template
// files ({workspace}) with the jobs routes enabled and disabled;
// DSH_SCENARIO_API5_JOB, a command over {origin} and {out} that opens a session
// in the browser, starts a long job and a streaming turn, and writes
// {"session":"","jobsURL":""} to {out}; DSH_SCENARIO_API5_UI, a command over
// {origin} and {session} and {out} writing {"roster":"","streaming":false} as the
// browser shows them; DSH_SCENARIO_API5_STOP, a command over {origin} and
// {session} stopping the job; DSH_SCENARIO_API5_COOKIE, a command over {origin}
// printing the signed-in Cookie header value.
//
//dsh:scenario DH-E-API-5
func TestDisablingFeatureRemovesRoutes(t *testing.T) {
	scenario.Run(t, "DH-E-API-5", scenario.Needs{
		Bin: true, Deadline: 30 * time.Minute,
		Env: []string{"DSH_SCENARIO_API5_LAYER", "DSH_SCENARIO_API5_DISABLED", "DSH_SCENARIO_API5_JOB", "DSH_SCENARIO_API5_UI", "DSH_SCENARIO_API5_STOP", "DSH_SCENARIO_API5_COOKIE"},
	}, func(s *scenario.S) {
		vars := map[string]string{"workspace": s.Workspace}
		layerFile := s.Home + string(os.PathSeparator) + "config" + string(os.PathSeparator) + "layer.yml"
		write := func(input string) {
			if err := os.MkdirAll(s.Home+string(os.PathSeparator)+"config", 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(layerFile, []byte(layerInput(t, input, "a writable layer", vars)), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		write("DSH_SCENARIO_API5_LAYER")
		h := startDshOnTerminal(t, []string{"DSH_HOME=" + s.Home}, s.Bin, "--home", s.Home, "web")
		origin := originOf(h.waitFor(t, 2*time.Minute, "a launch link", func(out string) string { return launchLink.FindString(plainText(out)) }))
		var job struct {
			Session string `json:"session"`
			JobsURL string `json:"jobsURL"`
		}
		operatorJSON(t, "DSH_SCENARIO_API5_JOB", "starts a job in the browser", map[string]string{"origin": origin}, &job)
		s.Cleanup(func() {
			write("DSH_SCENARIO_API5_LAYER")
			operatorStep(t, "DSH_SCENARIO_API5_STOP", "stops the job", map[string]string{"origin": origin, "session": job.Session})
			_ = terminate(t, h)
			h.await(time.Minute)
		})
		cookie := lastLine(operatorStep(t, "DSH_SCENARIO_API5_COOKIE", "reads the signed-in cookie", map[string]string{"origin": origin}))
		type view struct {
			Roster    string `json:"roster"`
			Streaming bool   `json:"streaming"`
		}
		look := func() view {
			var v view
			operatorJSON(t, "DSH_SCENARIO_API5_UI", "reads the browser", map[string]string{"origin": origin, "session": job.Session}, &v)
			return v
		}
		status := func() int {
			req, err := http.NewRequestWithContext(s.Context(), http.MethodGet, job.JobsURL, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Cookie", cookie)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("GET %s: %v", job.JobsURL, err)
			}
			resp.Body.Close()
			return resp.StatusCode
		}
		settle := func(want int) {
			for deadline := time.Now().Add(time.Minute); status() != want && time.Now().Before(deadline); {
				time.Sleep(time.Second)
			}
		}
		if got := status(); got != http.StatusOK {
			t.Fatalf("the jobs URL answers %d before the edit, want 200", got)
		}

		write("DSH_SCENARIO_API5_DISABLED")
		settle(http.StatusNotFound)
		if got := status(); got != http.StatusNotFound {
			t.Errorf("the jobs URL answers %d with the component disabled, want 404", got)
		}
		if v := look(); v.Roster != "unavailable" || !v.Streaming {
			t.Errorf("with the component disabled the roster shows %q and streaming is %t, want unavailable and true", v.Roster, v.Streaming)
		}
		if h.await(0) {
			t.Fatal("the host restarted or stopped on the edit")
		}

		write("DSH_SCENARIO_API5_LAYER")
		settle(http.StatusOK)
		if got := status(); got != http.StatusOK {
			t.Errorf("the jobs URL answers %d after re-enabling, want 200", got)
		}
		if v := look(); v.Roster == "unavailable" || v.Roster == "" {
			t.Errorf("after re-enabling the roster shows %q, want it live", v.Roster)
		}
	})
}
