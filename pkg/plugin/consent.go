package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
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

	// Source is the display form of what is being installed: url@ref, or
	// "<path> (working tree)" for a development install.
	Source string `json:"source"`
	Commit string `json:"commit"`
	// Dev reports that this approval covers a working tree rather than a
	// commit. It is a consent fact in its own right, not a display detail: it
	// is the difference between approving something fixed and approving
	// whatever that directory contains the next time the panel is rebuilt.
	Dev bool `json:"dev,omitempty"`
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
	Label    string   `json:"label,omitempty"`
	Keys     []string `json:"keys,omitempty"`
	// Views is the panel's view declaration, one line per view in declaration
	// order, with the one a drawer pane would default to marked.
	//
	// Flattened to lines for the reason Settings is: this struct is compared and
	// digested as text. Ordered rather than sorted, because the order IS one of
	// the facts — it decides which view an installed panel offers a drawer.
	//
	// Like the keys above it grants nothing, so it is on the screen for the same
	// reason: an update that stops offering `compact` to the drawer, or starts
	// offering something else, changes what the user will see in their drawer and
	// should not pass silently.
	Views []string `json:"views,omitempty"`
	// Settings is the manifest's default settings table, flattened to sorted
	// "dotted.key = value" lines.
	//
	// Flattened rather than carried as a map because everything in ConsentFacts
	// is compared and digested as text: a map's iteration order would make the
	// digest unstable, and Diff would have nothing to show a user but "the
	// settings changed". A line per leaf means an update diff names the setting
	// that moved and both of its values.
	//
	// It is here despite grove never executing any of it. A panel's defaults
	// decide what it does on first run, an update that changes one changes the
	// behavior the user approved, and the manifest digest already re-opens the
	// prompt for it — showing the values is what makes that prompt answerable.
	Settings []string `json:"settings,omitempty"`
}

// NewConsentFacts assembles the consent screen's content from a validated
// manifest and a resolved source. runBinary is the absolute path treemux will
// spawn — the installed binary, not the one in the checkout.
func NewConsentFacts(m *Manifest, src ResolvedSource, manifestBytes []byte, runBinary string) ConsentFacts {
	keys := make([]string, 0, len(m.Panel.Keys))
	for _, k := range m.Panel.Keys {
		keys = append(keys, fmt.Sprintf("%s — %s", k.Key, k.Description))
	}
	preferred := m.Panel.PreferredDrawerView()
	views := make([]string, 0, len(m.Panel.Views))
	for _, name := range m.Panel.ViewNames() {
		line := fmt.Sprintf("%s — %s", name, m.Panel.Views[name].Description)
		switch {
		case name == preferred:
			line += " (what a drawer pane gets by default)"
		case m.Panel.Views[name].Drawer:
			line += " (also offered to a drawer pane)"
		}
		views = append(views, line)
	}
	sum := sha256.Sum256(manifestBytes)
	return ConsentFacts{
		Name:           m.Plugin.Name,
		Description:    m.Plugin.Description,
		Homepage:       m.Plugin.Homepage,
		Source:         src.Display(),
		Commit:         src.Commit,
		Dev:            src.Dev,
		ManifestDigest: "sha256:" + hex.EncodeToString(sum[:]),
		Build:          append([]string(nil), m.Build.Command...),
		Run:            append([]string{runBinary}, m.Panel.Args...),
		Env:            append([]string(nil), m.Panel.Env...),
		Protocol:       m.Panel.Protocol,
		Icon:           m.Panel.Icon,
		Label:          m.Panel.Label,
		Keys:           keys,
		Views:          views,
		Settings:       FlattenSettings(m.Panel.Settings),
	}
}

// FlattenSettings renders a settings table as sorted "dotted.key = value"
// lines. Sorted so the result is stable across runs — the approval digest is
// computed over it, and a map's iteration order would make an unchanged
// manifest hash differently every time.
//
// Exported because the fragment writer and the consent screen must agree on
// what a setting is called; a user reading "timer.work_minutes = 25" at the
// prompt should find the same path in the file grove writes.
func FlattenSettings(settings map[string]any) []string {
	var out []string
	flattenSettingsInto(&out, "", settings)
	sort.Strings(out)
	return out
}

func flattenSettingsInto(out *[]string, prefix string, v any) {
	switch v := v.(type) {
	case map[string]any:
		for name, value := range v {
			key := name
			if prefix != "" {
				key = prefix + "." + name
			}
			flattenSettingsInto(out, key, value)
		}
	case []any:
		// Arrays render whole rather than one line per element: an ordered
		// list is one decision, and splitting it would let a reordering read
		// as several unrelated changes in an update diff.
		parts := make([]string, 0, len(v))
		for _, e := range v {
			parts = append(parts, fmt.Sprintf("%v", e))
		}
		*out = append(*out, fmt.Sprintf("%s = [%s]", prefix, strings.Join(parts, ", ")))
	default:
		*out = append(*out, fmt.Sprintf("%s = %v", prefix, v))
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
		"label=" + f.Label,
		"settings=" + strings.Join(f.Settings, "\x1f"),
	}
	// Appended only when there are views, so a manifest that declares none
	// hashes exactly as it did before views existed. Every plugin installed
	// before this field hashes the same way it did when it was approved, and an
	// unconditional line would have re-opened the prompt for all of them to ask
	// about something none of them says.
	if len(f.Views) > 0 {
		parts = append(parts, "views="+strings.Join(f.Views, "\x1f"))
	}
	// Appended only when set, for the same round-tripping reason as views: no
	// previously-approved plugin re-opens its prompt. When it IS set it must be
	// in the digest, so an approval granted to a pinned commit can never be
	// reused to run a mutable working tree — the exec-trust store would
	// otherwise see the same value for two materially different things.
	if f.Dev {
		parts = append(parts, "dev=true")
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
	// One row rather than one per view: the order is part of what changed (it
	// decides the drawer default), and a per-line diff keyed on the view name
	// would report a reordering as nothing at all.
	add("views", strings.Join(old.Views, ", "), strings.Join(next.Views, ", "))
	add("label", old.Label, next.Label)
	// One line per changed setting rather than one "settings" row: an update
	// that retunes a default should say which one and from what.
	out = append(out, diffLines("settings", old.Settings, next.Settings)...)
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

// diffLines reports per-line changes between two flattened key = value lists,
// keyed by the part before the first "=" so a changed VALUE reads as a change
// rather than as one removal plus one addition.
func diffLines(field string, old, next []string) []FactChange {
	index := func(lines []string) map[string]string {
		m := make(map[string]string, len(lines))
		for _, l := range lines {
			key, value, found := strings.Cut(l, " = ")
			if !found {
				key = l
			}
			m[key] = value
		}
		return m
	}
	oldByKey, nextByKey := index(old), index(next)

	keys := make([]string, 0, len(oldByKey)+len(nextByKey))
	seen := make(map[string]bool, len(keys))
	for _, m := range []map[string]string{oldByKey, nextByKey} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	sort.Strings(keys)

	var out []FactChange
	for _, k := range keys {
		before, hadBefore := oldByKey[k]
		after, hasAfter := nextByKey[k]
		if hadBefore && hasAfter && before == after {
			continue
		}
		out = append(out, FactChange{Field: field + "." + k, Old: before, New: after})
	}
	return out
}
