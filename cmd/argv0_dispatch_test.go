package cmd

import (
	"path/filepath"
	"testing"
)

// fakeRegistry stands in for the sdk tool registry so the dispatch decision can
// be tested without touching a state dir or a filesystem.
func fakeRegistry(name string) (string, bool) {
	switch name {
	case "cx", "nb", "treemux":
		return name, true
	case "daemon", "groved":
		return "groved", true
	}
	return "", false
}

func TestArgv0Tool(t *testing.T) {
	exposures := map[string]string{
		"gnb":     "nb",      // `grove expose nb --as gnb`
		"stale":   "retired", // a tool that has left the registry
		"empty":   "",        // a malformed ledger entry
		"cockpit": "treemux", // custom name for a registered tool
	}

	tests := []struct {
		name     string
		argv0    string
		wantTool string
		wantOK   bool
	}{
		{"grove itself never dispatches", "grove", "", false},
		{"grove by absolute path never dispatches", "/usr/local/bin/grove", "", false},
		{"grove by relative path never dispatches", "./grove", "", false},
		{"grove.exe never dispatches", "grove.exe", "", false},
		{"a registered alias dispatches to itself", "cx", "cx", true},
		{"an exposure link found by path still dispatches", "/home/u/.local/bin/cx", "cx", true},
		{"a repo name dispatches to its binary alias", "daemon", "groved", true},
		{"a custom name dispatches via the ledger", "gnb", "nb", true},
		{"a custom name for a registered tool", "cockpit", "treemux", true},
		{"a stale ledger entry does not dispatch", "stale", "", false},
		{"a malformed ledger entry does not dispatch", "empty", "", false},
		{"an unregistered name falls through to grove", "ls", "", false},
		{"a test binary name falls through to grove", "cmd.test", "", false},
		{"an empty argv0 falls through", "", "", false},
		{"a bare separator falls through", "/", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool, ok := argv0Tool(tt.argv0, exposures, fakeRegistry)
			if ok != tt.wantOK || tool != tt.wantTool {
				t.Errorf("argv0Tool(%q) = (%q, %v), want (%q, %v)", tt.argv0, tool, ok, tt.wantTool, tt.wantOK)
			}
		})
	}
}

// An empty ledger is the common case (nothing exposed under a custom name):
// registered names must still dispatch on their own.
func TestArgv0ToolWithNoLedger(t *testing.T) {
	if tool, ok := argv0Tool("cx", nil, fakeRegistry); !ok || tool != "cx" {
		t.Errorf("argv0Tool(cx, nil) = (%q, %v), want (cx, true)", tool, ok)
	}
	if _, ok := argv0Tool("gnb", nil, fakeRegistry); ok {
		t.Error("a custom name dispatched with no ledger to map it")
	}
}

// registryBinary is the real registry lookup argv0Delegation uses; pin the
// alias translation that the raw registry map does not do for you.
func TestRegistryBinaryTranslatesAliases(t *testing.T) {
	exposeSandbox(t)

	cases := map[string]string{
		"daemon":  "groved",
		"groved":  "groved",
		"nb":      "nb",
		"treemux": "treemux",
		"sync":    "grove-syncd",
	}
	for in, want := range cases {
		got, ok := registryBinary(in)
		if !ok || got != want {
			t.Errorf("registryBinary(%q) = (%q, %v), want (%q, true)", in, got, ok, want)
		}
	}
	if _, ok := registryBinary("definitely-not-a-grove-tool"); ok {
		t.Error("registryBinary accepted an unregistered name")
	}
}

// End-to-end over the real ledger + real registry: expose under a custom name,
// then ask what an invocation arriving under that name should run.
func TestArgv0DelegationReadsRealLedger(t *testing.T) {
	dir, _, _ := exposeSandbox(t)

	if err := runExpose("nb", "gnb", false); err != nil {
		t.Fatalf("expose nb --as gnb: %v", err)
	}
	if tool, ok := argv0Delegation(filepath.Join(dir, "gnb")); !ok || tool != "nb" {
		t.Errorf("argv0Delegation(gnb) = (%q, %v), want (nb, true)", tool, ok)
	}
	if _, ok := argv0Delegation(filepath.Join(dir, "grove")); ok {
		t.Error("grove dispatched to itself")
	}
	if _, ok := argv0Delegation("/usr/bin/ls"); ok {
		t.Error("an unregistered name dispatched")
	}
}
