package repository

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateGoWork(t *testing.T) {
	tests := []struct {
		name         string
		initialWork  string
		repoName     string
		expectedWork string
		expectError  bool
	}{
		{
			name: "add to existing use block",
			initialWork: `go 1.24.4

use (
	./grove-core
	./grove-meta
)
`,
			repoName: "grove-new",
			expectedWork: `go 1.24.4

use (
	./grove-core
	./grove-meta
	./grove-new
)
`,
		},
		{
			name: "add to empty use block",
			initialWork: `go 1.24.4

use (
)
`,
			repoName: "grove-new",
			expectedWork: `go 1.24.4

use (
	./grove-new
)
`,
		},
		{
			name: "create use block when missing",
			initialWork: `go 1.24.4
`,
			repoName: "grove-new",
			expectedWork: `go 1.24.4

use (
	./grove-new
)
`,
		},
		{
			name: "idempotent - module already exists",
			initialWork: `go 1.24.4

use (
	./grove-core
	./grove-new
)
`,
			repoName:     "grove-new",
			expectedWork: "", // Should remain unchanged
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory
			tmpDir := t.TempDir()
			oldDir, _ := os.Getwd()
			defer os.Chdir(oldDir)
			os.Chdir(tmpDir)

			// Write initial go.work file
			workPath := filepath.Join(tmpDir, "go.work")
			if err := os.WriteFile(workPath, []byte(tt.initialWork), 0644); err != nil {
				t.Fatalf("Failed to write initial go.work: %v", err)
			}

			// Run updateGoWork
			err := updateGoWork(tt.repoName)
			if (err != nil) != tt.expectError {
				t.Fatalf("updateGoWork() error = %v, expectError %v", err, tt.expectError)
			}

			// Read result
			result, err := os.ReadFile(workPath)
			if err != nil {
				t.Fatalf("Failed to read result: %v", err)
			}

			// For idempotent test, expect no change
			if tt.expectedWork == "" {
				if string(result) != tt.initialWork {
					t.Errorf("Expected no change, but file was modified.\nGot:\n%s", result)
				}
				return
			}

			// Normalize whitespace for comparison
			got := strings.TrimSpace(string(result))
			want := strings.TrimSpace(tt.expectedWork)

			if got != want {
				t.Errorf("updateGoWork() result mismatch.\nGot:\n%s\n\nWant:\n%s", got, want)
			}
		})
	}
}

func TestExtractMakefileList(t *testing.T) {
	tests := []struct {
		name     string
		lines    []string
		startIdx int
		expected []string
	}{
		{
			name: "single line list",
			lines: []string{
				"PACKAGES = grove-core grove-meta grove-tend",
				"",
			},
			startIdx: 0,
			expected: []string{"grove-core", "grove-meta", "grove-tend"},
		},
		{
			name: "multi-line list with backslashes",
			lines: []string{
				"PACKAGES = grove-core \\",
				"    grove-meta \\",
				"    grove-tend",
				"",
			},
			startIdx: 0,
			expected: []string{"grove-core", "grove-meta", "grove-tend"},
		},
		{
			name: "list with duplicates",
			lines: []string{
				"PACKAGES = grove-core grove-meta grove-core",
				"",
			},
			startIdx: 0,
			expected: []string{"grove-core", "grove-meta"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractMakefileList(tt.lines, tt.startIdx)

			if len(result) != len(tt.expected) {
				t.Errorf("extractMakefileList() length = %d, want %d", len(result), len(tt.expected))
				return
			}

			for i, item := range tt.expected {
				if result[i] != item {
					t.Errorf("extractMakefileList()[%d] = %s, want %s", i, result[i], item)
				}
			}
		})
	}
}

