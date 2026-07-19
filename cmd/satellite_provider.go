package cmd

// The provider seam for `grove satellite up`/`down`: a satelliteProvider owns
// HOW a satellite machine is created and destroyed (gcp: terraform against
// the embedded module), while the shared verbs own everything around it —
// option loading/validation, bootstrap, host-key pin, registry/state writes,
// config push, repo mirror, sync finishing, daemon reload. Infra targets
// resolve through the provider registry below; gcp (terraform, full-stack
// default), tart (local Apple-Silicon VMs, exec-only, no bootstrap script),
// and docker (local containers, exec-only, no bootstrap script) are the
// providers today. The gcp embedded-terraform target resolution is a
// gcp-private detail behind the seam.

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
)

// satelliteEndpoint is how the shared `up` verb reaches the machine a
// provider created.
type satelliteEndpoint struct {
	// SSHAddr is host:port (gcp: "<external_ip>:22").
	SSHAddr string
	// User is the SSH login user on the machine.
	User string
	// IdentityFile is the optional SSH private key (empty = agent-only auth).
	IdentityFile string
	// LocalRunDir is the provider's local working dir (gcp: the terraform
	// dir). The shared verb runs the bootstrap subprocess with it as cwd,
	// preserving the pre-seam behavior. Empty = caller cwd.
	LocalRunDir string
	// ProviderRef is the provider's machine handle for the satellite, stamped
	// into the state entry so a later `up`/`down` can recognize the machine
	// as grove-created (tart: "tart:<vm-name>"; empty for gcp, whose
	// terraform state already owns that mapping).
	ProviderRef string
}

// satelliteUpOptions carries the merged inputs a provider's PrepareUp/Up
// need. One pragmatic struct rather than per-provider option types: providers
// read the fields they understand (gcp reads all of them). Passed by pointer
// so Up can write RESOLVED values back (gcp: the auto-detected CIDR) for the
// shared verb to persist.
type satelliteUpOptions struct {
	// Name is the satellite name (gcp: terraform var vm_name).
	Name string
	// Infra is the merged [satellites.<name>.infra] block, flags already
	// applied.
	Infra satelliteInfraConfig
	// TFDir is the --tf-dir bring-your-own-terraform-module escape hatch (a
	// gcp option; see tfDirFlagHelp).
	TFDir string
	// AssumeYes skips the provider's billable-confirm prompt (--yes).
	AssumeYes bool
	// ServiceAccountEmail is the provision block's service account (gcp:
	// terraform var service_account_email).
	ServiceAccountEmail string
	// PostConfirm, when non-nil, runs after the provider's confirm prompt
	// succeeds and BEFORE any resource is created. The shared verb resolves
	// the provision-token commands here so a broken one aborts while the
	// provision is still free — preserving the historical
	// confirm → tokens → terraform order. An error aborts Up.
	PostConfirm func() error
}

// satelliteDownOptions mirrors satelliteUpOptions for Down. PostConfirm runs
// after the destroy confirm and before the machine is destroyed — the shared
// verb best-effort-deregisters sync cursors there (C19/C20).
type satelliteDownOptions struct {
	Name        string
	Infra       satelliteInfraConfig
	TFDir       string
	AssumeYes   bool
	PostConfirm func() error
}

// satelliteProvider owns machine creation/destruction for one infra target.
// The shared verbs call PrepareUp (validation only) before anything
// user-visible happens, then Up/Down — which may prompt exactly as the
// pre-seam code did.
type satelliteProvider interface {
	// Kind names the provider/target ("gcp"; later "tart", "docker"). NOT
	// the full/exec satellite kind — that axis is DefaultSatelliteKind's.
	Kind() string
	// DefaultSatelliteKind is the satellite kind an `up` without --kind
	// resolves to (gcp: satelliteKindFull; tart: satelliteKindExec).
	DefaultSatelliteKind() string
	// UsesBootstrapScript reports whether the shared `up` verb runs the
	// embedded bootstrap script against the machine (gcp: true). Providers
	// without it (tart: false) are provisioned client-side instead: the verb
	// implies --prebuilt, restricts the satellite to exec kind, and the
	// provider itself performs the minimal guest prep over ssh.
	UsesBootstrapScript() bool
	// DefaultPrebuiltTarget is the <goos>/<goarch> cross-compile target the
	// shared verb builds the implied-prebuilt stack for when
	// --prebuilt-target is not given: the arch of the machine the provider
	// creates. Only consulted for non-bootstrap-script providers today
	// (tart: the linux/arm64 guest; docker: the daemon's reported Os/Arch,
	// which needs a live daemon query — hence the error); gcp mirrors the
	// flag's linux/amd64 default.
	DefaultPrebuiltTarget() (string, error)
	// PrepareUp validates provider inputs and resolves provider-private
	// state (gcp: the terraform dir + variables.tf sanity check) WITHOUT
	// creating anything or prompting. It runs before the shared bootstrap
	// extraction and the confirm prompt, keeping every validation ahead of
	// machine creation.
	PrepareUp(opts *satelliteUpOptions) error
	// Up prompts the billable confirm (unless opts.AssumeYes), runs
	// opts.PostConfirm, then creates/starts the machine and returns how to
	// reach it.
	Up(ctx context.Context, opts *satelliteUpOptions) (satelliteEndpoint, error)
	// Down prompts the destroy confirm (unless opts.AssumeYes), runs
	// opts.PostConfirm, then destroys the machine.
	Down(ctx context.Context, opts *satelliteDownOptions) error
}

