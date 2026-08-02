package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/config"
	"github.com/grovetools/grove/cmd/satelliteassets"
)

func TestSatelliteSeedDeclaresIntentAsMachineConfig(t *testing.T) {
	files, err := buildSatelliteConfigSeed("vm1", []string{"cloud", "grovetools"}).Files()
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	byName := map[string]string{}
	for _, f := range files {
		byName[f.Name] = f.Content
	}

	machine := byName[config.SeedFileMachineTOML]
	if !strings.Contains(machine, `name = "vm1"`) {
		t.Errorf("the VM does not adopt its registry name as its machine name:\n%s", machine)
	}
	if !strings.Contains(machine, "[machine.ecosystems.grovetools]") ||
		!strings.Contains(machine, `path = "`+bootstrapRemoteCodeDir+`"`) {
		t.Errorf("machine.toml does not declare the ecosystem the bootstrap clones:\n%s", machine)
	}
}

// The VM's own entries pull, so they CANNOT be satellite-role — that role is
// push-only by hard invariant. This asserts the deviation from the contract's
// §1 Q5 wording is the one that is actually in the code.
func TestSatelliteSeedSyncEntriesArePeerRoleAndPull(t *testing.T) {
	seed := buildSatelliteConfigSeed("vm1", []string{"cloud"})
	if seed.Sync == nil {
		t.Fatal("no sync seed rendered")
	}
	for _, ws := range seed.Sync.Workspaces {
		if ws.Role != config.SyncRolePeer {
			t.Errorf("workspace %q has role %q, want peer", ws.Name, ws.Role)
		}
		if !ws.Pull {
			t.Errorf("workspace %q does not pull; the VM materializes its own notebook", ws.Name)
		}
	}
	if seed.Sync.Server != "http://"+defaultSyncRemoteAddr {
		t.Errorf("sync server = %q, want the VM's own syncd", seed.Sync.Server)
	}
	// Never a token in the seed — the bootstrap mints it ON the VM.
	bundle, err := seed.Bundle()
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	if strings.Contains(bundle, "token = ") {
		t.Errorf("the seed carries a literal token:\n%s", bundle)
	}
}

func TestSatelliteSeedWithoutWorkspacesRendersNoSyncConfig(t *testing.T) {
	seed := buildSatelliteConfigSeed("vm1", nil)
	if seed.Sync != nil {
		t.Fatalf("a satellite with no sync workspaces still got a sync seed: %+v", seed.Sync)
	}
	if len(seed.Dirs) != 0 {
		t.Fatalf("no workspaces should mean no notebook skeletons: %v", seed.Dirs)
	}
	files, err := seed.Files()
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	for _, f := range files {
		if f.Name == config.SeedFileSyncTOML {
			t.Fatalf("rendered a sync.toml with no workspaces:\n%s", f.Content)
		}
	}
}

// The bootstrap script must not author config any more. This is the acceptance
// criterion "bootstrap heredocs gone", asserted against the embedded script so
// it cannot regress silently.
func TestBootstrapScriptNoLongerAuthorsConfig(t *testing.T) {
	script := readEmbeddedBootstrap(t)
	for _, forbidden := range []string{
		// The two step-5 heredocs, by the shapes that actually author config.
		`cat > "$HOME/.config/grove/`,
		`> "$HOME/.config/grove/sync.toml"`,
		// The bash-side TOML assembly they contained.
		"\n[[workspaces]]",
		"\n[groves.",
		"printf 'server =",
		// The flag whose only job was feeding that assembly.
		"    --workspaces)",
	} {
		if strings.Contains(script, forbidden) {
			t.Errorf("satellite-bootstrap.sh still contains %q — step 5 must only unpack the CLI's config seed", forbidden)
		}
	}
	if !strings.Contains(script, "--config-seed") {
		t.Error("satellite-bootstrap.sh does not accept --config-seed")
	}
}

