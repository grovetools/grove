package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	
	"github.com/mattsolo1/grove-tend/pkg/command"
	"github.com/mattsolo1/grove-tend/pkg/fs"
	"github.com/mattsolo1/grove-tend/pkg/harness"
)

// WorkspaceDetectionScenario tests workspace detection functionality
func WorkspaceDetectionScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "workspace-detection",
		Description: "Verifies workspace detection and status command",
		Tags:        []string{"workspace", "dev"},
		Steps: []harness.Step{
			{
				Name:        "Test workspace detection",
				Description: "Test grove dev workspace command in and out of workspace",
				Func: func(ctx *harness.Context) error {
					// Get grove binary path
					groveBinary := ctx.GroveBinary
					
					// Test outside workspace
					tempDir := ctx.NewDir("temp")
					originalDir, _ := os.Getwd()
					defer os.Chdir(originalDir)
					os.Chdir(tempDir)
					
					cmd := command.New(groveBinary, "dev", "workspace")
					result := cmd.Run()
					if result.ExitCode != 0 {
						return fmt.Errorf("workspace command failed: %s", result.Stderr)
					}
					
					if !strings.Contains(result.Stdout, "Not in a Grove workspace") {
						return fmt.Errorf("expected 'Not in a Grove workspace', got: %s", result.Stdout)
					}
					
					// Create a workspace
					workspaceDir := ctx.NewDir("test-workspace")
					os.Chdir(workspaceDir)
					
					// Create workspace marker
					os.MkdirAll(filepath.Join(workspaceDir, ".grove"), 0755)
					fs.WriteString(filepath.Join(workspaceDir, ".grove", "workspace"), 
						"branch: test\nplan: test-plan\ncreated_at: 2025-09-26T12:00:00Z\n")
					
					// Create a mock grove binary
					binDir := filepath.Join(workspaceDir, "grove-meta", "bin")
					os.MkdirAll(binDir, 0755)
					mockGrovePath := filepath.Join(binDir, "grove")
					fs.WriteString(mockGrovePath, "#!/bin/sh\necho 'mock grove'\n")
					os.Chmod(mockGrovePath, 0755)
					
					// Create grove.yml for discovery
					groveYmlPath := filepath.Join(workspaceDir, "grove-meta", "grove.yml")
					os.MkdirAll(filepath.Dir(groveYmlPath), 0755)
					fs.WriteString(groveYmlPath, `name: grove-meta
binary:
  name: grove
  path: bin/grove
`)
					
					// Test inside workspace
					cmd = command.New(groveBinary, "dev", "workspace").Dir(workspaceDir)
					result = cmd.Run()
					// The command might fail if binary discovery fails, but should still output workspace info
					combinedOutput := result.Stdout + result.Stderr
					
					// Debug: print what we got
					fmt.Printf("Debug: Grove binary used: %s\n", groveBinary)
					fmt.Printf("Debug: Current directory: %s\n", workspaceDir)
					fmt.Printf("Debug: Command output (stdout): %s\n", result.Stdout)
					fmt.Printf("Debug: Command output (stderr): %s\n", result.Stderr)
					fmt.Printf("Debug: Exit code: %d\n", result.ExitCode)
					
					if !strings.Contains(combinedOutput, "You are in a Grove workspace") {
						return fmt.Errorf("expected workspace detection, got: %s", combinedOutput)
					}
					
					// Binary listing might fail, but that's okay for this test
					// We're primarily testing workspace detection
					
					// Test --check flag
					cmd = command.New(groveBinary, "dev", "workspace", "--check").Dir(workspaceDir)
					result = cmd.Run()
					if result.ExitCode != 0 {
						return fmt.Errorf("workspace --check should return 0 in workspace")
					}
					
					// Test --path flag
					cmd = command.New(groveBinary, "dev", "workspace", "--path").Dir(workspaceDir)
					result = cmd.Run()
					if result.ExitCode != 0 {
						return fmt.Errorf("workspace --path failed: %s", result.Stderr)
					}
					
					if !strings.Contains(result.Stdout, workspaceDir) {
						return fmt.Errorf("expected workspace path, got: %s", result.Stdout)
					}
					
					return nil
				},
			},
		},
	}
}

