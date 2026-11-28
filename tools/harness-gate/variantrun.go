package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
)

// variantBudget bounds every go test run of one s18 variant together: the job's
// 3 h variant limit (timeout-minutes 180, M2-EN-7) less its steps before the
// scenarios.
var variantBudget = 175 * time.Minute

// runVariantRun is "harness-gate variant-run [--root dir] --stage DH-S-n
// --scenarios IDS", the scenario step of an s18 variant job: IDS are the
// variant's required scenarios ("require --variant"), run in the stage's order
// (stageOrder) by Config.runVariant. It returns 2 on a usage or setup error, 1
// when a scenario did not pass and 0 when every one did.
func runVariantRun(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	var stage, ids string
	root, ok := subcommandFlags("variant-run", args, stderr, func(f *flag.FlagSet) {
		f.StringVar(&stage, "stage", "", "the acceptance stage of the variant, e.g. DH-S-18")
		f.StringVar(&ids, "scenarios", "", "the variant's comma-separated scenario IDs (require --variant)")
	})
	if !ok {
		return 2
	}
	stage = strings.TrimSpace(stage)
	if _, ok := acceptance.StageNumber(stage); !ok {
		fmt.Fprintf(stderr, "harness-gate variant-run: --stage %q is not a stage ID (DH-S-n)\n", stage)
		return 2
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate variant-run: %v\n", err)
		return 2
	}
	cfg, err := NewConfig(abs)
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate variant-run: %v\n", err)
		return 2
	}
	x, scenarios, err := cfg.acceptance()
	if err != nil {
		fmt.Fprintf(stderr, "harness-gate variant-run: %v\n", err)
		return 2
	}
	plan, err := acceptance.Preflight(map[string]string{acceptance.RequireEnv: ids}, x, scenarios)
	if err != nil {
		fmt.Fprintf(stdout, "harness-gate variant-run: FAILED, no scenario is started:\n%v\n", err)
		return 1
	}
	if len(plan.Scenarios) == 0 {
		fmt.Fprintln(stderr, "harness-gate variant-run: --scenarios names no scenario")
		return 2
	}
	if err := cfg.runVariant(ctx, stdout, stage, plan); err != nil {
		fmt.Fprintf(stdout, "harness-gate variant-run: FAILED:\n%v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "harness-gate variant-run: passed; %d scenarios\n", len(plan.Scenarios))
	return 0
}

// runVariant runs plan, one variant's scenarios of stage, phase by phase
// (runOrdered): on DH-S-18 a variant holding DH-E-TOOLCHAIN-2 runs it first and
// every later scenario on the binary it verified (impl.md 18.1). Per phase and
// module one go test -tags scenario run, judged by scenarioVerdict: a skip, a
// non-zero exit or a planned test without a PASS line fails it.
func (c *Config) runVariant(ctx context.Context, w io.Writer, stage string, plan acceptance.Plan) error {
	deadline := time.Now().Add(variantBudget)
	return runOrdered(w, stage, plan, func(phase acceptance.Plan, extra ...string) error {
		mods, byMod, err := c.modulePlans(phase)
		if err != nil {
			return err
		}
		var errs []error
		for _, mod := range mods {
			p := byMod[mod]
			out, runErr, err := c.goTestRun(ctx, w, mod, p, deadline, extra...)
			if err == nil {
				err = scenarioVerdict(out, p.Tests(), runErr)
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", c.rel(mod), err))
			}
		}
		return errors.Join(errs...)
	})
}
