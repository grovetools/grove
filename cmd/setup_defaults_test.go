package cmd

import (
	"strings"
	"testing"

	"github.com/grovetools/core/logging"
	"github.com/grovetools/grove/pkg/setup"
)

func TestRunSetupDefaultsWithoutOnlyRunsRemainingSafeSteps(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", home+"/data")
	t.Setenv("XDG_CONFIG_HOME", home+"/config")

	service := setup.NewService(true)
	if err := runSetupDefaults(service, map[string]bool{}, logging.NewLogger("setup-test"), logging.NewPrettyLogger()); err != nil {
		t.Fatal(err)
	}
	var descriptions []string
	for _, action := range service.Actions() {
		descriptions = append(descriptions, action.Description)
	}
	got := strings.Join(descriptions, "\n")
	for _, want := range []string{"Run grove keys generate tmux", "tmux.conf", "grove.lua"} {
		if !strings.Contains(got, want) {
			t.Errorf("--defaults did not run %q; actions:\n%s", want, got)
		}
	}
}

func TestRunSetupDefaultsOnlyStillFiltersSteps(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	service := setup.NewService(true)
	if err := runSetupDefaults(service, map[string]bool{"gemini": true}, logging.NewLogger("setup-test"), logging.NewPrettyLogger()); err != nil {
		t.Fatal(err)
	}
	if actions := service.Actions(); len(actions) != 0 {
		t.Fatalf("--only gemini ran unrelated default-safe steps: %#v", actions)
	}
}
