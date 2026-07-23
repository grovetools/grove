// Package satellitecontract defines versioned wire and lifecycle contracts for
// experimental full Tart satellites. It intentionally performs no provider
// operations; later phases must satisfy these contracts before exposing full
// Tart creation or destructive verbs.
package satellitecontract

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
)

const (
	SchemaVersion = 1

	ExpectedTartMount = "/Volumes/solot7"
	ExpectedTartHome  = "/Volumes/solot7/tart"

	ExitOK                = 0
	ExitUsage             = 64
	ExitReturnDirty       = 20
	ExitReturnUnknown     = 21
	ExitMaintenanceFailed = 22
	ExitPreflightFailed   = 23
)

// DestructiveOverrideWarning is intentionally exact. Callers must print it
// with DestructiveOverrideConfirmation and record the confirmed satellite.
const DestructiveOverrideWarning = "WARNING: this permanently deletes unreturned notebook changes and guest-local Pi credentials; it does not revoke upstream OAuth credentials or guarantee secure erasure."

func DestructiveOverrideConfirmation(satellite string) string {
	return fmt.Sprintf("discard unreturned record changes for satellite %q", satellite)
}

type Health string

const (
	HealthReady       Health = "ready"
	HealthDegraded    Health = "degraded"
	HealthUnavailable Health = "unavailable"
	HealthUnknown     Health = "unknown"
)

func (h Health) valid() bool {
	return h == HealthReady || h == HealthDegraded || h == HealthUnavailable || h == HealthUnknown
}

// AuthStatus deliberately contains metadata only. OAuth token values have no
// field in the capability schema.
type AuthStatus struct {
	Present bool   `json:"present"`
	Type    string `json:"type,omitempty"`
	Usable  *bool  `json:"usable,omitempty"`
}

type RuntimeStatus struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	Platform       string `json:"platform"`
	PackageVersion string `json:"package_version"`
	PackageSHA256  string `json:"package_sha256"`
}

type PolicyStatus struct {
	Trust             Health `json:"trust"`
	Policy            Health `json:"policy"`
	Guard             Health `json:"guard"`
	IsolationBoundary string `json:"isolation_boundary"`
}

type ReturnCleanliness string

const (
	ReturnClean   ReturnCleanliness = "clean"
	ReturnDirty   ReturnCleanliness = "dirty"
	ReturnUnknown ReturnCleanliness = "unknown"
)

type RecordReturnStatus struct {
	Generation        string            `json:"generation"`
	Cleanliness       ReturnCleanliness `json:"cleanliness"`
	EscrowOperationID string            `json:"escrow_operation_id,omitempty"`
	EscrowSHA256      string            `json:"escrow_sha256,omitempty"`
	EscrowVerified    bool              `json:"escrow_verified,omitempty"`
}

type ArtifactFetchStatus struct {
	Supported bool  `json:"supported"`
	MaxFiles  int   `json:"max_files,omitempty"`
	MaxBytes  int64 `json:"max_bytes,omitempty"`
}

type CapabilityStatus struct {
	SchemaVersion int                 `json:"schema_version"`
	Runtime       RuntimeStatus       `json:"runtime"`
	Auth          AuthStatus          `json:"auth"`
	Policy        PolicyStatus        `json:"policy"`
	RecordReturn  RecordReturnStatus  `json:"record_return"`
	ArtifactFetch ArtifactFetchStatus `json:"artifact_fetch"`
}

