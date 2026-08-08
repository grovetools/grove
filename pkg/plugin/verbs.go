package plugin

import (
	"fmt"
	"path/filepath"
	"strings"
)

// A tool plugin's claim on the command line is its VERB SET: the distinct
// first tokens of the phrases its manifest provides. `grove <verb> ...`
// reaches whatever binary owns the verb, so two owners for one verb is not a
// preference question — it is two programs both believing they answer the
// same command. The installer therefore refuses the collision loudly at
// install time, before the consent prompt, and dispatch resolves a verb
// against the lockfile knowing every recorded verb has exactly one owner.

// ToolVerbs returns the distinct dispatch verbs of a provides list — the
// first token of each phrase, in declaration order. The phrases are the
// manifest's bare form ("forge up", "forge status"); for the recorded consent
// lines, which ToolFacts prefixed with "grove ", use PinVerbs.
func ToolVerbs(provides []string) []string {
	var out []string
	seen := make(map[string]bool, len(provides))
	for _, phrase := range provides {
		fields := strings.Fields(phrase)
		if len(fields) == 0 {
			continue
		}
		if v := fields[0]; !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// PinVerbs returns the verb set an installed pin's approval recorded. The
// consent lines are ToolFacts output — each phrase spelled as the command it
// enables, "grove forge up" — so exactly one "grove " prefix is peeled before
// the verb is read. A panel pin records no provides and has no verbs.
func PinVerbs(pin *Pin) []string {
	if pin == nil || len(pin.Consent.Provides) == 0 {
		return nil
	}
	bare := make([]string, 0, len(pin.Consent.Provides))
	for _, line := range pin.Consent.Provides {
		bare = append(bare, strings.TrimPrefix(line, "grove "))
	}
	return ToolVerbs(bare)
}

// ResolveToolVerb finds the installed tool plugin that answers `grove <verb>`:
// a pin whose Kind is "tool" and whose verb set — or installed binary's
// basename, for a tool invoked by its binary's name rather than a declared
// phrase — matches verb. Deterministic (pins are scanned in name order),
// though install-time collision refusal is what actually keeps a verb from
// having two owners.
//
// It reports the pin as recorded and does not check that the binary still
// exists — the caller is the one with an error message to render, and "the
// verb is owned but its binary is gone" is a different sentence from "nothing
// owns this verb".
func ResolveToolVerb(lock *Lock, verb string) (name string, pin *Pin, ok bool) {
	if lock == nil || verb == "" {
		return "", nil, false
	}
	for _, n := range lock.Names() {
		p := lock.Plugins[n]
		if p == nil || p.Kind != "tool" {
			continue
		}
		for _, v := range PinVerbs(p) {
			if v == verb {
				return n, p, true
			}
		}
		if p.Binary != "" && filepath.Base(p.Binary) == verb {
			return n, p, true
		}
	}
	return "", nil, false
}

// refuseVerbCollisions is the install-time gate: every verb the manifest
// claims must be unowned. It runs after the manifest is loaded and before the
// consent prompt — a user should never be asked to approve an install that
// cannot proceed — and every refusal names both parties, because the remedy
// (remove one, rename one, wait for a grove release) depends on who the other
// party is.
//
// The plugin's own entry is skipped so an update can re-claim its own verbs.
func (in *Installer) refuseVerbCollisions(m *Manifest, lock *Lock) error {
	verbs := ToolVerbs(m.Tool.Provides)

	// (b) names grove itself answers to: built-in commands, registered
	// ecosystem tools. Nil-safe — an Installer wired without ReservedVerbs
	// simply has no reserved names to defend.
	reserved := make(map[string]bool, len(in.ReservedVerbs))
	for _, r := range in.ReservedVerbs {
		reserved[r] = true
	}
	for _, v := range verbs {
		if reserved[v] {
			return fmt.Errorf("cannot install %q: it provides `grove %s`, and %q is already a grove command (a built-in, or a registered ecosystem tool) — grove's own name wins, so the install would produce a command that never runs", m.Plugin.Name, v, v)
		}
	}

	// (a) other installed plugins: their recorded verb sets, and their
	// installed binary names — a tool can be invoked by its binary's basename,
	// so a verb equal to any plugin's binary name is the same collision.
	for _, other := range lock.Names() {
		if other == m.Plugin.Name {
			continue
		}
		pin := lock.Plugins[other]
		otherVerbs := make(map[string]bool)
		for _, v := range PinVerbs(pin) {
			otherVerbs[v] = true
		}
		var binName string
		if pin.Binary != "" {
			binName = filepath.Base(pin.Binary)
		}
		for _, v := range verbs {
			if otherVerbs[v] {
				return fmt.Errorf("cannot install %q: it provides `grove %s`, which the installed plugin %q already provides — remove one of them (`grove plugin remove %s`) or rename the verb", m.Plugin.Name, v, other, other)
			}
			if v == binName {
				return fmt.Errorf("cannot install %q: it provides `grove %s`, which is the installed binary name of plugin %q — remove one of them (`grove plugin remove %s`) or rename the verb", m.Plugin.Name, v, other, other)
			}
		}
	}
	return nil
}
