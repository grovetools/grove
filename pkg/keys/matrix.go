package keys

import "sort"

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
	sort.Strings(report.TUINames)

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
