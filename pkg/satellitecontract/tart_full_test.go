package satellitecontract

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func boolPtr(v bool) *bool { return &v }

func validCapability() CapabilityStatus {
	return CapabilityStatus{
		SchemaVersion: SchemaVersion,
		Runtime: RuntimeStatus{
			Name: "pi", Version: "0.80.10", Platform: "linux/arm64",
			PackageVersion: "0.1.0", PackageSHA256: strings.Repeat("a", 64),
		},
		Auth: AuthStatus{Present: true, Type: "oauth", Usable: boolPtr(true)},
		Policy: PolicyStatus{
			Trust: HealthReady, Policy: HealthReady, Guard: HealthReady,
			IsolationBoundary: "tart-vm",
		},
		RecordReturn:  RecordReturnStatus{Generation: "generation-1", Cleanliness: ReturnClean},
		ArtifactFetch: ArtifactFetchStatus{Supported: true, MaxFiles: 32, MaxBytes: 1 << 20},
	}
}

func TestCapabilityStatusContainsMetadataNotCredentials(t *testing.T) {
	status := validCapability()
	if err := status.Validate(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"access_token", "refresh_token", "api_key", "credential"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("capability JSON contains secret-bearing field %q: %s", forbidden, encoded)
		}
	}

	status.Auth = AuthStatus{Present: false, Type: "oauth", Usable: boolPtr(false)}
	if err := status.Validate(); err == nil {
		t.Fatal("absent auth claiming type/usability must fail")
	}
	status = validCapability()
	status.Auth.Type = "token"
	if err := status.Validate(); err == nil {
		t.Fatal("unknown auth type must fail")
	}
	status = validCapability()
	status.Policy.IsolationBoundary = "guard"
	if err := status.Validate(); err == nil {
		t.Fatal("guard must never be identified as the isolation boundary")
	}
	status = validCapability()
	status.ArtifactFetch = ArtifactFetchStatus{Supported: false, MaxFiles: 1}
	if err := status.Validate(); err == nil {
		t.Fatal("unsupported artifact fetch cannot advertise limits")
	}
}

func TestMaintenanceStateMachine(t *testing.T) {
	allowed := [][2]MaintenanceState{
		{MaintenanceActive, MaintenanceDraining},
		{MaintenanceDraining, MaintenanceQuiesced},
		{MaintenanceQuiesced, MaintenanceEscrowed},
		{MaintenanceQuiesced, MaintenanceDeleting},
		{MaintenanceEscrowed, MaintenanceDeleting},
	}
	for _, edge := range allowed {
		if !CanTransition(edge[0], edge[1]) {
			t.Errorf("expected transition %s -> %s", edge[0], edge[1])
		}
	}
	for _, edge := range [][2]MaintenanceState{
		{MaintenanceActive, MaintenanceDeleting},
		{MaintenanceDraining, MaintenanceDeleting},
		{MaintenanceDeleting, MaintenanceActive},
	} {
		if CanTransition(edge[0], edge[1]) {
			t.Errorf("unsafe transition %s -> %s", edge[0], edge[1])
		}
	}
}

func TestDeletionGateIsGenerationBoundAndFailClosed(t *testing.T) {
	clean := RecordReturnStatus{Generation: "g1", Cleanliness: ReturnClean}
	if err := DeletionAllowed(clean, clean, false); err != nil {
		t.Fatalf("clean generation: %v", err)
	}

	dirty := RecordReturnStatus{Generation: "g1", Cleanliness: ReturnDirty}
	if err := DeletionAllowed(dirty, dirty, false); err == nil || !strings.Contains(err.Error(), "exit 20") {
		t.Fatalf("dirty return should use exit %d: %v", ExitReturnDirty, err)
	}
	if err := DeletionAllowed(dirty, dirty, true); err != nil {
		t.Fatalf("explicit destructive override should permit known dirty state: %v", err)
	}

	unknown := RecordReturnStatus{Generation: "g1", Cleanliness: ReturnUnknown}
	if err := DeletionAllowed(unknown, unknown, true); err == nil || !strings.Contains(err.Error(), "exit 21") {
		t.Fatalf("override must not permit unknown state: %v", err)
	}
	changed := RecordReturnStatus{Generation: "g2", Cleanliness: ReturnClean}
	if err := DeletionAllowed(clean, changed, true); err == nil || !strings.Contains(err.Error(), "exit 21") {
		t.Fatalf("TOCTOU generation change must fail: %v", err)
	}

	escrow := RecordReturnStatus{
		Generation: "g1", Cleanliness: ReturnDirty, EscrowOperationID: "op-1",
		EscrowSHA256: strings.Repeat("b", 64),
	}
	if err := DeletionAllowed(escrow, escrow, false); err == nil {
		t.Fatal("unverified escrow must not permit deletion")
	}
	escrow.EscrowVerified = true
	if err := DeletionAllowed(escrow, escrow, false); err != nil {
		t.Fatalf("verified escrow should permit deletion: %v", err)
	}
}

