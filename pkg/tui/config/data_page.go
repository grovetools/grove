package config

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/components/pager"
	"github.com/grovetools/core/tui/theme"

	grovekeymap "github.com/grovetools/grove/pkg/keymap"
)

// dataLayer is one stop of the Data page's layer cycler.
type dataLayer struct {
	name  string
	layer config.ConfigSource
}

// dataLayers is the cycle order: global → ecosystem → notebook → project.
func dataLayers() []dataLayer {
	return []dataLayer{
		{"Global", config.SourceGlobal},
		{"Ecosystem", config.SourceEcosystem},
		{"Notebook", config.SourceProjectNotebook},
		{"Project", config.SourceProject},
	}
}

// DataPage collapses the four per-layer tree editors into a single tab: it
// wraps ONE *LayerPage and retargets it at the next layer when the cycle
// key (ConfigKeyMap.CycleLayer) is pressed. All tree/audit/edit/delete
// behavior — including config-review's orphan surfacing and delete-from-
// layer machinery — lives in the wrapped LayerPage, untouched.
//
// CRITICAL invariant (pager contract): the pager reassigns
// m.pages[activePage] = updated on every message, and LayerPage.Update
// returns its bare *LayerPage receiver. DataPage.Update therefore
// intercepts every message, delegates to the inner LayerPage, DISCARDS the
// promoted return value, and unconditionally returns the DataPage itself —
// otherwise the first keystroke would replace the DataPage in the pager's
// page slice with the bare LayerPage and permanently lose the cycler.
type DataPage struct {
	inner  *LayerPage
	layers []dataLayer
	idx    int
	keys   grovekeymap.ConfigKeyMap
}

// Compile-time interface checks.
var (
	_ pager.Page          = (*DataPage)(nil)
	_ pager.PageWithTitle = (*DataPage)(nil)
	_ pager.PageWithID    = (*DataPage)(nil)
)

// NewDataPage builds the Data page starting on the Global layer.
func NewDataPage(layered *config.LayeredConfig, filters *FilterState, keys grovekeymap.ConfigKeyMap, width, height int) *DataPage {
	layers := dataLayers()
	inner := NewLayerPage(layers[0].name, layers[0].layer, layered, filters, keys, width, height)
	inner.cycleHint = "[" + keys.CycleLayer.Help().Key + ": layer →]"
	return &DataPage{
		inner:  inner,
		layers: layers,
		keys:   keys,
	}
}

// Name implements pager.Page. The tab is always labeled "Data" regardless
// of which layer is showing (the layer shows in the title row instead).
func (dp *DataPage) Name() string { return "Data" }

// TabID implements pager.PageWithID.
func (dp *DataPage) TabID() string { return "data" }

// Title implements pager.PageWithTitle: current layer name + its config
// file path (LayerPageTitle already renders "  <abbreviated path>" muted,
// or "" when the layer has no file).
func (dp *DataPage) Title() string {
	label := theme.DefaultTheme.Muted.Render("  layer: " + LayerDisplayName(dp.inner.layer) + " ·")
	path := LayerPageTitle(dp.inner.layer, dp.inner.config)
	if path == "" {
		path = " " + theme.DefaultTheme.Muted.Render("no config file")
	}
	return label + path
}

// Layer returns the currently displayed config layer.
func (dp *DataPage) Layer() config.ConfigSource { return dp.inner.layer }

func (dp *DataPage) Init() tea.Cmd { return nil }

// Update implements pager.Page. See the type comment for why the inner
// page's returned Page is discarded and dp is always returned.
func (dp *DataPage) Update(msg tea.Msg) (pager.Page, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok && dp.inner.active {
		// Cycle key advances the layer — unless a z-fold chord is
		// pending, in which case the key belongs to the chord and must
		// reach the inner page.
		if key.Matches(keyMsg, dp.keys.CycleLayer) && !dp.inner.IsZChordPending() {
			dp.cycleLayer()
			return dp, nil
		}
	}

	_, cmd := dp.inner.Update(msg) // promoted *LayerPage return intentionally discarded
	return dp, cmd
}

// cycleLayer advances global → ecosystem → notebook → project → global.
func (dp *DataPage) cycleLayer() {
	dp.idx = (dp.idx + 1) % len(dp.layers)
	entry := dp.layers[dp.idx]
	dp.inner.SetLayer(entry.layer, entry.name)
}

func (dp *DataPage) View() string { return dp.inner.View() }

func (dp *DataPage) Focus() tea.Cmd { return dp.inner.Focus() }

func (dp *DataPage) Blur() { dp.inner.Blur() }

func (dp *DataPage) SetSize(width, height int) { dp.inner.SetSize(width, height) }

// Refresh re-derives the inner page's content from a reloaded config.
func (dp *DataPage) Refresh(layered *config.LayeredConfig) { dp.inner.Refresh(layered) }
