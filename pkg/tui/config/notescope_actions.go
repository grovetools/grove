package config

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/grovetools/core/pkg/coderoot"
	"github.com/grovetools/core/pkg/syncproto"
	"github.com/grovetools/grove/pkg/notescope"
)

// The Model's half of the P3 scope seam: turning one page intent into one verb
// run, off the update loop.
//
// Every one of these is reached from a keypress and from nothing else. They are
// tea.Cmds rather than inline calls because a share can talk to a server for
// several seconds and a TUI that blocks its update loop on that looks hung —
// and because a Cmd is what a test can invoke deliberately, one step at a time.

type (
	// joinDeltaLoadedMsg carries one answered inventory back to the page.
	joinDeltaLoadedMsg struct {
		inventory syncproto.InventoryResponse
		scanned   []notescope.Notebook
		err       error
	}
	// scopeActionDoneMsg carries one finished verb run — its evidence, and its
	// refusal when it refused.
	scopeActionDoneMsg struct {
		result notescope.ActionResult
		err    error
	}
)

// resolveScope builds the Service these acts run through. The field exists so
// a test can drive every keypath against a fake that touches no server and no
// live config; production leaves it nil and gets whatever this binary
// registered (grove's cmd package does; a host that carries no device session
// registers nothing and the pages say so).
func (m Model) resolveScope() (notescope.Service, error) {
	if m.scopeService != nil {
		return m.scopeService()
	}
	return notescope.ResolveService()
}

// fetchJoinDelta asks the server what it holds and re-reads the local side, so
// both halves of the comparison are as of the same keypress.
func (m Model) fetchJoinDelta() tea.Cmd {
	resolve := m.resolveScope
	return func() tea.Msg {
		service, err := resolve()
		if err != nil {
			return joinDeltaLoadedMsg{err: err}
		}
		inventory, err := service.Inventory(context.Background())
		if err != nil {
			return joinDeltaLoadedMsg{err: err}
		}
		table, err := coderoot.Load()
		if err != nil {
			return joinDeltaLoadedMsg{err: err}
		}
		scanned, err := notescope.Scan(table)
		if err != nil {
			return joinDeltaLoadedMsg{err: err}
		}
		return joinDeltaLoadedMsg{inventory: inventory, scanned: scanned}
	}
}

// runScopeAction runs one verb and reports what it printed. The error is
// carried rather than swallowed: these verbs refuse for reasons the operator
// needs (a missing root, a duplicate stamp id, a server that says no), and a
// refusal is the most useful thing this page can display.
func (m Model) runScopeAction(act func(context.Context, notescope.Service) (notescope.ActionResult, error)) tea.Cmd {
	resolve := m.resolveScope
	return func() tea.Msg {
		service, err := resolve()
		if err != nil {
			return scopeActionDoneMsg{err: err}
		}
		result, err := act(context.Background(), service)
		return scopeActionDoneMsg{result: result, err: err}
	}
}
