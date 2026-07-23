package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/grovetools/grove/pkg/satellitecontract"
)

var inspectTartHostVolume = inspectTartHostVolumeReal

func inspectTartHostVolumeReal() (satellitecontract.VolumeFacts, error) {
	facts := satellitecontract.VolumeFacts{
		MountPoint: satellitecontract.ExpectedTartMount,
		TartHome:   satellitecontract.ExpectedTartHome,
	}
	mountOut, err := exec.Command("mount").Output()
	if err != nil {
		return facts, fmt.Errorf("inspect mounted volumes: %w", err)
	}
	needle := " on " + satellitecontract.ExpectedTartMount + " ("
	for _, line := range strings.Split(string(mountOut), "\n") {
		if strings.Contains(line, needle) {
			facts.Mounted = true
			break
		}
	}
	if !facts.Mounted {
		return facts, nil
	}

	infoOut, err := exec.Command("diskutil", "info", satellitecontract.ExpectedTartMount).Output()
	if err != nil {
		return facts, fmt.Errorf("read Tart volume identity: %w", err)
	}
	for _, line := range strings.Split(string(infoOut), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Volume UUID":
			facts.MountIdentity = strings.TrimSpace(value)
		case "Device Identifier":
			if facts.MountIdentity == "" {
				facts.MountIdentity = strings.TrimSpace(value)
			}
		}
	}
	if facts.MountIdentity == "" {
		return facts, fmt.Errorf("diskutil did not report a volume UUID or device identifier for %s", satellitecontract.ExpectedTartMount)
	}

	dfOut, err := exec.Command("df", "-Pk", satellitecontract.ExpectedTartMount).Output()
	if err != nil {
		return facts, fmt.Errorf("read Tart volume capacity: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(dfOut)), "\n")
	if len(lines) < 2 {
		return facts, fmt.Errorf("unexpected df output for %s", satellitecontract.ExpectedTartMount)
	}
	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) < 4 {
		return facts, fmt.Errorf("unexpected df row for %s", satellitecontract.ExpectedTartMount)
	}
	blocks, err := strconv.ParseUint(fields[3], 10, 64)
	if err != nil {
		return facts, fmt.Errorf("parse available blocks for Tart volume: %w", err)
	}
	facts.AvailableBytes = blocks * 1024

	if err := os.MkdirAll(satellitecontract.ExpectedTartHome, 0o755); err != nil {
		return facts, fmt.Errorf("create required TART_HOME: %w", err)
	}
	probe, err := os.CreateTemp(satellitecontract.ExpectedTartHome, ".grove-write-probe-")
	if err == nil {
		facts.Writable = true
		name := probe.Name()
		_ = probe.Close()
		_ = os.Remove(name)
	}
	// Resolve symlinks: a symlinked Tart home could escape the mounted volume.
	resolved, err := filepath.EvalSymlinks(satellitecontract.ExpectedTartHome)
	if err != nil {
		return facts, fmt.Errorf("resolve required TART_HOME: %w", err)
	}
	if filepath.Clean(resolved) != satellitecontract.ExpectedTartHome {
		facts.TartHome = resolved
	}
	return facts, nil
}
