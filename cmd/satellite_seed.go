package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/grovetools/core/config"
)

// The VM's config seed — `grove satellite up` as materialize + provisioning.
//
// satellite-bootstrap.sh step 5 used to write the VM's grove.toml and sync.toml
// from hand-written heredocs: a `cat > grove.toml <<'CFG'` block and a `printf`
// loop that assembled `[[workspaces]]` entries in bash. Both are gone. The CLI
// now renders the same three files through core/config's shared config-seed
// writer and ships them as a bundle; step 5 only unpacks what it is handed.
//
// Three things that were structurally impossible before become true by
// construction:
//
//   - The VM's `[[workspaces]]` entries go through the role-aware editor's
//     single rendering choke point (config.RenderSyncWorkspaces), so the
//     push-only invariant is enforced on the VM's file by the same code that
//     enforces it on the laptop's.
//   - The VM declares its ecosystem as machine INTENT (machine.toml
//     `[machine.ecosystems.*]`) like every other machine, rather than as a raw
//     `[groves.*]` entry.
//   - The generated TOML is parsed back through the real loaders before it
//     leaves the laptop, so a malformed seed fails here rather than as a
//     silently broken daemon on a remote host.
//
// ROLE. The VM's own entries are role = "peer", NOT role = "satellite". The
// contract's §1 Q5 sentence ("the role-aware editor writing role = satellite
// entries") describes the LAPTOP's file, which renderLaptopSyncWorkspaces
// already stamps. A satellite-role entry is push-only BY DEFINITION
// (config.validateSyncWorkspaceRole hard-refuses satellite + pull), and the
// whole point of the VM's sync.toml is that the VM pulls. What the VM is doing
// — a machine mirroring its own notebook from its own syncd — is exactly what
// the contract calls peer in Q5 step 4. See the deviation ledger.
//
// TIER. Satellites stay FLAT regardless of the ecosystem card's layout
// (contract §1 Q5): the VM gets repos, not a submodule-anchored superrepo, so
// submodule-anchored worktree/plan tooling does not work there. That is a named
// capability loss for a disposable tier, stated in `grove satellite up --help`.

const (
	// satelliteEcosystemName is the ecosystem the satellite tier provisions.
	// It matches bootstrapRemoteCodeDir, which the bootstrap clones into.
	satelliteEcosystemName = "grovetools"
	// satelliteNotebookRoot is where the VM keeps its notebook tree. The
	// workspace skeletons the seed creates hang off it.
	satelliteNotebookRoot = "~/notebooks/" + satelliteEcosystemName
	// satelliteSyncTokenCommand reads the token the bootstrap mints ON the VM
	// (step 6). Tokens are never rendered into the seed.
	satelliteSyncTokenCommand = "cat ~/.config/grove/sync.token"
)

// satelliteWorkspaceSkeleton is the per-workspace directory set the VM's
// notebook needs. Lifted verbatim from the bootstrap's step-5 mkdir loop.
var satelliteWorkspaceSkeleton = []string{"inbox", "plans", "concepts", "daily", "quick"}

// buildSatelliteConfigSeed renders the config a satellite VM boots with.
//
// `name` is the satellite's registry name and becomes its [machine] name, so
// the VM shows up as itself on identity surfaces instead of as a bare hostname.
// `workspaces` are the sync workspaces resolved by `up` — the same list the
// laptop's push-only entries are generated from.
func buildSatelliteConfigSeed(name string, workspaces []string) config.ConfigSeed {
	seed := config.ConfigSeed{
		Provenance:  fmt.Sprintf("Written by `grove satellite up %s` (config seed).", name),
		MachineName: name,
		Ecosystems: map[string]config.MachineEcosystem{
			satelliteEcosystemName: {
				Path:        bootstrapRemoteCodeDir,
				Notebook:    satelliteEcosystemName,
				Description: "Grove ecosystem (satellite)",
			},
		},
		Notebooks: map[string]string{satelliteEcosystemName: satelliteNotebookRoot},
		DaemonSSH: true,
		// Migration window: in source mode the VM builds grove from the
		// grovetools superrepo's PINNED submodule SHAs, which may predate
		// machine.toml support entirely. An explicit [groves.*] entry wins over
		// a compiled one, so shipping both is correct on a new grove and the
		// only thing that works on an old one. Remove once the pins carry
		// machine-config support.
		LegacyGroves: true,
	}

	// A satellite with no sync workspaces (`--sync-port 0`, or an explicitly
	// empty list) gets no sync.toml at all rather than an empty one.
	if len(workspaces) > 0 {
		entries := make([]config.SyncWorkspace, 0, len(workspaces))
		for _, ws := range workspaces {
			entries = append(entries, config.SyncWorkspace{
				Name: ws,
				Role: config.SyncRolePeer,
				Pull: true,
			})
			for _, sub := range satelliteWorkspaceSkeleton {
				seed.Dirs = append(seed.Dirs,
					strings.TrimPrefix(satelliteNotebookRoot, "~/")+"/workspaces/"+ws+"/"+sub)
			}
		}
		seed.Sync = &config.SyncSeed{
			Server:       "http://" + defaultSyncRemoteAddr,
			TokenCommand: satelliteSyncTokenCommand,
			Workspaces:   entries,
		}
	}
	return seed
}

// writeSatelliteConfigSeed renders the seed into a bundle file in the
// satellite's own state directory and returns its path.
//
// The bundle is a local file the bootstrap script `cat`s onto the SSH stream:
// its PATH is safe in argv (it names nothing secret) and its CONTENT never
// reaches remote shell as an interpolated value — the remote unpacker reads it
// line by line. It is left on disk deliberately, next to the per-satellite
// terraform state: re-reading the exact bytes a satellite was provisioned with
// is the first thing anyone debugging its config wants.
//
// `fallbackDir` is used when the satellite state dir cannot be resolved (the
// provider's local run dir), so a seed always has somewhere to land.
func writeSatelliteConfigSeed(fallbackDir, name string, workspaces []string) (string, error) {
	bundle, err := buildSatelliteConfigSeed(name, workspaces).Bundle()
	if err != nil {
		return "", err
	}
	dir, err := satelliteStateDir(name)
	if err != nil || dir == "" {
		dir = fallbackDir
	}
	if dir == "" {
		return "", fmt.Errorf("no directory to stage the config seed in")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	path := filepath.Join(dir, "config-seed.bundle")
	if err := os.WriteFile(path, []byte(bundle), 0o600); err != nil {
		return "", fmt.Errorf("write config seed: %w", err)
	}
	return path, nil
}
