package cmd

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/grovetools/grove/pkg/satellitecontract"
)

const (
	tartHostImageAllowance  = uint64(4 << 30)
	tartHostGrowthAllowance = uint64(20 << 30)
	tartHostReserve         = uint64(10 << 30)
	tartGuestHydrationFloor = uint64(4 << 30)
	tartGuestReserve        = uint64(5 << 30)
)

type satelliteCapacityPlan struct {
	Host  satellitecontract.CapacityBudget
	Guest satellitecontract.CapacityBudget
}

// calculateFullTartCapacityPlan accounts for the payload Grove can observe
// locally and explicit growth/reserve allowances. Host and guest are checked
// independently; neither observation is reused for the other filesystem.
func calculateFullTartCapacityPlan(sourceRoot string) (satelliteCapacityPlan, error) {
	payload, err := regularFileBytes(sourceRoot)
	if err != nil {
		return satelliteCapacityPlan{}, fmt.Errorf("calculate full Tart payload size: %w", err)
	}
	return satelliteCapacityPlan{
		Host: satellitecontract.CapacityBudget{
			PayloadBytes: payload + tartHostImageAllowance,
			GrowthBytes:  tartHostGrowthAllowance,
			ReserveBytes: tartHostReserve,
		},
		Guest: satellitecontract.CapacityBudget{
			PayloadBytes: payload + tartGuestHydrationFloor,
			GrowthBytes:  payload,
			ReserveBytes: tartGuestReserve,
		},
	}, nil
}

func regularFileBytes(root string) (uint64, error) {
	var total uint64
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Build outputs and dependency caches are not shipped or hydrated.
		if d.IsDir() && path != root {
			switch d.Name() {
			case "node_modules", "bin", "dist", "zig-out", ".cache":
				return filepath.SkipDir
			}
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > 0 {
			total += uint64(info.Size())
		}
		return nil
	})
	return total, err
}

// validateFullTartGuestCapacity runs after pinned SSH auth and before any
// prebuilt payload, bootstrap, repository mirror, or notebook hydration.
func validateFullTartGuestCapacity(ssh *satelliteSSH, budget satellitecontract.CapacityBudget) error {
	out, err := ssh.outputCommand("df -Pk / | awk 'NR==2 {print $4}'", "")
	if err != nil {
		return fmt.Errorf("full Tart guest capacity preflight: %w", err)
	}
	blocks, err := strconv.ParseUint(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return fmt.Errorf("full Tart guest capacity preflight returned %q: %w", strings.TrimSpace(out), err)
	}
	if blocks > ^uint64(0)/1024 {
		return fmt.Errorf("full Tart guest capacity preflight overflow")
	}
	if err := satellitecontract.ValidateGuestCapacity(blocks*1024, budget); err != nil {
		return fmt.Errorf("full Tart guest capacity preflight: %w", err)
	}
	return nil
}
