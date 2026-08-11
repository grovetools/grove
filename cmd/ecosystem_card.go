package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/machine"
)

// Ecosystem-card authoring, shared by `grove ecosystem init` (mints a card for
// a new ecosystem) and `grove ecosystem adopt` (backfills one into an existing
// ecosystem). Both derive the card the same way; only the confirmation
// behavior differs.

// detectEcosystemLayout classifies an ecosystem root the way materialize will
// later clone it: a .gitmodules at the root means the primary remote IS the
// superrepo and its submodules are the member repos; anything else is a flat
// set of independent repos.
//
// Detection is deliberately a single filesystem probe rather than a git query,
// so it gives the same answer in a fresh directory, a bare clone, and a
// worktree.
func detectEcosystemLayout(root string) string {
	if info, err := os.Stat(filepath.Join(root, ".gitmodules")); err == nil && !info.IsDir() {
		return config.LayoutSuperrepo
	}
	return config.LayoutFlat
}

// discoverEcosystemRemotes reads this ecosystem's remote URLs. A directory
// that is not a git repository (or has no remotes) yields none — that is the
// normal state of an ecosystem `init` just scaffolded, not an error.
//
// It reads `git config --get-regexp remote.*.url` rather than `git remote -v`
// deliberately: `remote -v` renders URLs through this host's
// `url.<base>.insteadOf` rewrites, and a card exists precisely to be read on
// some *other* machine, which may not share them. The raw configured URL is
// the portable one.
//
// The result is ordered "origin first, then alphabetically" so re-deriving the
// card produces byte-identical output and adopt stays a no-op.
func discoverEcosystemRemotes(root string) []config.EcosystemRemote {
	cmd := exec.Command("git", "config", "--get-regexp", `^remote\..*\.url$`)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		// Exit 1 is "no matching keys" — an ecosystem with no remotes yet.
		return nil
	}

	urls := make(map[string]string)
	for _, line := range strings.Split(string(out), "\n") {
		key, url, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || url == "" {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".url")
		if name == "" || name == key {
			continue
		}
		// A remote may declare several URLs (push mirrors); the first is the
		// fetch URL, which is what a peer clones from.
		if _, seen := urls[name]; !seen {
			urls[name] = url
		}
	}

	names := make([]string, 0, len(urls))
	for name := range urls {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if (names[i] == "origin") != (names[j] == "origin") {
			return names[i] == "origin"
		}
		return names[i] < names[j]
	})

	remotes := make([]config.EcosystemRemote, 0, len(names))
	for _, name := range names {
		remotes = append(remotes, config.EcosystemRemote{Name: name, URL: urls[name]})
	}
	return remotes
}

// discoverFlatMemberRemotes captures one clone URL per immediate member repo.
// Flat cards use the remote name as the destination directory, so the member
// directory name (not its local git remote name) is recorded. Immediate
// children are a deliberately bounded rule: adopt never wanders outside the
// ecosystem root or guesses through arbitrary nested directories.
func discoverFlatMemberRemotes(root string) []config.EcosystemRemote {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var remotes []config.EcosystemRemote
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		memberRoot := filepath.Join(root, entry.Name())
		top, err := exec.Command("git", "-C", memberRoot, "rev-parse", "--show-toplevel").Output()
		if err != nil {
			continue
		}
		memberInfo, memberErr := os.Stat(memberRoot)
		topInfo, topErr := os.Stat(strings.TrimSpace(string(top)))
		if memberErr != nil || topErr != nil || !os.SameFile(memberInfo, topInfo) {
			continue // ordinary directory inside a root repo, not a member repo
		}
		memberRemotes := discoverEcosystemRemotes(memberRoot)
		if len(memberRemotes) == 0 {
			continue
		}
		// discoverEcosystemRemotes orders origin first, then by name. The first
		// fetch URL is therefore the conventional clone source when available.
		remotes = append(remotes, config.EcosystemRemote{Name: entry.Name(), URL: memberRemotes[0].URL})
	}
	return remotes
}

// deriveEcosystemCard builds the card for the ecosystem rooted at root.
//
// The id is the one invariant: an existing card's id is carried forward
// untouched and a fresh ULID is minted only when there is none. Layout and
// remotes are always re-derived from the repo — they are observations, and an
// ecosystem that grew a .gitmodules or changed its origin should say so.
// Notebooks are carried forward, because they are a declaration no probe can
// reconstruct.
func deriveEcosystemCard(root string, existing *config.EcosystemCard) config.EcosystemCard {
	card := config.EcosystemCard{}
	if existing != nil {
		card.ID = existing.ID
	}
	if card.ID == "" {
		card.ID = machine.NewID()
	}
	return card
}

// setEcosystemDefaultNotebook records name as the card's default notebook,
// demoting any previous default. A no-op for an empty name.
func setEcosystemDefaultNotebook(card *config.EcosystemCard, name string) {
	// Notebook routing is machine-local recorded configuration. Ecosystem cards
	// are identity-only and deliberately cannot carry a default notebook.
}

// proposeEcosystemNotebook returns the notebook this machine already
// associates with the ecosystem at absPath — the grove entry covering it names
// one — so adopt can offer the binding the user is de facto already using
// instead of asking them to invent one.
func proposeEcosystemNotebook(absPath string, cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	if discoverable, groveName := isEcosystemDiscoverable(absPath, cfg); discoverable {
		if entry, ok := cfg.Groves[groveName]; ok && entry.Notebook != "" {
			return entry.Notebook
		}
	}
	if cfg.Notebooks != nil && cfg.Notebooks.Rules != nil {
		return cfg.Notebooks.Rules.Default
	}
	return ""
}

// renderEcosystemCardSummary is the human-readable form both verbs print (and
// adopt shows before asking for confirmation).
func renderEcosystemCardSummary(card config.EcosystemCard) string {
	return fmt.Sprintf("  id:      %s\n", card.ID)
}
