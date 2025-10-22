package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	stdlog "log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/hpcloud/tail"
	"github.com/mattsolo1/grove-core/logging"
	"github.com/mattsolo1/grove-core/tui/theme"
	"github.com/mattsolo1/grove-core/pkg/workspace"
	"github.com/mattsolo1/grove-meta/pkg/discovery"
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
	cmd.Flags().BoolP("tui", "i", false, "Launch interactive TUI mode")

	return cmd
}

func runLogs(cmd *cobra.Command, args []string) error {
	logger := logging.NewLogger("logs")
	follow, _ := cmd.Flags().GetBool("follow")
	ecosystem, _ := cmd.Flags().GetBool("ecosystem")
	compact, _ := cmd.Flags().GetBool("compact")
	maxFields, _ := cmd.Flags().GetInt("max-fields")
	tableView, _ := cmd.Flags().GetBool("table")
	verbosity, _ := cmd.Flags().GetCount("verbose")
	tuiMode, _ := cmd.Flags().GetBool("tui")
	linesToShow, _ := cmd.Flags().GetInt("lines")

	// Auto-detect TTY and enable TUI mode if interactive
	if !tuiMode && os.Stdin != nil {
		if fi, err := os.Stdout.Stat(); err == nil {
			if (fi.Mode() & os.ModeCharDevice) != 0 {
				tuiMode = true
			}
		}
	}

	// 1. Determine which workspaces to show
	var workspaces []string
	
	if ecosystem {
		// Show all workspaces in ecosystem
		_, err := workspace.FindEcosystemRoot("")
		if err != nil {
			return fmt.Errorf("failed to find workspace root: %w", err)
		}

		projects, err := discovery.DiscoverProjects()
		if err != nil {
			return fmt.Errorf("failed to discover workspaces: %w", err)
		}
		var allWorkspaces []string
		for _, p := range projects {
			allWorkspaces = append(allWorkspaces, p.Path)
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
		_, err := workspace.FindEcosystemRoot("")
		if err != nil {
			return fmt.Errorf("failed to find workspace root: %w", err)
		}

		projects, err := discovery.DiscoverProjects()
		if err != nil {
			return fmt.Errorf("failed to discover workspaces: %w", err)
		}
		var allWorkspaces []string
		for _, p := range projects {
			allWorkspaces = append(allWorkspaces, p.Path)
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

	// If TUI mode is enabled, run the TUI instead
	if tuiMode {
		return runLogsTUI(cmd, workspaces, follow)
	}

	// 3. Concurrently tail files
	lineChan := make(chan TailedLine)
	var wg sync.WaitGroup

	for _, fileInfo := range logFiles {
		wg.Add(1)
		go func(path, wsName string) {
			defer wg.Done()
			
			// 1. Pre-read last N lines if requested.
			if linesToShow > 0 {
				lastLines, err := readLastNLines(path, linesToShow)
				if err != nil {
					logger.Debugf("Error reading last lines from %s: %v", path, err)
				} else {
					for _, line := range lastLines {
						// Ensure we don't send empty strings from the pre-read
						if line != "" {
							lineChan <- TailedLine{Workspace: wsName, Line: line}
						}
					}
				}
			}

			// 2. If not in follow mode, we are done.
			if !follow {
				return
			}

			// 3. If in follow mode, start tailing from the absolute end of the file.
			config := tail.Config{
				Follow:   true,
				ReOpen:   true,
				Location: &tail.SeekInfo{Offset: 0, Whence: io.SeekEnd}, // Start from the end
				Logger:   stdlog.New(ioutil.Discard, "", 0), // Suppress tail library debug output
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
	// Use theme colors for workspace coloring
	colorPalette := []lipgloss.Color{
		theme.DefaultTheme.Info.GetForeground().(lipgloss.Color),
		theme.DefaultTheme.Accent.GetForeground().(lipgloss.Color),
		theme.DefaultTheme.Success.GetForeground().(lipgloss.Color),
		theme.DefaultTheme.Warning.GetForeground().(lipgloss.Color),
		theme.DefaultTheme.Error.GetForeground().(lipgloss.Color),
	}
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
		timeStyle := theme.DefaultTheme.Faint
		componentStyle := theme.DefaultTheme.Muted.Copy().Bold(true)
		fieldStyle := theme.DefaultTheme.Muted

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
		return theme.DefaultTheme.Success
	case "warning", "warn":
		return theme.DefaultTheme.Warning
	case "error", "fatal", "panic":
		return theme.DefaultTheme.Error
	case "debug", "trace":
		return theme.DefaultTheme.Muted
	default:
		return lipgloss.NewStyle()
	}
}

// readLastNLines efficiently reads the last n lines from a file without loading
// the entire file into memory. It reads the file in chunks from the end.
func readLastNLines(path string, n int) ([]string, error) {
	if n <= 0 {
		return []string{}, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}

	filesize := stat.Size()
	if filesize == 0 {
		return []string{}, nil
	}

	// Use a buffer to read chunks of the file
	const bufferSize = 4096
	buffer := make([]byte, bufferSize)
	
	var lines []string
	var lineBuffer []byte
	offset := filesize

	for len(lines) < n && offset > 0 {
		readSize := int64(bufferSize)
		if offset < readSize {
			readSize = offset
		}
		offset -= readSize

		_, err := file.ReadAt(buffer[:readSize], offset)
		if err != nil && err != io.EOF {
			return nil, err
		}

		// Process the buffer from end to start
		for i := int(readSize) - 1; i >= 0; i-- {
			if buffer[i] == '\n' {
				// We found a newline, so we have a complete line
				if len(lineBuffer) > 0 {
					// Prepend to lines (we're reading backwards)
					lines = append([]string{string(lineBuffer)}, lines...)
					lineBuffer = nil
					
					if len(lines) >= n {
						break
					}
				}
			} else {
				// Prepend byte to the current line buffer
				lineBuffer = append([]byte{buffer[i]}, lineBuffer...)
			}
		}

		if len(lines) >= n {
			break
		}
	}
	
	// Add the first line if it wasn't terminated by a newline
	if len(lines) < n && len(lineBuffer) > 0 {
		lines = append([]string{string(lineBuffer)}, lines...)
	}

	// In case we over-shot due to buffer boundaries, trim to N lines
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}

	return lines, nil
}