func (s CapabilityStatus) Validate() error {
	if s.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", s.SchemaVersion)
	}
	if s.Runtime.Name == "" || s.Runtime.Version == "" || s.Runtime.Platform == "" {
		return errors.New("runtime name, version, and platform are required")
	}
	if s.Runtime.PackageVersion == "" || !sha256Pattern.MatchString(s.Runtime.PackageSHA256) {
		return errors.New("runtime package version and valid sha256 are required")
	}
	if s.Auth.Present && s.Auth.Type != "oauth" && s.Auth.Type != "api_key" {
		return errors.New("present auth requires oauth or api_key type metadata")
	}
	if !s.Auth.Present && (s.Auth.Type != "" || s.Auth.Usable != nil) {
		return errors.New("absent auth cannot claim type or usability")
	}
	if !s.Policy.Trust.valid() || !s.Policy.Policy.valid() || !s.Policy.Guard.valid() {
		return errors.New("invalid trust, policy, or guard health")
	}
	if s.Policy.IsolationBoundary != "tart-vm" {
		return errors.New("isolation_boundary must be tart-vm; guard and trust are not sandboxes")
	}
	if err := s.RecordReturn.Validate(); err != nil {
		return err
	}
	if s.ArtifactFetch.Supported && (s.ArtifactFetch.MaxFiles <= 0 || s.ArtifactFetch.MaxBytes <= 0) {
		return errors.New("supported artifact fetch requires positive limits")
	}
	if !s.ArtifactFetch.Supported && (s.ArtifactFetch.MaxFiles != 0 || s.ArtifactFetch.MaxBytes != 0) {
		return errors.New("unsupported artifact fetch cannot advertise limits")
	}
	return nil
}

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (s RecordReturnStatus) Validate() error {
	if s.Generation == "" {
		return errors.New("record-return generation is required")
	}
	if s.Cleanliness != ReturnClean && s.Cleanliness != ReturnDirty && s.Cleanliness != ReturnUnknown {
		return errors.New("invalid record-return cleanliness")
	}
	hasID, hasHash := s.EscrowOperationID != "", s.EscrowSHA256 != ""
	if hasID != hasHash {
		return errors.New("escrow operation id and hash must appear together")
	}
	if hasHash && !sha256Pattern.MatchString(s.EscrowSHA256) {
		return errors.New("invalid escrow sha256")
	}
	if s.EscrowVerified && !hasHash {
		return errors.New("verified escrow requires operation id and hash")
	}
	return nil
}

type MaintenanceState string

const (
	MaintenanceActive   MaintenanceState = "active"
	MaintenanceDraining MaintenanceState = "draining"
	MaintenanceQuiesced MaintenanceState = "quiesced"
	MaintenanceEscrowed MaintenanceState = "escrowed"
	MaintenanceDeleting MaintenanceState = "deleting"
	MaintenanceFailed   MaintenanceState = "failed"
)

var transitions = map[MaintenanceState]map[MaintenanceState]bool{
	MaintenanceActive:   {MaintenanceDraining: true},
	MaintenanceDraining: {MaintenanceQuiesced: true, MaintenanceFailed: true},
	MaintenanceQuiesced: {MaintenanceEscrowed: true, MaintenanceDeleting: true, MaintenanceFailed: true},
	MaintenanceEscrowed: {MaintenanceDeleting: true, MaintenanceFailed: true},
	MaintenanceFailed:   {MaintenanceActive: true, MaintenanceDraining: true},
}

func CanTransition(from, to MaintenanceState) bool { return transitions[from][to] }

// DeletionAllowed is the final fail-closed gate. It rejects generation changes,
// unknown state, and unverified escrow. The caller must obtain final immediately
// before provider deletion; a destructive override never converts unknown into
// clean.
func DeletionAllowed(initial, final RecordReturnStatus, destructiveOverride bool) error {
	if err := initial.Validate(); err != nil {
		return fmt.Errorf("invalid initial return status: %w", err)
	}
	if err := final.Validate(); err != nil {
		return fmt.Errorf("invalid final return status: %w", err)
	}
	if initial.Generation != final.Generation {
		return fmt.Errorf("record generation changed (exit %d)", ExitReturnUnknown)
	}
	if final.Cleanliness == ReturnUnknown {
		return fmt.Errorf("record return is unknown (exit %d)", ExitReturnUnknown)
	}
	if final.Cleanliness == ReturnClean || final.EscrowVerified || destructiveOverride {
		return nil
	}
	return fmt.Errorf("unreturned record changes block deletion (exit %d)", ExitReturnDirty)
}

// PackageActivation defines the absolute content-addressed install location and
// the same-directory settings files used for atomic rename and rollback.
type PackageActivation struct {
	SchemaVersion  int    `json:"schema_version"`
	PackageName    string `json:"package_name"`
	PackageVersion string `json:"package_version"`
	SHA256         string `json:"sha256"`
	StoreRoot      string `json:"store_root"`
	SettingsPath   string `json:"settings_path"`
}