// End to end across the language boundary: the Go renderer's bundle, unpacked
// by the REAL block from the embedded script, must produce config the real
// loaders accept. Neither half is stubbed.
func TestSatelliteConfigSeedUnpacksThroughTheRealBootstrapBlock(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	unpacker := extractUnpacker(t, readEmbeddedBootstrap(t))

	bundle, err := buildSatelliteConfigSeed("vm1", []string{"cloud", "grovetools"}).Bundle()
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}

	home := t.TempDir()
	seedPath := filepath.Join(t.TempDir(), "seed.bundle")
	if err := os.WriteFile(seedPath, []byte(bundle), 0o600); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(t.TempDir(), "unpack.sh")
	if err := os.WriteFile(scriptPath, []byte(unpacker), 0o700); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", scriptPath)
	cmd.Env = append(os.Environ(), "HOME="+home, "GROVE_CONFIG_SEED="+seedPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("unpacker failed: %v\n%s", err, out)
	}

	configDir := filepath.Join(home, ".config", "grove")
	mc, err := config.LoadMachineConfigFrom(filepath.Join(configDir, config.SeedFileMachineTOML))
	if err != nil {
		t.Fatalf("unpacked machine.toml does not load: %v", err)
	}
	if mc == nil || mc.Machine.Name != "vm1" {
		t.Fatalf("unpacked machine.toml lost its content: %+v", mc)
	}
	sc, err := config.LoadSyncConfigFrom(filepath.Join(configDir, config.SeedFileSyncTOML))
	if err != nil {
		t.Fatalf("unpacked sync.toml does not load: %v", err)
	}
	if len(sc.Workspaces) != 2 {
		t.Fatalf("unpacked sync.toml has %d workspaces, want 2", len(sc.Workspaces))
	}
	for _, ws := range sc.Workspaces {
		if !ws.Pull || ws.Role != config.SyncRolePeer {
			t.Fatalf("workspace %q lost role/pull across the wire format: %+v", ws.Name, ws)
		}
	}
	if _, err := config.LoadFromTOMLBytes(mustRead(t, filepath.Join(configDir, config.SeedFileGroveTOML))); err != nil {
		t.Fatalf("unpacked grove.toml does not load: %v", err)
	}

	// Notebook skeletons, and the sync file's restrictive mode, survived.
	if _, err := os.Stat(filepath.Join(home, "notebooks/grovetools/workspaces/cloud/inbox")); err != nil {
		t.Fatalf("notebook skeleton missing: %v", err)
	}
	info, err := os.Stat(filepath.Join(configDir, config.SeedFileSyncTOML))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("sync.toml landed with mode %o, want 600", info.Mode().Perm())
	}
}

// The unpacker's refusals are the only defense on the far side of the wire.
func TestBootstrapUnpackerRefusesHostileSeeds(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	unpacker := extractUnpacker(t, readEmbeddedBootstrap(t))
	scriptPath := filepath.Join(t.TempDir(), "unpack.sh")
	if err := os.WriteFile(scriptPath, []byte(unpacker), 0o700); err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		"escaping dir":      "#!grove-config-seed v1\n#!dir ../../etc\n#!file grove.toml 644\nx = 1\n",
		"absolute dir":      "#!grove-config-seed v1\n#!dir /etc/cron.d\n#!file grove.toml 644\nx = 1\n",
		"unlisted file":     "#!grove-config-seed v1\n#!file .bashrc 644\nrm -rf /\n",
		"executable mode":   "#!grove-config-seed v1\n#!file grove.toml 755\nx = 1\n",
		"unknown directive": "#!grove-config-seed v1\n#!exec rm -rf /\n",
		"content first":     "#!grove-config-seed v1\nx = 1\n",
		"no files":          "#!grove-config-seed v1\n",
	}
	for name, bundle := range cases {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			seedPath := filepath.Join(t.TempDir(), "seed.bundle")
			if err := os.WriteFile(seedPath, []byte(bundle), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", scriptPath)
			cmd.Env = append(os.Environ(), "HOME="+home, "GROVE_CONFIG_SEED="+seedPath)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("unpacker accepted a hostile seed:\n%s", out)
			}
		})
	}
}

func readEmbeddedBootstrap(t *testing.T) string {
	t.Helper()
	data, err := satelliteassets.BootstrapScript()
	if err != nil {
		t.Fatalf("BootstrapScript: %v", err)
	}
	return string(data)
}

// extractUnpacker slices the delimited config-seed unpacker out of the
// bootstrap script so a test can execute the very code the VM runs.
func extractUnpacker(t *testing.T, script string) string {
	t.Helper()
	const begin = "# --- config-seed unpacker (begin) ---"
	const end = "# --- config-seed unpacker (end) ---"
	_, rest, ok := strings.Cut(script, begin)
	if !ok {
		t.Fatal("bootstrap script has no config-seed unpacker begin marker")
	}
	body, _, ok := strings.Cut(rest, end)
	if !ok {
		t.Fatal("bootstrap script has no config-seed unpacker end marker")
	}
	return body
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