// satelliteProviderRegistry maps infra target names to provider
// constructors. Target validity IS registry membership now; the
// embedded-terraform-target check that used to gate targets
// (resolveSatelliteTarget) lives inside the gcp provider.
var satelliteProviderRegistry = map[string]func(target string) satelliteProvider{
	defaultSatelliteTarget: newGCPSatelliteProvider,
	tartSatelliteTarget:    newTartSatelliteProvider,
	dockerSatelliteTarget:  newDockerSatelliteProvider,
}

// satelliteProviderFor resolves an infra target name (empty = the gcp
// default) to a fresh provider instance. Unknown targets error, listing the
// known providers.
func satelliteProviderFor(target string) (satelliteProvider, error) {
	if target == "" {
		target = defaultSatelliteTarget
	}
	ctor, ok := satelliteProviderRegistry[target]
	if !ok {
		known := make([]string, 0, len(satelliteProviderRegistry))
		for name := range satelliteProviderRegistry {
			known = append(known, name)
		}
		sort.Strings(known)
		return nil, fmt.Errorf("unknown satellite target %q (known providers: %s)", target, strings.Join(known, ", "))
	}
	return ctor(target), nil
}

// satelliteProviderRefTarget extracts the infra target from a state entry's
// provider_ref ("tart:grove-sat-mysat" -> "tart"). Empty when no ref was
// stamped (gcp, whose terraform state owns the mapping) — an unrecognized
// prefix is returned as-is so callers refuse rather than silently falling back
// to the gcp default.
func satelliteProviderRefTarget(providerRef string) string {
	target, _, ok := strings.Cut(providerRef, ":")
	if !ok {
		return ""
	}
	return target
}

// satelliteProviderRefMismatch cross-checks a requested infra target against
// the provider recorded in an existing entry's provider_ref, returning the
// RECORDED target when the two disagree (empty when they agree or nothing is
// recorded). Each provider's "is this machine ours?" check only looks within
// its own provider, so driving the wrong provider at an existing satellite
// orphans the machine it already created — `up` and `down` both refuse on a
// non-empty result.
func satelliteProviderRefMismatch(entry satelliteConfigEntry, target string) string {
	if target == "" {
		target = defaultSatelliteTarget
	}
	ref := satelliteProviderRefTarget(entry.ProviderRef)
	if ref == "" || ref == target {
		return ""
	}
	return ref
}

// resolveSatelliteKind resolves the `up --kind` flag value: empty means the
// provider's default (gcp: full); anything but full/exec is rejected.
func resolveSatelliteKind(flagValue string, p satelliteProvider) (string, error) {
	switch flagValue {
	case "":
		return p.DefaultSatelliteKind(), nil
	case satelliteKindFull, satelliteKindExec:
		return flagValue, nil
	default:
		return "", fmt.Errorf("--kind must be %q or %q (got %q)", satelliteKindFull, satelliteKindExec, flagValue)
	}
}

// satelliteEndpointHost extracts the bare host from an endpoint's SSHAddr —
// the value the shared verb keyscans, bootstraps, and prints.
func satelliteEndpointHost(ep satelliteEndpoint) (string, error) {
	host, _, err := net.SplitHostPort(ep.SSHAddr)
	if err != nil {
		return "", fmt.Errorf("provider endpoint address %q is not host:port: %w", ep.SSHAddr, err)
	}
	return host, nil
}