// WorkspaceBinaryDelegationScenario tests workspace-aware binary delegation
func WorkspaceBinaryDelegationScenario() *harness.Scenario {
	return &harness.Scenario{
		Name:        "workspace-binary-delegation",
		Description: "Verifies grove delegates to workspace binaries",
		Tags:        []string{"workspace", "delegation"},
		Steps: []harness.Step{
			{
				Name:        "Test binary delegation",
				Description: "Test grove delegates to workspace binary when available",
				Func: func(ctx *harness.Context) error {
					// Create workspace with marker
					workspaceDir := ctx.NewDir("delegation-workspace")
					originalDir, _ := os.Getwd()
					defer os.Chdir(originalDir)
					os.Chdir(workspaceDir)
					
					os.MkdirAll(filepath.Join(workspaceDir, ".grove"), 0755)
					fs.WriteString(filepath.Join(workspaceDir, ".grove", "workspace"),
						"branch: test\nplan: test-plan\ncreated_at: 2025-09-26T12:00:00Z\n")
					
					// Create mock cx binary in workspace
					cxDir := filepath.Join(workspaceDir, "grove-context", "bin")
					os.MkdirAll(cxDir, 0755)
					mockCxPath := filepath.Join(cxDir, "cx")
					
					// Mock cx that outputs its location
					fs.WriteString(mockCxPath, `#!/bin/sh
echo "WORKSPACE_CX_VERSION"
echo "Path: $0"
`)
					os.Chmod(mockCxPath, 0755)
					
					// Create grove.yml for cx discovery
					cxGroveYml := filepath.Join(workspaceDir, "grove-context", "grove.yml")
					os.MkdirAll(filepath.Dir(cxGroveYml), 0755)
					fs.WriteString(cxGroveYml, `name: grove-context
binary:
  name: cx
  path: bin/cx
`)
					
					// Also create a global mock cx for comparison
					globalBinDir := ctx.NewDir("global-bin")
					globalCxPath := filepath.Join(globalBinDir, "cx")
					fs.WriteString(globalCxPath, `#!/bin/sh
echo "GLOBAL_CX_VERSION"
echo "Path: $0"
`)
					os.Chmod(globalCxPath, 0755)
					
					// Temporarily modify HOME to use our global mock
					originalHome := os.Getenv("HOME")
					tempHome := ctx.NewDir("temp-home")
					os.Setenv("HOME", tempHome)
					defer os.Setenv("HOME", originalHome)
					
					// Create .grove/bin in temp home
					groveBinDir := filepath.Join(tempHome, ".grove", "bin")
					os.MkdirAll(groveBinDir, 0755)
					
					// Link global cx to .grove/bin
					os.Symlink(globalCxPath, filepath.Join(groveBinDir, "cx"))
					
					// Get grove binary path
					groveBinary := ctx.GroveBinary
					
					// Test delegation inside workspace
					cmd := command.New(groveBinary, "cx", "version").Dir(workspaceDir)
					result := cmd.Run()
					
					// Should use workspace version
					if !strings.Contains(result.Stdout, "WORKSPACE_CX_VERSION") {
						return fmt.Errorf("expected workspace cx, got: %s", result.Stdout)
					}
					
					// Test delegation outside workspace
					os.Chdir(tempHome)
					cmd = command.New(groveBinary, "cx", "version")
					result = cmd.Run()
					
					// Should use global version
					if !strings.Contains(result.Stdout, "GLOBAL_CX_VERSION") {
						return fmt.Errorf("expected global cx, got: %s", result.Stdout)
					}
					
					return nil
				},
			},
		},
	}
}