func TestPackageActivationIsContentAddressedAndRollbackSafe(t *testing.T) {
	activation := PackageActivation{
		SchemaVersion: SchemaVersion,
		PackageName:   "@grovetools/grove-pi", PackageVersion: "0.1.0",
		SHA256: strings.Repeat("c", 64), StoreRoot: "/opt/grove/pi-packages",
		SettingsPath: "/home/admin/.pi/agent/settings.json",
	}
	packagePath, err := activation.PackagePath()
	if err != nil {
		t.Fatal(err)
	}
	want := "/opt/grove/pi-packages/sha256/" + strings.Repeat("c", 64) + "/package"
	if packagePath != want {
		t.Fatalf("package path = %q, want %q", packagePath, want)
	}
	staged, err := activation.StagedSettingsPath("op-42")
	if err != nil || staged != activation.SettingsPath+".staged-op-42" {
		t.Fatalf("staged settings = %q, %v", staged, err)
	}
	rollback, err := activation.RollbackSettingsPath()
	if err != nil || rollback != activation.SettingsPath+".previous" {
		t.Fatalf("rollback settings = %q, %v", rollback, err)
	}
	activation.StoreRoot = "relative"
	if _, err := activation.PackagePath(); err == nil {
		t.Fatal("relative package store must fail")
	}
}

func TestTartVolumePreflightFailsBeforeCreation(t *testing.T) {
	budget := CapacityBudget{PayloadBytes: 100, GrowthBytes: 50, ReserveBytes: 25}
	valid := VolumeFacts{
		Mounted: true, MountPoint: ExpectedTartMount, MountIdentity: "volume-uuid",
		TartHome: ExpectedTartHome, Writable: true, AvailableBytes: 175,
	}
	tests := map[string]VolumeFacts{
		"absent":    {TartHome: ExpectedTartHome},
		"wrong":     func() VolumeFacts { f := valid; f.MountIdentity = "other"; return f }(),
		"read-only": func() VolumeFacts { f := valid; f.Writable = false; return f }(),
		"low-space": func() VolumeFacts { f := valid; f.AvailableBytes = 174; return f }(),
		"fallback":  func() VolumeFacts { f := valid; f.TartHome = "/tmp/tart"; return f }(),
	}
	for name, facts := range tests {
		t.Run(name, func(t *testing.T) {
			created := false
			if err := ValidateTartVolume(facts, "volume-uuid", budget); err == nil {
				created = true // represents the provider clone call in Phase 1.
			}
			if created {
				t.Fatal("invalid volume reached VM creation boundary")
			}
		})
	}
	if err := ValidateTartVolume(valid, "volume-uuid", budget); err != nil {
		t.Fatalf("valid preflight: %v", err)
	}
}

func TestCapacityAccountingIsIndependentAndOverflowSafe(t *testing.T) {
	plan := CapacityPlan{
		Host:  CapacityBudget{PayloadBytes: 10, GrowthBytes: 20, ReserveBytes: 30},
		Guest: CapacityBudget{PayloadBytes: 40, GrowthBytes: 50, ReserveBytes: 60},
	}
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := ValidateGuestCapacity(149, plan.Guest); err == nil {
		t.Fatal("low guest space must fail independently of host space")
	}
	if _, err := (CapacityBudget{PayloadBytes: math.MaxUint64, GrowthBytes: 1}).RequiredBytes(); err == nil {
		t.Fatal("overflow must fail closed")
	}
}

func TestDestructiveWarningIsExactAndSatelliteSpecific(t *testing.T) {
	const want = "WARNING: this permanently deletes unreturned notebook changes and guest-local Pi credentials; it does not revoke upstream OAuth credentials or guarantee secure erasure."
	if DestructiveOverrideWarning != want {
		t.Fatalf("warning changed: %q", DestructiveOverrideWarning)
	}
	if got := DestructiveOverrideConfirmation("lab"); got != `discard unreturned record changes for satellite "lab"` {
		t.Fatalf("confirmation = %q", got)
	}
}
