package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// ErrDeclined is returned when the user does not approve an install. It is an
// error so no caller can mistake a declined install for a completed one, and
// the CLI prints it without a stack of "failed to" wrapping.
var ErrDeclined = errors.New("declined")

// Installer drives the pipeline. Confirm is the consent gate: it is called
// once, before anything is built, written or trusted, and returning false
// stops the install with nothing left behind.
type Installer struct {
	// Out receives progress lines.
	Out io.Writer
	// Confirm shows the request and reports whether the user approved it.
	// A nil Confirm declines everything, which is the safe default for a
	// caller that forgot to wire one.
	Confirm func(*ConsentRequest) (bool, error)
	// Now stamps the lockfile and the trust record. Injected for tests.
	Now func() time.Time
	// ReservedVerbs are the names `grove <verb>` already answers to without
	// consulting the lockfile — built-in commands, registered ecosystem tools.
	// A tool manifest claiming one of them is refused at install time (see
	// refuseVerbCollisions), because grove's own name wins at dispatch and the
	// install would produce a command that never runs. Nil is simply "no
	// reserved names"; the CLI populates it from its command tree and the sdk
	// registry.
	ReservedVerbs []string
}

// ConsentRequest is everything the user needs in order to answer "should this
// run on my machine?".
type ConsentRequest struct {
	// Facts is what is being approved.
	Facts ConsentFacts
	// Previous is the approved state being replaced, nil on a first install.
	Previous *ConsentFacts
	// Changes is Previous -> Facts, empty on a first install.
	Changes []FactChange
	// Unknown lists manifest keys this grove did not understand.
	Unknown []string

	FragmentPath string
	BinaryPath   string
	SourceDir    string
}

// IsUpdate reports whether this request replaces an existing approval.
func (r *ConsentRequest) IsUpdate() bool { return r.Previous != nil }

// Options are the knobs shared by install and update.
type Options struct {
	// Ref overrides the ref in the spec (`--ref`).
	Ref string
	// Force reinstalls even when the resolved commit is already installed.
	Force bool
	// Dev installs from a local working tree in place: no clone, no ref, no
	// pin, and the build runs where the user develops so it resolves
	// dependencies the same way their own `go build` does. See DevSource.
	Dev bool
}

// Result reports what an install did.
type Result struct {
	Name string
	Pin  *Pin
	// Action is "installed", "updated" or "unchanged".
	Action string
}