func TestUpdateRootMakefile(t *testing.T) {
	tests := []struct {
		name           string
		initialMake    string
		repoName       string
		binaryAlias    string
		expectedResult string
		expectError    bool
	}{
		{
			name: "add new package and binary",
			initialMake: `# Root Makefile

PACKAGES = grove-core grove-meta
# GROVE-META:ADD-REPO:PACKAGES - Do not remove this comment

BINARIES = grove gm
# GROVE-META:ADD-REPO:BINARIES - Do not remove this comment

build:
	echo "Building"
`,
			repoName:    "grove-new",
			binaryAlias: "gn",
			expectedResult: `# Root Makefile

PACKAGES = grove-core grove-meta grove-new
# GROVE-META:ADD-REPO:PACKAGES - Do not remove this comment

BINARIES = gm gn grove
# GROVE-META:ADD-REPO:BINARIES - Do not remove this comment

build:
	echo "Building"
`,
		},
		{
			name: "idempotent - package already exists",
			initialMake: `PACKAGES = grove-core grove-new
# GROVE-META:ADD-REPO:PACKAGES - Do not remove this comment

BINARIES = grove gn
# GROVE-META:ADD-REPO:BINARIES - Do not remove this comment
`,
			repoName:    "grove-new",
			binaryAlias: "gn",
			expectedResult: `PACKAGES = grove-core grove-new
# GROVE-META:ADD-REPO:PACKAGES - Do not remove this comment

BINARIES = grove gn
# GROVE-META:ADD-REPO:BINARIES - Do not remove this comment
`,
		},
		{
			name: "missing package hook",
			initialMake: `PACKAGES = grove-core grove-meta

BINARIES = grove gm
# GROVE-META:ADD-REPO:BINARIES - Do not remove this comment
`,
			repoName:    "grove-new",
			binaryAlias: "gn",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory
			tmpDir := t.TempDir()
			oldDir, _ := os.Getwd()
			defer os.Chdir(oldDir)
			os.Chdir(tmpDir)

			// Write initial Makefile
			makePath := filepath.Join(tmpDir, "Makefile")
			if err := os.WriteFile(makePath, []byte(tt.initialMake), 0644); err != nil {
				t.Fatalf("Failed to write initial Makefile: %v", err)
			}

			// Run updateRootMakefile
			err := updateRootMakefile(tt.repoName, tt.binaryAlias)
			if (err != nil) != tt.expectError {
				t.Fatalf("updateRootMakefile() error = %v, expectError %v", err, tt.expectError)
			}

			if tt.expectError {
				return
			}

			// Read result
			result, err := os.ReadFile(makePath)
			if err != nil {
				t.Fatalf("Failed to read result: %v", err)
			}

			got := string(result)
			if got != tt.expectedResult {
				t.Errorf("updateRootMakefile() result mismatch.\nGot:\n%s\n\nWant:\n%s", got, tt.expectedResult)
			}
		})
	}
}

func TestIsValidRepoName(t *testing.T) {
	tests := []struct {
		name     string
		repoName string
		expected bool
	}{
		{"valid simple name", "grove-test", true},
		{"valid multi-part name", "grove-test-tool", true},
		{"valid with numbers", "grove-test123", true},
		{"missing grove prefix", "test-tool", false},
		{"uppercase letters", "grove-Test", false},
		{"special characters", "grove-test!", false},
		{"underscore", "grove_test", false},
		{"empty after grove", "grove-", false},
		{"just grove", "grove", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidRepoName(tt.repoName)
			if result != tt.expected {
				t.Errorf("isValidRepoName(%s) = %v, want %v", tt.repoName, result, tt.expected)
			}
		})
	}
}

func TestDeriveAliasFromRepoName(t *testing.T) {
	tests := []struct {
		name     string
		repoName string
		expected string
	}{
		{"simple name", "grove-test", "t"},
		{"multi-part name", "grove-context", "c"},
		{"three parts", "grove-test-tool", "tt"},
		{"four parts", "grove-test-tool-suite", "tts"},
		{"empty part", "grove--tool", "t"},
		{"no parts after grove", "grove", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := deriveAliasFromRepoName(tt.repoName)
			if result != tt.expected {
				t.Errorf("deriveAliasFromRepoName(%s) = %s, want %s", tt.repoName, result, tt.expected)
			}
		})
	}
}

func TestCheckBinaryAliasConflict(t *testing.T) {
	tests := []struct {
		name        string
		makefile    string
		alias       string
		expectError bool
	}{
		{
			name: "no conflict",
			makefile: `BINARIES = grove gm cx
# Some other content`,
			alias:       "gn",
			expectError: false,
		},
		{
			name: "conflict exists",
			makefile: `BINARIES = grove gm cx
# Some other content`,
			alias:       "gm",
			expectError: true,
		},
		{
			name: "conflict in multi-line binaries",
			makefile: `BINARIES = grove \
    gm \
    cx`,
			alias:       "cx",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory
			tmpDir := t.TempDir()
			oldDir, _ := os.Getwd()
			defer os.Chdir(oldDir)
			os.Chdir(tmpDir)

			// Write Makefile
			if err := os.WriteFile("Makefile", []byte(tt.makefile), 0644); err != nil {
				t.Fatalf("Failed to write Makefile: %v", err)
			}

			err := checkBinaryAliasConflict(tt.alias)
			if (err != nil) != tt.expectError {
				t.Errorf("checkBinaryAliasConflict(%s) error = %v, expectError %v", tt.alias, err, tt.expectError)
			}
		})
	}
}
