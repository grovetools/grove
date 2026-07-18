package config

import (
	"fmt"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/grovetools/core/config"
	"github.com/grovetools/core/tui/components/help"
	"github.com/grovetools/core/tui/components/pager"

	grovekeymap "github.com/grovetools/grove/pkg/keymap"
	"github.com/grovetools/grove/pkg/setup"
)

// SinglePageOpts configures NewSinglePage.
type SinglePageOpts struct {
	// EssentialsOnly filters curated pages to their Essential-tagged rows —
	// onboarding's density (spec 23). Ignored by non-curated pages (Themes,
	// which is already a single decision).
	EssentialsOnly bool
}

// NewSinglePage builds a config Model hosting exactly ONE page, identified
// by its tab ID ("themes", "appearance", "layout", "keys", "notebook"). The onboarding
// steps embed these so the Theme/Keys steps ARE the real config machinery:
// the full commit path (setSettingMsg → TypedValue → WriteTransform →
// SaveGlobalSetting → reloadConfig → SettingAppliedMsg) lives in the Model
// and is reused, not reimplemented. The pager renders without tab chrome
// (one page needs no tabs) and tab-jump/cycle keys are naturally inert.
func NewSinglePage(pageID string, layered *config.LayeredConfig, yamlHandler *setup.YAMLHandler, tomlHandler *setup.TOMLHandler, keys grovekeymap.ConfigKeyMap, opts SinglePageOpts) (Model, error) {
	width, height := 80, 24 // initial dummy size, replaced by WindowSizeMsg
	curatedOpts := CuratedOpts{EssentialsOnly: opts.EssentialsOnly}

	m := Model{
		layered:     layered,
		yamlHandler: yamlHandler,
		tomlHandler: tomlHandler,
		keys:        keys,
		help:        help.NewBuilder().WithKeys(keys).WithTitle("Configuration Editor").Build(),
		state:       viewList,
		// The single-page Model hosts no LayerPage, but the shared filter
		// state must exist for the (inert) filter toggles. Deliberately not
		// loaded from / saved to the persisted UI state: an onboarding
		// embedding must not disturb the user's config-panel preferences.
		filters: &FilterState{ShowPreview: true},
	}

	var page pager.Page
	switch pageID {
	case "themes":
		m.themesPage = NewThemesPage(layered, keys, width, height)
		page = m.themesPage
	case "appearance":
		cp := NewCuratedPage("Appearance", AppearanceSettings(), layered, keys, width, height, curatedOpts)
		m.curatedPages = []*CuratedPage{cp}
		page = cp
	case "layout":
		cp := NewCuratedPage("Layout", LayoutSettings(), layered, keys, width, height, curatedOpts)
		m.curatedPages = []*CuratedPage{cp}
		page = cp
	case "keys":
		cp := NewCuratedPage("Keys", KeysSettings(), layered, keys, width, height, curatedOpts)
		m.curatedPages = []*CuratedPage{cp}
		m.keysPage = cp
		page = cp
	case "notebook":
		cp := NewCuratedPage("Notebook", NotebookSettings(), layered, keys, width, height, curatedOpts)
		m.curatedPages = []*CuratedPage{cp}
		page = cp
	default:
		return Model{}, fmt.Errorf("unknown config page %q", pageID)
	}

	pagerCfg := pager.Config{
		OuterPadding: [4]int{1, 2, 0, 2},
		ShowTitleRow: true, // keeps the "saves to <path>" transparency line
		FooterHeight: 2,    // status + help line
		HideTabBar:   true,
	}
	m.pager = pager.NewWith([]pager.Page{page}, newPagerKeyMap(keys), pagerCfg)

	ti := textinput.New()
	ti.Prompt = "  > "
	ti.CharLimit = 200
	ti.Width = 50
	m.input = ti

	page.Focus()
	return m, nil
}

// newPagerKeyMap adapts the config keymap's tab bindings for the pager.
// Shared by New (five tabs) and NewSinglePage (where the jumps are no-ops).
func newPagerKeyMap(keys grovekeymap.ConfigKeyMap) pager.KeyMap {
	return pager.KeyMap{
		Tab1:    keys.Base.Tab1,
		Tab2:    keys.Base.Tab2,
		Tab3:    keys.Base.Tab3,
		Tab4:    keys.Base.Tab4,
		Tab5:    keys.Base.Tab5,
		Tab6:    keys.Base.Tab6,
		Tab7:    keys.Base.Tab7,
		Tab8:    keys.Base.Tab8,
		Tab9:    keys.Base.Tab9,
		NextTab: keys.NextPage,
		PrevTab: keys.PrevPage,
	}
}

// TextEntryActive reports whether the active page currently owns raw key
// input — an inline editor open or a key capture armed. Embedding hosts
// (the treemux onboarding takeover) check this to forward every keystroke
// wholesale instead of consuming their own navigation keys.
func (m Model) TextEntryActive() bool {
	if ti, ok := m.pager.Active().(pager.PageWithTextInput); ok {
		return ti.IsTextEntryActive()
	}
	return false
}
