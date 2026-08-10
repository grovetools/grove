package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/grovetools/tend/pkg/fs"
	"github.com/grovetools/tend/pkg/harness"
	"github.com/grovetools/tend/pkg/verify"
)

// ConfigCutoverV2V3Scenario runs the checked-in config-lab against production
// grove and groved binaries. It is explicit-only because the grove E2E build
// does not normally build the sibling daemon binary.
func ConfigCutoverV2V3Scenario() *harness.Scenario {
	return harness.NewScenarioWithOptions(
		"config-cutover-v2-v3",
		"Real-binary V2/V3 config migration, degraded CLI, and status-only daemon gate",
		[]string{"config", "migration", "v2", "v3"},
		[]harness.Step{
			harness.NewStep("Run sandboxed real-binary config-lab", func(ctx *harness.Context) error {
				groveBin := filepath.Join(ctx.ProjectRoot, "bin", "grove")
				grovedBin := filepath.Join(filepath.Dir(ctx.ProjectRoot), "daemon", "bin", "groved")
				for _, bin := range []string{groveBin, grovedBin} {
					info, err := os.Stat(bin)
					if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
						return fmt.Errorf("required real binary %s is not built; run make build in grove/ and daemon/", bin)
					}
				}
				artifacts := ctx.NewDir("configlab-evidence")
				sandbox := ctx.NewDir("configlab-sandbox")
				hostileParent := ctx.NewDir("configlab-hostile-parent")
				hostileOverlay := filepath.Join(hostileParent, "operator-overlay.toml")
				// A malformed overlay makes successful completion proof that the lab did
				// not read this inherited path. The byte comparison below separately
				// proves that the path outside the lab sandbox was not modified.
				hostileBytes := []byte("[broken\nexternal = true\n")
				if err := os.WriteFile(hostileOverlay, hostileBytes, 0o600); err != nil {
					return fmt.Errorf("seed hostile parent overlay: %w", err)
				}
				script := filepath.Join(ctx.ProjectRoot, "tests", "configlab", "run.sh")
				cmd := ctx.Command("bash", script,
					"--grove", groveBin,
					"--groved", grovedBin,
					"--artifacts", artifacts,
					"--sandbox", sandbox,
				).Env(
					"GROVE_CONFIG_OVERLAY="+hostileOverlay,
					"GROVE_CONFIGLAB=1",
					"GROVE_CONFIGLAB_FAIL_AFTER=roots.toml",
				).Timeout(3 * time.Minute)
				result := cmd.Run()
				ctx.ShowCommandOutput("config-cutover-v2-v3", result.Stdout, result.Stderr)
				if err := ctx.Check("config-lab exits successfully", result.AssertSuccess()); err != nil {
					return fmt.Errorf("config-lab failed: %w\nstdout:\n%s\nstderr:\n%s", err, result.Stdout, result.Stderr)
				}
				before, beforeErr := os.ReadFile(filepath.Join(artifacts, "before-effective.json"))
				after, afterErr := os.ReadFile(filepath.Join(artifacts, "after-effective.json"))
				hostileAfter, hostileErr := os.ReadFile(hostileOverlay)
				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("summary exists", nil, fs.AssertExists(filepath.Join(artifacts, "configlab-summary.json")))
					v.Equal("hostile parent overlay remains readable", nil, hostileErr)
					v.Equal("hostile inherited malformed overlay was neither read nor modified", string(hostileBytes), string(hostileAfter))
					v.Equal("before effective JSON readable", nil, beforeErr)
					v.Equal("after effective JSON readable", nil, afterErr)
					v.Equal("before/after effective JSON match", string(before), string(after))
					v.Equal("doctor degraded JSON exists", nil, fs.AssertExists(filepath.Join(artifacts, "doctor-degraded.json")))
					v.Equal("daemon degraded status exists", nil, fs.AssertExists(filepath.Join(artifacts, "daemon-degraded-status.json")))
					v.Equal("structured submit 503 exists", nil, fs.AssertExists(filepath.Join(artifacts, "daemon-submit-503.json")))
				})
			}),
		},
		true,
		true,
	)
}
