//go:build scenario

package scenarios

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// util1Run is one headless run of the scenario under an environment.
func util1Run(t *testing.T, bin, root, layer, prompt string, env ...string) dshRun {
	t.Helper()
	return runDsh(t, 5*time.Minute, env, bin, "--home", root, "--layer", layer, "headless", prompt)
}

// TestAllTrafficGoesThroughInterceptingProxy: all traffic goes through a real
// TLS-intercepting proxy; the egress scenario fetches a page and starts an MCP
// server (areas/util-test-support.md DH-E-UTIL-1; DH-D-UTIL-2). Executed in DH-S-39
// (step 39.15) on Linux and Windows; authored in DH-S-6.
//
// Given the built dsh, a real forward proxy that re-signs TLS with a private
// authority and logs each request, HTTPS_PROXY exported, the authority's
// certificate file configured in ExtraCACerts, a real model API key supplied at
// run time and a fresh home with every sharing setting off, when (1) one headless
// prompt calls the model, fetches a public page and starts one MCP server over
// HTTP, (2) the certificate setting is removed and the run repeated and (3) it is
// restored, HTTPS_PROXY set to a pac+http value and the run repeated, then in (1)
// the proxy log shows the model, fetch and MCP requests, each with User-Agent: dsh,
// and the packet capture shows no direct connection and no request to any other
// host; in (2) every request fails with a certificate error and nothing is sent
// unverified; in (3) the launch prints a diagnostic that names the variable and not
// its value, and HTTPS connects directly. Cleanup: the variables are unset (they
// are only ever passed to the child), the home is deleted; on failure the proxy
// log and the capture are kept (their paths are logged).
//
// Inputs: DSH_SCENARIO_UTIL1_PROXY is the proxy's address (http://host:port);
// DSH_SCENARIO_UTIL1_CA the authority's certificate file; DSH_SCENARIO_UTIL1_LOG the
// proxy's request log; DSH_SCENARIO_UTIL1_LAYER the layer text over {ca} that
// configures the model with the key, the fetch tool and one MCP server over HTTP,
// sharing off, and ExtraCACerts = {ca} when {ca} is not empty;
// DSH_SCENARIO_UTIL1_PROMPT the prompt; DSH_SCENARIO_UTIL1_HOSTS the ";"-separated
// hosts of the model, the page and the MCP server; DSH_SCENARIO_UTIL1_CAPTURE a
// shell command line over {proxy} that prints, one per line, the remote addresses
// of the TCP connections the last run made other than to {proxy};
// DSH_SCENARIO_UTIL1_PAC the pac+http value.
//
//dsh:scenario DH-E-UTIL-1
func TestAllTrafficGoesThroughInterceptingProxy(t *testing.T) {
	proxy := need(t, "DSH_SCENARIO_UTIL1_PROXY", "the intercepting proxy's address")
	ca := need(t, "DSH_SCENARIO_UTIL1_CA", "the proxy authority's certificate file")
	logPath := need(t, "DSH_SCENARIO_UTIL1_LOG", "the proxy's request log")
	layerT := need(t, "DSH_SCENARIO_UTIL1_LAYER", "layer text over {ca} with model, fetch, MCP over HTTP, sharing off")
	prompt := need(t, "DSH_SCENARIO_UTIL1_PROMPT", "a prompt that calls the model, fetches a page and uses the MCP server")
	hosts := needList(t, "DSH_SCENARIO_UTIL1_HOSTS", "hosts of the model, the page and the MCP server")
	capture := need(t, "DSH_SCENARIO_UTIL1_CAPTURE", "command printing the last run's direct connections")
	pac := need(t, "DSH_SCENARIO_UTIL1_PAC", "a pac+http HTTPS_PROXY value")

	bin := dshBinary(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "home")
	layer := func(ca string) string {
		path := filepath.Join(dir, "layer-"+filepath.Base(ca)+".yml")
		if err := os.WriteFile(path, []byte(expand(layerT, map[string]string{"ca": ca})), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	logged := func() string {
		data, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("the proxy log: %v", err)
		}
		return string(data)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("kept: the proxy log %s and the capture of %q", logPath, capture)
		}
	})
	proxied := []string{"HTTPS_PROXY=" + proxy, "HTTP_PROXY=" + proxy, "NO_PROXY="}

	t.Run("through the proxy", func(t *testing.T) {
		before := len(logged())
		r := util1Run(t, bin, root, layer(ca), prompt, proxied...)
		if r.code != 0 {
			t.Fatalf("the headless run exited %d:\n%s", r.code, r.stderr)
		}
		got := logged()[before:]
		for _, host := range hosts {
			seen := false
			for _, line := range strings.Split(got, "\n") {
				if strings.Contains(line, host) {
					seen = true
					if !strings.Contains(line, "User-Agent: dsh") {
						t.Errorf("a request to %s lacks User-Agent: dsh: %s", host, line)
					}
				}
			}
			if !seen {
				t.Errorf("the proxy logged no request to %s:\n%s", host, got)
			}
		}
		for _, line := range strings.Split(got, "\n") {
			known := strings.TrimSpace(line) == ""
			for _, host := range hosts {
				known = known || strings.Contains(line, host)
			}
			if !known {
				t.Errorf("a request to another host: %s", line)
			}
		}
		if direct := strings.TrimSpace(mustShell(t, expand(capture, map[string]string{"proxy": proxy}))); direct != "" {
			t.Errorf("direct connections bypassed the proxy:\n%s", direct)
		}
	})

	t.Run("without the authority", func(t *testing.T) {
		r := util1Run(t, bin, root, layer(""), prompt, proxied...)
		if r.code == 0 {
			t.Fatal("the run without ExtraCACerts succeeded")
		}
		if !strings.Contains(strings.ToLower(r.stderr), "certificate") {
			t.Errorf("the failure is not a certificate error:\n%s", r.stderr)
		}
	})

	t.Run("pac+http is refused by name", func(t *testing.T) {
		r := util1Run(t, bin, root, layer(ca), prompt, "HTTPS_PROXY="+pac, "HTTP_PROXY=", "NO_PROXY=")
		out := r.stdout + r.stderr
		if !strings.Contains(out, "HTTPS_PROXY") {
			t.Errorf("no diagnostic names HTTPS_PROXY:\n%s", out)
		}
		if strings.Contains(out, pac) {
			t.Errorf("the diagnostic prints the variable's value:\n%s", out)
		}
		if direct := strings.TrimSpace(mustShell(t, expand(capture, map[string]string{"proxy": proxy}))); direct == "" {
			t.Error("HTTPS did not connect directly with a pac+http value")
		}
	})
}
