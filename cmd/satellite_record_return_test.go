package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fakeRecordTransport struct {
	maintenance    []recordMaintenanceStatus
	maintenanceErr map[int]error
	incoming       []recordIncomingStatus
	incomingErr    map[int]error
	mc, ic         int
}

func (f *fakeRecordTransport) Maintenance(_ context.Context, _, _ string) (recordMaintenanceStatus, error) {
	i := f.mc
	f.mc++
	if err := f.maintenanceErr[i]; err != nil {
		return recordMaintenanceStatus{}, err
	}
	if i >= len(f.maintenance) {
		return recordMaintenanceStatus{Draining: true}, nil
	}
	return f.maintenance[i], nil
}
func (f *fakeRecordTransport) Incoming(_ context.Context, _ string, _ []string) (recordIncomingStatus, error) {
	i := f.ic
	f.ic++
	if err := f.incomingErr[i]; err != nil {
		return recordIncomingStatus{}, err
	}
	return f.incoming[i], nil
}
func incoming(g string, clean, escrow bool) recordIncomingStatus {
	var s recordIncomingStatus
	s.Manifest.Generation = g
	s.Clean = clean
	s.EscrowVerified = escrow
	if !clean {
		s.Manifest.Operations = []json.RawMessage{json.RawMessage(`{"type":"create"}`)}
	}
	return s
}

func TestRecordSafeDownCleanAndGenerationBound(t *testing.T) {
	f := &fakeRecordTransport{maintenance: []recordMaintenanceStatus{{Draining: true}, {Draining: true}, {Draining: true}}, maintenanceErr: map[int]error{}, incoming: []recordIncomingStatus{incoming(strings.Repeat("a", 64), true, false), incoming(strings.Repeat("a", 64), true, false)}, incomingErr: map[int]error{}}
	check, cleanup, err := prepareRecordSafeDown(context.Background(), f, "sat", []string{"ws"}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err = check(); err != nil {
		t.Fatal(err)
	}
}
func TestRecordSafeDownRefusesGuestOnlyNoteWithoutEscrow(t *testing.T) {
	f := &fakeRecordTransport{maintenance: []recordMaintenanceStatus{{Draining: true}, {Draining: true}}, maintenanceErr: map[int]error{}, incoming: []recordIncomingStatus{incoming(strings.Repeat("b", 64), false, false)}, incomingErr: map[int]error{}}
	_, _, err := prepareRecordSafeDown(context.Background(), f, "sat", []string{"ws"}, false)
	if err == nil || !strings.Contains(err.Error(), "unreturned record changes") {
		t.Fatalf("got %v", err)
	}
}
func TestRecordSafeDownAllowsVerifiedEscrow(t *testing.T) {
	g := strings.Repeat("c", 64)
	f := &fakeRecordTransport{maintenance: []recordMaintenanceStatus{{Draining: true}, {Draining: true}, {Draining: true}}, maintenanceErr: map[int]error{}, incoming: []recordIncomingStatus{incoming(g, false, true), incoming(g, false, true)}, incomingErr: map[int]error{}}
	check, cleanup, err := prepareRecordSafeDown(context.Background(), f, "sat", []string{"ws"}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err = check(); err != nil {
		t.Fatal(err)
	}
}
func TestRecordSafeDownTOCTOUGenerationChangeRefuses(t *testing.T) {
	f := &fakeRecordTransport{maintenance: []recordMaintenanceStatus{{Draining: true}, {Draining: true}, {Draining: true}}, maintenanceErr: map[int]error{}, incoming: []recordIncomingStatus{incoming(strings.Repeat("d", 64), true, false), incoming(strings.Repeat("e", 64), true, false)}, incomingErr: map[int]error{}}
	check, cleanup, err := prepareRecordSafeDown(context.Background(), f, "sat", []string{"ws"}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err = check(); err == nil || !strings.Contains(err.Error(), "generation changed") {
		t.Fatalf("got %v", err)
	}
}
func TestRecordSafeDownDisconnectFailsClosed(t *testing.T) {
	f := &fakeRecordTransport{maintenanceErr: map[int]error{1: errors.New("ssh disconnected")}, incomingErr: map[int]error{}}
	_, _, err := prepareRecordSafeDown(context.Background(), f, "sat", []string{"ws"}, false)
	if err == nil || !strings.Contains(err.Error(), "disconnect/unknown") {
		t.Fatalf("got %v", err)
	}
}
func TestRecordSafeDownActiveJobRefuses(t *testing.T) {
	f := &fakeRecordTransport{maintenanceErr: map[int]error{1: errors.New("1 managed job is still active")}, incomingErr: map[int]error{}}
	_, _, err := prepareRecordSafeDown(context.Background(), f, "sat", []string{"ws"}, false)
	if err == nil || !strings.Contains(err.Error(), "active") {
		t.Fatalf("got %v", err)
	}
}

func TestRecordSafeDownPendingParkedDivergedRefuse(t *testing.T) {
	for _, st := range []recordMaintenanceStatus{{Draining: true, OutboxPending: 1}, {Draining: true, OutboxParked: 1}, {Draining: true, DocumentsDiverged: 1}} {
		f := &fakeRecordTransport{maintenance: []recordMaintenanceStatus{{Draining: true}, st}, maintenanceErr: map[int]error{}, incomingErr: map[int]error{}}
		_, _, err := prepareRecordSafeDown(context.Background(), f, "sat", []string{"ws"}, false)
		if err == nil || !strings.Contains(err.Error(), "not drained") {
			t.Fatalf("status %+v got %v", st, err)
		}
	}
}
func TestRecordSafeDownForceStillRefusesUnknownFinal(t *testing.T) {
	g := strings.Repeat("f", 64)
	f := &fakeRecordTransport{maintenance: []recordMaintenanceStatus{{Draining: true}, {Draining: true}, {Draining: true}}, maintenanceErr: map[int]error{}, incoming: []recordIncomingStatus{incoming(g, false, false)}, incomingErr: map[int]error{1: errors.New("server gone")}}
	check, cleanup, err := prepareRecordSafeDown(context.Background(), f, "sat", []string{"ws"}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err = check(); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("got %v", err)
	}
}
