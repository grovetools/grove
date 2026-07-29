package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/grovetools/core/pkg/exectrust"
)

// Install-time trust is the consent moment.
//
// An installed plugin is a process treemux spawns on your machine every time
// it boots. The decision to allow that is made once, against a screen showing
// what will run — and it is recorded in core/pkg/exectrust, the SAME MAC'd
// store `grove config trust` writes, keyed by the manifest fragment the
// installer is about to write. There is deliberately no second trust store:
// the provenance work already learned that lesson, and a plugin the user
// forgot about should show up in one `grove config trust --list`, not two.
//
// The digest binds the decision to the pinned commit, so an approval covers
// that ref and nothing else. `update` recomputes it, finds a different digest,
// and asks again with a diff.

// ConsentFacts is everything the user is shown before an install proceeds, and
// everything the approval is bound to.
type ConsentFacts struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Homepage    string `json:"homepage,omitempty"`

	// Source is the display form of what is being installed: url@ref.
	Source string `json:"source"`
	Commit string `json:"commit"`
	// ManifestDigest hashes the grove-plugin.toml bytes, so an edit anywhere
	// in the manifest re-opens the question even if no field below changed.
	ManifestDigest string `json:"manifest_digest"`

	// Build is the argv that runs in the checkout. Empty means no build step.
	Build []string `json:"build,omitempty"`
	// Run is the argv treemux will spawn.
	Run []string `json:"run"`
	// Env is the extra environment the panel is spawned with.
	Env []string `json:"env,omitempty"`

	Protocol string   `json:"protocol,omitempty"`
	Icon     string   `json:"icon,omitempty"`
	Keys     []string `json:"keys,omitempty"`
}

// NewConsentFacts assembles the consent screen's content from a validated
// manifest and a resolved source. runBinary is the absolute path treemux will
// spawn — the installed binary, not the one in the checkout.
func NewConsentFacts(m *Manifest, src ResolvedSource, manifestBytes []byte, runBinary string) ConsentFacts {
	keys := make([]string, 0, len(m.Panel.Keys))
	for _, k := range m.Panel.Keys {
		keys = append(keys, fmt.Sprintf("%s — %s", k.Key, k.Description))
	}
	sum := sha256.Sum256(manifestBytes)
	return ConsentFacts{
		Name:           m.Plugin.Name,
		Description:    m.Plugin.Description,
		Homepage:       m.Plugin.Homepage,
		Source:         src.Display(),
		Commit:         src.Commit,
		ManifestDigest: "sha256:" + hex.EncodeToString(sum[:]),
		Build:          append([]string(nil), m.Build.Command...),
		Run:            append([]string{runBinary}, m.Panel.Args...),
		Env:            append([]string(nil), m.Panel.Env...),
		Protocol:       m.Panel.Protocol,
		Icon:           m.Panel.Icon,
		Keys:           keys,
	}
}

// Digest is the value recorded in the exec-trust store. It uses
// exectrust.Digest so a plugin approval hashes the same way a `grove config
// trust` approval does.
func (f ConsentFacts) Digest() string {
	parts := []string{
		"source=" + f.Source,
		"commit=" + f.Commit,
		"manifest=" + f.ManifestDigest,
		"build=" + strings.Join(f.Build, "\x1f"),
		"run=" + strings.Join(f.Run, "\x1f"),
		"env=" + strings.Join(f.Env, "\x1f"),
		"protocol=" + f.Protocol,
		"keys=" + strings.Join(f.Keys, "\x1f"),
	}
	return exectrust.Digest(parts)
}

// FactChange is one line of an update diff.
type FactChange struct {
	Field string
	Old   string
	New   string
}

// Diff reports what changed between an approved install and a proposed one,
// in the order the consent screen shows the fields. Only the fields the
// approval is bound to are compared; a new description or homepage is shown by
// the consent screen anyway and is not what the user is deciding about.
func Diff(old, next ConsentFacts) []FactChange {
	var out []FactChange
	add := func(field, a, b string) {
		if a != b {
			out = append(out, FactChange{Field: field, Old: a, New: b})
		}
	}
	add("source", old.Source, next.Source)
	add("commit", shortCommit(old.Commit), shortCommit(next.Commit))
	add("build", strings.Join(old.Build, " "), strings.Join(next.Build, " "))
	add("run", strings.Join(old.Run, " "), strings.Join(next.Run, " "))
	add("env", strings.Join(old.Env, " "), strings.Join(next.Env, " "))
	add("protocol", old.Protocol, next.Protocol)
	add("keys", strings.Join(old.Keys, ", "), strings.Join(next.Keys, ", "))
	// The icon is cosmetic, but the manifest digest covers it, so a changed
	// icon alone re-opens the prompt. Showing it keeps the screen from saying
	// "nothing you approved has changed" while asking about something.
	add("icon", old.Icon, next.Icon)
	return out
}

// trustKey is the path an approval is filed under.
//
// exectrust canonicalizes its keys through EvalSymlinks, which only resolves
// for a path that EXISTS. A fragment does not exist when an install is
// approved and no longer exists when one is removed, so letting the store
// canonicalize would file the record under one path and look it up under
// another (on macOS, /var/... versus /private/var/...). Resolving the
// directory — which always exists at both moments — and rejoining the basename
// gives the same key at every point in a plugin's life.
func trustKey(fragmentPath string) string {
	dir, base := filepath.Split(fragmentPath)
	resolved, err := filepath.EvalSymlinks(filepath.Clean(dir))
	if err != nil {
		return fragmentPath
	}
	return filepath.Join(resolved, base)
}

// RecordApproval writes the install decision into the exec-trust store,
// keyed by the manifest fragment path.
func RecordApproval(fragmentPath, digest string, now time.Time) error {
	store := exectrust.Load()
	store.Trust(trustKey(fragmentPath), digest, now)
	if err := store.Save(); err != nil {
		return fmt.Errorf("record the install approval: %w", err)
	}
	return nil
}

// RevokeApproval drops the trust record for a fragment. Used by remove, so an
// uninstall does not leave a decision behind about a file that no longer
// exists.
func RevokeApproval(fragmentPath string) error {
	store := exectrust.Load()
	if !store.Revoke(trustKey(fragmentPath)) {
		return nil
	}
	if err := store.Save(); err != nil {
		return fmt.Errorf("drop the install approval: %w", err)
	}
	return nil
}

// IsApproved reports whether the fragment is still trusted at the digest that
// was approved. A false here means the fragment or the pin was edited outside
// `grove plugin`, which `grove plugin list` surfaces rather than repairing.
func IsApproved(fragmentPath, digest string) bool {
	return exectrust.Load().IsTrusted(trustKey(fragmentPath), digest)
}

// shortCommit abbreviates a commit for display, leaving anything that is not a
// full hash alone.
func shortCommit(c string) string {
	if len(c) >= 12 {
		return c[:12]
	}
	return c
}
