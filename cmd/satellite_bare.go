package cmd

import "fmt"

// validateSatelliteBareUp gates `up --bare` before anything is created. Bare
// mode exists so a guest can serve as a clean room (install/onboarding
// testing): the provider creates the machine and layer-0 pins the transport,
// and nothing else touches it. The gates keep that guarantee honest:
//
//   - tart only, for now: gcp provisions THROUGH a bootstrap script (bare
//     would be a VM grove cannot even reach idempotently), and the docker
//     image bakes grove prep into the container image itself.
//   - exec kind only: a full satellite is DEFINED by the stack bare skips.
//   - an explicit --prebuilt is a contradiction, not an override.
//   - an existing non-bare entry is refused: the stack already on (or half on)
//     that guest cannot be unshipped, so re-provisioning it "bare" would lie.
//     The reverse promotion — plain `up` on a bare entry — is allowed and
//     clears the marker.
func validateSatelliteBareUp(providerKind string, execKind bool, explicitPrebuilt bool, existing satelliteConfigEntry, name string) error {
	if providerKind != tartSatelliteTarget {
		return fmt.Errorf("--bare currently supports the %q target only (got %q)", tartSatelliteTarget, providerKind)
	}
	if !execKind {
		return fmt.Errorf("--bare requires an exec-kind satellite: a full satellite is defined by the stack --bare skips")
	}
	if explicitPrebuilt {
		return fmt.Errorf("--prebuilt contradicts --bare: bare provisions no grove stack")
	}
	if existing.ProviderRef != "" && !existing.Bare {
		return fmt.Errorf("satellite %q already exists without --bare — its guest already carries (or may carry) the grove stack, which --bare cannot remove: destroy it first with `grove satellite down %s`, or pick another name", name, name)
	}
	return nil
}
