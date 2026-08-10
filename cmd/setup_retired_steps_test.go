package cmd

import (
	"testing"

	"github.com/grovetools/grove/pkg/setup"
)

func TestSetupWizardRetiresCodeTopologySteps(t *testing.T) {
	m := newSetupModel(setup.NewService(false), nil)
	for _, c := range m.components {
		if c.id == "ecosystem" || c.id == "notebook" {
			t.Fatalf("obsolete setup component still exposed: %s", c.id)
		}
	}
}
