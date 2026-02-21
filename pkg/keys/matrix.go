package keys

import (
	"sort"
	"strings"
)

// MatrixRow represents a single key and its usage across TUIs.
type MatrixRow struct {
	Key          string            `json:"key"`
	TUIs         map[string]string `json:"tuis"`
	Consistent   bool              `json:"consistent"`
	ConflictType string            `json:"conflict_type,omitempty"`
}

// MatrixReport contains the full matrix view of keys across TUIs.
type MatrixReport struct {
	Rows     []MatrixRow `json:"rows"`
	TUINames []string    `json:"tui_names"`
}

// BuildMatrix creates a matrix view showing what each key does in each TUI.
func BuildMatrix(bindings []KeyBinding) MatrixReport {
	rowMap := make(map[string]*MatrixRow)
	tuiSet := make(map[string]bool)

	for _, b := range bindings {
		if b.Domain != DomainTUI && b.Domain != DomainTmux {
			continue
		}

		tuiName := b.Source
		if b.Domain == DomainTmux {
			tuiName = "tmux-popup"
		}
		tuiSet[tuiName] = true

		for _, k := range b.Keys {
			if rowMap[k] == nil {
				rowMap[k] = &MatrixRow{
					Key:  k,
					TUIs: make(map[string]string),
				}
			}
			rowMap[k].TUIs[tuiName] = NormalizeAction(b.Action)
		}
	}

	report := MatrixReport{}
	for t := range tuiSet {
		report.TUINames = append(report.TUINames, t)
	}
	// Sort by package first, then by TUI name within package
	// TUI names are formatted as "tui-name (package)"
	sort.Slice(report.TUINames, func(i, j int) bool {
		pkgI := extractPackage(report.TUINames[i])
		pkgJ := extractPackage(report.TUINames[j])
		if pkgI != pkgJ {
			return pkgI < pkgJ
		}
		return report.TUINames[i] < report.TUINames[j]
	})

	for _, row := range rowMap {
		// Determine consistency - check if all TUIs that use this key
		// have the same action
		firstAction := ""
		consistent := true
		for _, action := range row.TUIs {
			if firstAction == "" {
				firstAction = action
			} else if action != firstAction {
				consistent = false
				row.ConflictType = "semantic"
				break
			}
		}
		row.Consistent = consistent
		report.Rows = append(report.Rows, *row)
	}

	// Sort alphabetically by key
	sort.Slice(report.Rows, func(i, j int) bool {
		return report.Rows[i].Key < report.Rows[j].Key
	})

	return report
}

// extractPackage extracts the package name from a TUI name formatted as "tui-name (package)".
func extractPackage(tuiName string) string {
	start := strings.LastIndex(tuiName, "(")
	end := strings.LastIndex(tuiName, ")")
	if start != -1 && end > start {
		return tuiName[start+1 : end]
	}
	return tuiName
}
