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

	srcDir, err := SourceDir(src.Slug)
	if err != nil {
		return nil, err
	}
	_, statErr := os.Stat(srcDir)
	freshCheckout := os.IsNotExist(statErr)

	in.progress("Fetching %s", src.URL)
	if err := Fetch(src, srcDir); err != nil {
		if freshCheckout {
			_ = os.RemoveAll(srcDir)
		}
		return nil, err
	}
	commit, err := Resolve(srcDir, src.Ref)
	if err != nil {
		return nil, err
	}
	if err := Checkout(srcDir, commit); err != nil {
		return nil, err
	}
	resolved := ResolvedSource{Source: src, Commit: commit, Dir: srcDir}

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

	fragmentPath, err := FragmentPath(name)
	if err != nil {
		return nil, err
	}
	binPath, err := BinPath(manifest.BinaryName())
	if err != nil {
		return nil, err
	}
	versionDir, err := VersionDir(name, commit)
	if err != nil {
		return nil, err
	}
	versionBinary := filepath.Join(versionDir, "bin", manifest.BinaryName())

	facts := NewConsentFacts(manifest, resolved, manifestBytes, binPath)
	digest := facts.Digest()

	if existing != nil && existing.ConsentDigest == digest && !opts.Force {
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
		Spec:           src.Spec,
		URL:            src.URL,
		Ref:            src.Ref,
		Commit:         commit,
		ManifestDigest: facts.ManifestDigest,
		ConsentDigest:  digest,
		Consent:        facts,
		SourceDir:      srcDir,
		VersionBinary:  versionBinary,
		Binary:         binPath,
		Fragment:       fragmentPath,
	}

	fragment, err := RenderFragment(manifest, binPath, pin)
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
	if opts.Ref == "" {
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

	if !keepSource && pin.SourceDir != "" && !lock.UsesSourceDir(pin.SourceDir, name) {
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

// Status is one row of `grove plugin list`.
type Status struct {
	Name string
	Pin  *Pin
	// FragmentPresent and BinaryPresent report whether what the pin claims is
	// actually on disk.
	FragmentPresent bool
	BinaryPresent   bool
	// Approved reports whether the exec-trust store still holds the approval
	// for this exact pin. False means the fragment or the lock entry was
	// edited outside `grove plugin`.
	Approved bool
}

// List reports every installed plugin and whether it is intact.
func List() ([]Status, error) {
	lock, err := LoadLock()
	if err != nil {
		return nil, err
	}
	out := make([]Status, 0, len(lock.Plugins))
	for _, name := range lock.Names() {
		pin := lock.Plugins[name]
		st := Status{Name: name, Pin: pin, Approved: IsApproved(pin.Fragment, pin.ConsentDigest)}
		if _, err := os.Stat(pin.Fragment); err == nil {
			st.FragmentPresent = true
		}
		if _, err := os.Stat(pin.Binary); err == nil {
			st.BinaryPresent = true
		}
		out = append(out, st)
	}
	return out, nil
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