func (p PackageActivation) Validate() error {
	if p.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", p.SchemaVersion)
	}
	if p.PackageName == "" || p.PackageVersion == "" {
		return errors.New("package name and version are required")
	}
	if !sha256Pattern.MatchString(p.SHA256) {
		return errors.New("package sha256 must be 64 lowercase hex characters")
	}
	if !filepath.IsAbs(p.StoreRoot) || !filepath.IsAbs(p.SettingsPath) {
		return errors.New("package store root and settings path must be absolute")
	}
	return nil
}

func (p PackageActivation) PackagePath() (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	return filepath.Join(p.StoreRoot, "sha256", p.SHA256, "package"), nil
}

func (p PackageActivation) StagedSettingsPath(operationID string) (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	if operationID == "" || filepath.Base(operationID) != operationID {
		return "", errors.New("invalid operation id")
	}
	return p.SettingsPath + ".staged-" + operationID, nil
}

func (p PackageActivation) RollbackSettingsPath() (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	return p.SettingsPath + ".previous", nil
}

// CapacityBudget is used independently for host-backed VM growth and for guest
// hydration. Callers must not use a single free-space observation for both.
type CapacityBudget struct {
	PayloadBytes uint64 `json:"payload_bytes"`
	GrowthBytes  uint64 `json:"growth_bytes"`
	ReserveBytes uint64 `json:"reserve_bytes"`
}

func (b CapacityBudget) RequiredBytes() (uint64, error) {
	parts := []uint64{b.PayloadBytes, b.GrowthBytes, b.ReserveBytes}
	var total uint64
	for _, part := range parts {
		if math.MaxUint64-total < part {
			return 0, errors.New("capacity budget overflow")
		}
		total += part
	}
	if total == 0 {
		return 0, errors.New("capacity budget must be calculated, not zero")
	}
	return total, nil
}

type CapacityPlan struct {
	Host  CapacityBudget `json:"host"`
	Guest CapacityBudget `json:"guest"`
}

func (p CapacityPlan) Validate() error {
	if _, err := p.Host.RequiredBytes(); err != nil {
		return fmt.Errorf("invalid host budget: %w", err)
	}
	if _, err := p.Guest.RequiredBytes(); err != nil {
		return fmt.Errorf("invalid guest budget: %w", err)
	}
	return nil
}

type VolumeFacts struct {
	Mounted        bool   `json:"mounted"`
	MountPoint     string `json:"mount_point"`
	MountIdentity  string `json:"mount_identity"`
	TartHome       string `json:"tart_home"`
	Writable       bool   `json:"writable"`
	AvailableBytes uint64 `json:"available_bytes"`
}

// ValidateTartVolume is the fail-before-clone host contract. expectedIdentity
// comes from persisted operator configuration (volume/device UUID), never from
// the mere existence of /Volumes/solot7 or a directory on the internal disk.
func ValidateTartVolume(f VolumeFacts, expectedIdentity string, hostBudget CapacityBudget) error {
	if !f.Mounted || filepath.Clean(f.MountPoint) != ExpectedTartMount {
		return fmt.Errorf("expected mounted volume at %s", ExpectedTartMount)
	}
	if expectedIdentity == "" || f.MountIdentity != expectedIdentity {
		return errors.New("Tart volume identity does not match the configured volume")
	}
	if filepath.Clean(f.TartHome) != ExpectedTartHome {
		return fmt.Errorf("TART_HOME must be %s (no fallback)", ExpectedTartHome)
	}
	if !f.Writable {
		return errors.New("Tart volume is read-only")
	}
	required, err := hostBudget.RequiredBytes()
	if err != nil {
		return err
	}
	if f.AvailableBytes < required {
		return fmt.Errorf("insufficient Tart volume headroom: available=%d required=%d", f.AvailableBytes, required)
	}
	return nil
}

func ValidateGuestCapacity(availableBytes uint64, guestBudget CapacityBudget) error {
	required, err := guestBudget.RequiredBytes()
	if err != nil {
		return err
	}
	if availableBytes < required {
		return fmt.Errorf("insufficient guest headroom: available=%d required=%d", availableBytes, required)
	}
	return nil
}
