package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/hpcloud/tail"
	"github.com/mattsolo1/grove-core/cli"
	"github.com/mattsolo1/grove-meta/pkg/workspace"
	"github.com/spf13/cobra"
)

// LogEntry represents a parsed log line from a JSON log file.
type LogEntry struct {
	Component string    `json:"component"`
	Level     string    `json:"level"`
	Message   string    `json:"msg"`
	Time      time.Time `json:"time"`
}

// TailedLine wraps a log line with its source workspace.
type TailedLine struct {
	Workspace string
	Line      string
}

func newLogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs [workspace-filter...]",
		Short: "Tail logs from current workspace or specified workspaces",
		Long: `Provides a real-time view of logs from workspaces within a Grove ecosystem.

By default, shows logs from the current workspace only. Use --ecosystem to show logs
from all workspaces, or specify workspace filters to show specific workspaces.

It discovers and tails log files from each workspace's .grove/logs directory,
parses the structured log entries, and displays them in a color-coded, readable format.`,
		RunE: runLogs,
	}

	cmd.Flags().BoolP("follow", "f", true, "Continuously tail logs")
	cmd.Flags().IntP("lines", "n", 10, "Number of lines to show from the end of the logs")
	cmd.Flags().Bool("ecosystem", false, "Show logs from all workspaces in the ecosystem")
	cmd.Flags().Bool("compact", false, "Show only essential fields for cleaner output")
	cmd.Flags().Int("max-fields", 5, "Maximum number of additional fields to show on one line (0 = unlimited)")
	cmd.Flags().Bool("table", false, "Show structured fields in a table below each log line")
	cmd.Flags().CountP("verbose", "v", "Increase verbosity (use -v, -vv, -vvv for more detail)")

	return cmd
}