// Install runs the whole pipeline for one source: fetch, pin, read the
// manifest, ask, build, install, declare, record.
//
// The order is the security-relevant part. Cloning and reading the manifest
// happen first because there is nothing to show the user until they have —
// but a clone runs no code from the repository. Everything that does run
// something, or writes anything grove will later act on, happens after the
// consent gate.
func (in *Installer) Install(ctx context.Context, spec string, opts Options) (*Result, error) {
	src, err := ParseSource(spec, opts.Ref)
	if err != nil {
		return nil, err
	}

	var (
		srcDir        string
		commit        string
		freshCheckout bool
	)
	if opts.Dev {
		// No fetch, no resolve, no checkout — and note that freshCheckout stays
		// false, so none of the abandon paths below can delete the directory.
		// This is the user's own tree, not a checkout grove owns.
		if srcDir, commit, err = DevSource(src); err != nil {
			return nil, err
		}
		in.progress("Development install — building in place in %s", srcDir)
	} else {
		if srcDir, err = SourceDir(src.Slug); err != nil {
			return nil, err
		}
		_, statErr := os.Stat(srcDir)
		freshCheckout = os.IsNotExist(statErr)

		in.progress("Fetching %s", src.URL)
		if err := Fetch(src, srcDir); err != nil {
			if freshCheckout {
				_ = os.RemoveAll(srcDir)
			}
			return nil, err
		}
		if commit, err = Resolve(srcDir, src.Ref); err != nil {
			return nil, err
		}
		if err := Checkout(srcDir, commit); err != nil {
			return nil, err
		}
	}
	resolved := ResolvedSource{Source: src, Commit: commit, Dir: srcDir, Dev: opts.Dev}

	manifest, manifestBytes, err := LoadManifest(srcDir)
	if err != nil {
		return nil, err
	}
	name := manifest.Plugin.Name

	lock, err := LoadLock()
	if err != nil {
		return nil, err
	}
	existing := lock.Get(name)
	if existing != nil && existing.URL != src.URL {
		return nil, fmt.Errorf("plugin %q is already installed from %s — remove it before installing it from %s", name, existing.URL, src.URL)
	}
	// The installed fragment is also the user's settings store. An update must
	// refresh the manifest-owned declaration without resetting values changed
	// through `grove plugin set` (or the Plugins panel). Overlay the current
	// values onto the new defaults: new settings appear, while every value the
	// user already had remains theirs. Do this before consent facts are built so
	// the prompt, trust digest, fragment and lockfile all describe one state.
	if existing != nil && manifest.Kind() != "tool" {
		current, ok, err := readFragmentSettings(existing.Fragment, name)
		if err != nil {
			return nil, err
		}
		if ok {
			manifest.Panel.Settings = overlaySettings(manifest.Panel.Settings, current)
		}
	}
	// A tool's verbs must be unowned BEFORE the user is asked anything: a
	// consent screen for an install that cannot proceed is a question with no
	// right answer. The plugin's own lock entry is skipped inside, so an
	// update re-claims its own verbs without colliding with itself.
	if manifest.Kind() == "tool" {
		if err := in.refuseVerbCollisions(manifest, lock); err != nil {
			return nil, err
		}
	}

	fragmentPath, err := FragmentPath(name)
	if err != nil {
		return nil, err
	}
	binPath, err := BinPath(manifest.BinaryName())
	if err != nil {
		return nil, err
	}
	// A dev install keys its version directory on the literal "dev" rather than
	// on a commit: there is no pin, and keying on HEAD would accumulate a
	// directory per commit for builds that are not reproducible from any of
	// them. Successive dev installs overwrite the one slot, which is the honest
	// shape — only the latest build exists.
	version := commit
	if opts.Dev {
		version = "dev"
	}
	versionDir, err := VersionDir(name, version)
	if err != nil {
		return nil, err
	}
	versionBinary := filepath.Join(versionDir, "bin", manifest.BinaryName())

	facts := NewConsentFacts(manifest, resolved, manifestBytes, binPath)
	digest := facts.Digest()

	// A dev install never short-circuits as "unchanged". The digest covers the
	// manifest, not the source, and the source is a directory the user is
	// actively editing — "nothing changed" is the one thing this mode can never
	// conclude. Rebuilding every time is the whole point of `install --dev`
	// being the iteration loop.
	if existing != nil && existing.ConsentDigest == digest && !opts.Force && !opts.Dev {
		if installed(existing) {
			in.progress("%s is already installed at %s", name, facts.Source)
			return &Result{Name: name, Pin: existing, Action: "unchanged"}, nil
		}
		// The pin says installed but the fragment or the binary is gone.
		// Repair it rather than reporting a no-op the user can see is wrong.
		in.progress("%s is pinned at %s but incomplete on disk — repairing", name, facts.Source)
	}

	if err := claimBinPath(binPath, existing); err != nil {
		return nil, err
	}

	req := &ConsentRequest{
		Facts:        facts,
		Unknown:      manifest.Unknown,
		FragmentPath: fragmentPath,
		BinaryPath:   binPath,
		SourceDir:    srcDir,
	}
	if existing != nil {
		prev := existing.Consent
		req.Previous = &prev
		req.Changes = Diff(prev, facts)
	}

	// Nothing has been built, written or trusted yet. The only thing this run
	// created is the checkout, so an install that does not proceed — declined,
	// or unanswerable because there is no terminal — leaves the machine
	// exactly as it found it.
	abandon := func() {
		if freshCheckout {
			_ = os.RemoveAll(srcDir)
		}
	}
	approved := false
	if in.Confirm != nil {
		ok, err := in.Confirm(req)
		if err != nil {
			abandon()
			return nil, err
		}
		approved = ok
	}
	if !approved {
		abandon()
		return nil, ErrDeclined
	}

	if len(manifest.Build.Command) > 0 {
		in.progress("Building %s", name)
		if err := runBuild(ctx, name, srcDir, manifest.Build.Command, in.writer()); err != nil {
			return nil, err
		}
	}
	if err := installBinary(filepath.Join(srcDir, manifest.Build.Binary), versionBinary, binPath); err != nil {
		return nil, err
	}
	in.progress("Installed %s", binPath)

	pin := &Pin{
		Spec: src.Spec,
		URL:  src.URL,
		Ref:  src.Ref,
		// facts.Kind is "tool" for a tool and "" for a panel, which is exactly
		// what the lockfile wants: every lockfile written before tools existed
		// carries no kind, and spelling the default out would keep old files
		// from round-tripping byte-identically.
		Kind:           facts.Kind,
		Commit:         commit,
		Dev:            opts.Dev,
		ManifestDigest: facts.ManifestDigest,
		ConsentDigest:  digest,
		Consent:        facts,
		SourceDir:      srcDir,
		VersionBinary:  versionBinary,
		Binary:         binPath,
		Fragment:       fragmentPath,
	}

	// Both kinds write a fragment at the same path, but a tool's is
	// comment-only: it is the trust anchor and the install record, not a
	// [tui.plugins] declaration — treemux must never see a pane for it.
	var fragment []byte
	if manifest.Kind() == "tool" {
		fragment, err = RenderToolFragment(manifest, binPath, pin)
	} else {
		fragment, err = RenderFragment(manifest, binPath, pin)
	}
	if err != nil {
		return nil, err
	}
	if err := WriteFragment(fragmentPath, fragment); err != nil {
		return nil, err
	}
	in.progress("Wrote %s", fragmentPath)

	if err := RecordApproval(fragmentPath, digest, in.now()); err != nil {
		return nil, err
	}

	action := "installed"
	if existing != nil {
		action = "updated"
	}
	lock.Set(name, pin, in.now())
	if err := lock.Save(); err != nil {
		return nil, err
	}
	return &Result{Name: name, Pin: pin, Action: action}, nil
}