func runLogs(cmd *cobra.Command, args []string) error {
	logger := cli.GetLogger(cmd)
	follow, _ := cmd.Flags().GetBool("follow")
	ecosystem, _ := cmd.Flags().GetBool("ecosystem")
	compact, _ := cmd.Flags().GetBool("compact")
	maxFields, _ := cmd.Flags().GetInt("max-fields")
	tableView, _ := cmd.Flags().GetBool("table")
	verbosity, _ := cmd.Flags().GetCount("verbose")
	_, _ = cmd.Flags().GetInt("lines") // lines parameter for future use

	// 1. Determine which workspaces to show
	var workspaces []string
	
	if ecosystem {
		// Show all workspaces in ecosystem
		rootDir, err := workspace.FindRoot("")
		if err != nil {
			return fmt.Errorf("failed to find workspace root: %w", err)
		}

		allWorkspaces, err := workspace.Discover(rootDir)
		if err != nil {
			return fmt.Errorf("failed to discover workspaces: %w", err)
		}
		workspaces = allWorkspaces
		
		// Filter workspaces if args are provided with --ecosystem
		if len(args) > 0 {
			var filtered []string
			for _, ws := range workspaces {
				wsName := filepath.Base(ws)
				for _, filter := range args {
					if strings.Contains(wsName, filter) {
						filtered = append(filtered, ws)
						break
					}
				}
			}
			workspaces = filtered
		}
	} else if len(args) > 0 {
		// Show specific workspaces by filter
		rootDir, err := workspace.FindRoot("")
		if err != nil {
			return fmt.Errorf("failed to find workspace root: %w", err)
		}

		allWorkspaces, err := workspace.Discover(rootDir)
		if err != nil {
			return fmt.Errorf("failed to discover workspaces: %w", err)
		}
		
		for _, ws := range allWorkspaces {
			wsName := filepath.Base(ws)
			for _, filter := range args {
				if strings.Contains(wsName, filter) {
					workspaces = append(workspaces, ws)
					break
				}
			}
		}
	} else {
		// Default: show current workspace only
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		
		// Check if current directory is a workspace (has grove.yml)
		if _, err := os.Stat(filepath.Join(cwd, "grove.yml")); err == nil {
			workspaces = []string{cwd}
		} else {
			return fmt.Errorf("current directory is not a Grove workspace (no grove.yml found)")
		}
	}

	if len(workspaces) == 0 {
		logger.Info("No matching workspaces found.")
		return nil
	}

	// 2. Discover log files (look for workspace-*.log files)
	var logFiles []struct{ Path, Workspace string }
	for _, ws := range workspaces {
		logDir := filepath.Join(ws, ".grove", "logs")
		files, err := filepath.Glob(filepath.Join(logDir, "workspace-*.log"))
		if err != nil {
			continue
		}
		for _, file := range files {
			logFiles = append(logFiles, struct{ Path, Workspace string }{file, filepath.Base(ws)})
		}
	}

	if len(logFiles) == 0 {
		logger.Info("No log files found in any workspace.")
		return nil
	}

	// 3. Concurrently tail files
	lineChan := make(chan TailedLine)
	var wg sync.WaitGroup

	for _, fileInfo := range logFiles {
		wg.Add(1)
		go func(path, wsName string) {
			defer wg.Done()
			config := tail.Config{
				Follow: follow,
				ReOpen: follow, // Only reopen if following
			}
			// When following, start from the end
			if follow {
				config.Location = &tail.SeekInfo{Offset: 0, Whence: io.SeekEnd}
			} else {
				// If not following, read from beginning to see existing logs
				config.Location = &tail.SeekInfo{Offset: 0, Whence: io.SeekStart}
			}

			t, err := tail.TailFile(path, config)
			if err != nil {
				logger.Debugf("Cannot tail file %s: %v", path, err)
				return
			}
			for line := range t.Lines {
				if line.Err != nil {
					logger.Debugf("Error reading line from %s: %v", path, line.Err)
					continue
				}
				lineChan <- TailedLine{Workspace: wsName, Line: line.Text}
			}
		}(fileInfo.Path, fileInfo.Workspace)
	}

	go func() {
		wg.Wait()
		close(lineChan)
	}()

	// 4. Process and render logs
	colorPalette := []lipgloss.Color{"39", "45", "51", "81", "117", "153", "189", "225"}
	workspaceColors := make(map[string]lipgloss.Style)
	colorIndex := 0

	for tailedLine := range lineChan {
		// Parse the JSON log entry into a generic map to capture all fields
		var rawEntry map[string]interface{}
		if err := json.Unmarshal([]byte(tailedLine.Line), &rawEntry); err != nil {
			// Print as-is if not JSON
			fmt.Println(tailedLine.Line)
			continue
		}

		// Extract standard fields
		level, _ := rawEntry["level"].(string)
		message, _ := rawEntry["msg"].(string)
		component, _ := rawEntry["component"].(string)
		timeStr, _ := rawEntry["time"].(string)
		
		// Parse time
		var logTime time.Time
		if parsedTime, err := time.Parse(time.RFC3339, timeStr); err == nil {
			logTime = parsedTime
		}

		// Assign color to workspace
		if _, ok := workspaceColors[tailedLine.Workspace]; !ok {
			color := colorPalette[colorIndex%len(colorPalette)]
			workspaceColors[tailedLine.Workspace] = lipgloss.NewStyle().Foreground(color).Bold(true)
			colorIndex++
		}

		// Styling
		wsStyle := workspaceColors[tailedLine.Workspace]
		levelStyle := getLevelStyle(level)
		timeStyle := lipgloss.NewStyle().Faint(true)
		componentStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Bold(true)
		fieldStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))

		// Build the base log line
		baseLine := fmt.Sprintf("%s %s %s %s %s",
			wsStyle.Render(fmt.Sprintf("[%s]", tailedLine.Workspace)),
			levelStyle.Render(fmt.Sprintf("[%s]", strings.ToUpper(level))),
			timeStyle.Render(logTime.Format("15:04:05")),
			componentStyle.Render(fmt.Sprintf("[%s]", component)),
			message,
		)

		// Collect additional fields (excluding standard ones)
		standardFields := map[string]bool{
			"level": true, "msg": true, "component": true, "time": true, "_verbosity": true,
		}
		
		// Extract verbosity metadata if present
		var verbosityMap map[string]int
		if verbosityRaw, exists := rawEntry["_verbosity"]; exists {
			if verbosityMapInterface, ok := verbosityRaw.(map[string]interface{}); ok {
				verbosityMap = make(map[string]int)
				for key, val := range verbosityMapInterface {
					if intVal, ok := val.(float64); ok {
						verbosityMap[key] = int(intVal)
					}
				}
			}
		}
		
		var additionalFields []string
		var basicFieldsFound []string
		var verboseFieldsFound []string
		var debugFieldsFound []string
		var metricFieldsFound []string
		var otherFields []string
		
		// Categorize fields by verbosity level using metadata
		for key, value := range rawEntry {
			if !standardFields[key] {
				// Format different types appropriately
				var formattedValue string
				switch v := value.(type) {
				case string:
					formattedValue = v
				case float64:
					// Check if it's an integer
					if v == float64(int64(v)) {
						formattedValue = fmt.Sprintf("%.0f", v)
					} else {
						formattedValue = fmt.Sprintf("%.2f", v)
					}
				case bool:
					formattedValue = fmt.Sprintf("%t", v)
				default:
					formattedValue = fmt.Sprintf("%v", v)
				}
				
				fieldStr := fmt.Sprintf("%s=%s", key, formattedValue)
				
				// Determine verbosity level from metadata or fallback to legacy logic
				verbosityLevel := 0
				if verbosityMap != nil {
					if level, exists := verbosityMap[key]; exists {
						verbosityLevel = level
					}
				} else {
					// Legacy compatibility: check for old prefix system and known metric fields
					if strings.HasPrefix(key, "v1_") {
						verbosityLevel = 1
					} else if strings.HasPrefix(key, "v2_") {
						verbosityLevel = 2
					} else if strings.HasPrefix(key, "v3_") {
						verbosityLevel = 3
					} else {
						// Check if it's a known metric field
						metricFields := []string{"model", "response_time_ms", "completion_tokens", "total_prompt_tokens", "cached_tokens", "job_type", "session", "user_id", "request_id"}
						for _, metricField := range metricFields {
							if key == metricField {
								verbosityLevel = 3
								break
							}
						}
					}
				}
				
				// Categorize by determined verbosity level
				switch verbosityLevel {
				case 0:
					basicFieldsFound = append(basicFieldsFound, fieldStr)
				case 1:
					verboseFieldsFound = append(verboseFieldsFound, fieldStr)
				case 2:
					debugFieldsFound = append(debugFieldsFound, fieldStr)
				case 3:
					metricFieldsFound = append(metricFieldsFound, fieldStr)
				default:
					otherFields = append(otherFields, fieldStr)
				}
			}
		}
		
		// Build the final fields list based on verbosity level
		if compact {
			// In compact mode, only show basic fields
			additionalFields = basicFieldsFound
		} else {
			// Build fields based on verbosity level
			additionalFields = basicFieldsFound // Always show basic fields
			
			if verbosity >= 1 {
				additionalFields = append(additionalFields, verboseFieldsFound...)
			}
			if verbosity >= 2 {
				additionalFields = append(additionalFields, debugFieldsFound...)
			}
			if verbosity >= 3 {
				additionalFields = append(additionalFields, metricFieldsFound...)
			}
			
			// Always add other unknown fields at the highest verbosity
			if verbosity >= 3 {
				additionalFields = append(additionalFields, otherFields...)
			}
			
			// Apply max-fields limit if specified
			if maxFields > 0 && len(additionalFields) > maxFields {
				truncated := additionalFields[:maxFields]
				remaining := len(additionalFields) - maxFields
				additionalFields = append(truncated, fmt.Sprintf("...+%d more", remaining))
			}
		}

		// Print the log entry
		if len(additionalFields) > 0 {
			if tableView {
				// Print the base log line
				fmt.Printf("%s\n", baseLine)
				
				// Extract file/func info for separate display
				var fileInfo, funcInfo string
				for key, value := range rawEntry {
					if key == "file" {
						fileInfo = fmt.Sprintf("%v", value)
					} else if key == "func" {
						funcInfo = fmt.Sprintf("%v", value)
					}
				}
				
				// Print file/func on separate lines first
				if fileInfo != "" || funcInfo != "" {
					if fileInfo != "" {
						fmt.Printf("    📁 %s\n", fieldStyle.Render(fileInfo))
					}
					if funcInfo != "" {
						fmt.Printf("    ⚙️  %s\n", fieldStyle.Render(funcInfo))
					}
				}
				
				// Organize fields by verbosity level for ordered display
				verbosityLevels := [][]string{
					{}, // level 0 - basic
					{}, // level 1 - verbose  
					{}, // level 2 - debug
					{}, // level 3 - metrics
				}
				
				// Sort fields by verbosity level, excluding file/func which we showed above
				for key, value := range rawEntry {
					if !standardFields[key] && key != "file" && key != "func" {
						// Format the value
						var formattedValue string
						switch v := value.(type) {
						case string:
							formattedValue = v
						case float64:
							if v == float64(int64(v)) {
								formattedValue = fmt.Sprintf("%.0f", v)
							} else {
								formattedValue = fmt.Sprintf("%.2f", v)
							}
						case bool:
							formattedValue = fmt.Sprintf("%t", v)
						default:
							formattedValue = fmt.Sprintf("%v", v)
						}
						
						// Determine verbosity level
						verbosityLevel := 0
						if verbosityMap != nil {
							if level, exists := verbosityMap[key]; exists {
								verbosityLevel = level
							}
						} else {
							// Legacy compatibility
							if strings.HasPrefix(key, "v1_") {
								verbosityLevel = 1
							} else if strings.HasPrefix(key, "v2_") {
								verbosityLevel = 2
							} else if strings.HasPrefix(key, "v3_") {
								verbosityLevel = 3
							} else {
								// Check known metric fields
								metricFields := []string{"model", "response_time_ms", "completion_tokens", "total_prompt_tokens", "cached_tokens", "job_type", "session", "user_id", "request_id"}
								for _, metricField := range metricFields {
									if key == metricField {
										verbosityLevel = 3
										break
									}
								}
							}
						}
						
						// Only show fields up to current verbosity level
						if verbosityLevel <= verbosity || (verbosityLevel == 0 && verbosity >= 0) {
							if verbosityLevel < len(verbosityLevels) {
								fieldStr := fmt.Sprintf("%s=%s", key, formattedValue)
								verbosityLevels[verbosityLevel] = append(verbosityLevels[verbosityLevel], fieldStr)
							}
						}
					}
				}
				
				// Print fields organized by verbosity level with color coding
				hasFields := false
				for _, fields := range verbosityLevels {
					if len(fields) > 0 {
						hasFields = true
						break
					}
				}
				
				if hasFields {
					fmt.Printf("    ┌─ Fields:\n")
					
					// Grey gradient styles and connectors for different verbosity levels
					verbosityStyles := []lipgloss.Style{
						lipgloss.NewStyle().Foreground(lipgloss.Color("255")), // level 0 - lightest
						lipgloss.NewStyle().Foreground(lipgloss.Color("250")), // level 1
						lipgloss.NewStyle().Foreground(lipgloss.Color("245")), // level 2  
						lipgloss.NewStyle().Foreground(lipgloss.Color("240")), // level 3 - darkest
					}
					
					// Different connectors for each verbosity level
					verbosityConnectors := []string{
						"├─", // level 0 - standard
						"╞═", // level 1 - double line
						"└•", // level 2 - bullet
						"⋯ ", // level 3 - ellipsis
					}
					
					type StyledField struct {
						Content   string
						Level     int
						IsLast    bool
					}
					
					var allFieldsOrdered []StyledField
					for level, fields := range verbosityLevels {
						for _, field := range fields {
							parts := strings.SplitN(field, "=", 2)
							if len(parts) == 2 {
								key := parts[0]
								value := parts[1]
								
								style := verbosityStyles[level]
								styledField := fmt.Sprintf("%-20s %s", style.Render(key+":"), value)
								allFieldsOrdered = append(allFieldsOrdered, StyledField{
									Content: styledField,
									Level:   level,
								})
							}
						}
					}
					
					// Mark the last field
					if len(allFieldsOrdered) > 0 {
						allFieldsOrdered[len(allFieldsOrdered)-1].IsLast = true
					}
					
					// Print all fields with appropriate connectors
					for _, styledField := range allFieldsOrdered {
						var connector string
						if styledField.IsLast {
							connector = "└─" // Always use └─ for the last item
						} else if styledField.Level < len(verbosityConnectors) {
							connector = verbosityConnectors[styledField.Level]
						} else {
							connector = "├─" // fallback
						}
						fmt.Printf("    %s %s\n", connector, styledField.Content)
					}
					fmt.Println() // Add spacing between log entries
				}
			} else {
				// Original inline display
				if !compact && len(additionalFields) > 8 {
					fmt.Printf("%s\n", baseLine)
					fmt.Printf("    %s\n", fieldStyle.Render(strings.Join(additionalFields, " ")))
				} else {
					fmt.Printf("%s %s\n", baseLine, fieldStyle.Render(strings.Join(additionalFields, " ")))
				}
			}
		} else {
			fmt.Printf("%s\n", baseLine)
		}
	}

	return nil
}

func getLevelStyle(level string) lipgloss.Style {
	switch strings.ToLower(level) {
	case "info":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42")) // Green
	case "warning", "warn":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214")) // Yellow
	case "error", "fatal", "panic":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true) // Red
	case "debug", "trace":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("245")) // Gray
	default:
		return lipgloss.NewStyle()
	}
}