// Update re-resolves a plugin's recorded ref and moves the pin to whatever it
// names now. It is the same pipeline as Install — deliberately, because an
// update has to ask the same question — with the recorded spec supplying the
// source and the recorded consent supplying the diff.
func (in *Installer) Update(ctx context.Context, name string, opts Options) (*Result, error) {
	lock, err := LoadLock()
	if err != nil {
		return nil, err
	}
	pin := lock.Get(name)
	if pin == nil {
		return nil, fmt.Errorf("%q is not installed", name)
	}
	// A dev entry stays a dev entry across updates. `update` on one means
	// "rebuild from the working tree", which is the iteration loop — so it
	// carries the mode forward rather than quietly re-pinning the panel to a
	// commit the user never asked for. A ref is meaningless here and DevSource
	// rejects one, so an explicit --ref becomes a clear error rather than a
	// silent mode switch.
	if pin.Dev {
		opts.Dev = true
		if opts.Ref != "" {
			return nil, fmt.Errorf("%s is a development install, which builds the working tree as it is — `--ref %s` cannot apply to it; reinstall without --dev to pin it", name, opts.Ref)
		}
	}
	if opts.Ref == "" && !opts.Dev {
		opts.Ref = pin.Ref
	}
	// The spec may carry its own ref; the pin's ref (or --ref) is the one
	// that governs, so pass the source without it.
	base, _ := splitRef(pin.Spec)
	result, err := in.Install(ctx, base, opts)
	if err != nil {
		return nil, err
	}
	if result.Name != name {
		return nil, fmt.Errorf("%s now calls itself %q — remove the old entry and install it under its new name", name, result.Name)
	}
	return result, nil
}

// Remove uninstalls a plugin: the fragment stops declaring it, the binary and
// its versions go, the checkout goes if nothing else uses it, the pin goes,
// and the install approval is withdrawn. Anything already missing is not an
// error — an uninstall's job is to leave nothing behind, not to insist on
// what was there.
func (in *Installer) Remove(name string, keepSource bool) ([]string, error) {
	lock, err := LoadLock()
	if err != nil {
		return nil, err
	}
	pin := lock.Get(name)
	if pin == nil {
		return nil, fmt.Errorf("%q is not installed", name)
	}

	var removed []string
	if pin.Fragment != "" {
		if err := os.Remove(pin.Fragment); err == nil {
			removed = append(removed, pin.Fragment)
		} else if !os.IsNotExist(err) {
			return removed, fmt.Errorf("remove %s: %w", pin.Fragment, err)
		}
	}

	versionsDir, err := PluginVersionsDir(name)
	if err != nil {
		return removed, err
	}
	removed = append(removed, removeBinary(pin, versionsDir)...)

	// pin.Dev is the guard that matters most in this function: a dev pin's
	// SourceDir is the user's own working tree, not a checkout grove created,
	// and removing a plugin must never delete the source someone is writing.
	if !keepSource && !pin.Dev && pin.SourceDir != "" && !lock.UsesSourceDir(pin.SourceDir, name) {
		if err := os.RemoveAll(pin.SourceDir); err == nil {
			removed = append(removed, pin.SourceDir)
		}
	}

	if pin.Fragment != "" {
		if err := RevokeApproval(pin.Fragment); err != nil {
			return removed, err
		}
	}

	lock.Remove(name)
	if err := lock.Save(); err != nil {
		return removed, err
	}
	return removed, nil
}

// installed reports whether a pin's artifacts are both still on disk.
func installed(pin *Pin) bool {
	if _, err := os.Stat(pin.Fragment); err != nil {
		return false
	}
	if _, err := os.Stat(pin.Binary); err != nil {
		return false
	}
	return true
}

func (in *Installer) now() time.Time {
	if in.Now != nil {
		return in.Now()
	}
	return time.Now()
}

func (in *Installer) writer() io.Writer {
	if in.Out != nil {
		return in.Out
	}
	return io.Discard
}

func (in *Installer) progress(format string, args ...any) {
	fmt.Fprintf(in.writer(), "  %s\n", fmt.Sprintf(format, args...))
}
