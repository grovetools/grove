package config

import (
	"context"
	"time"

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

// scopeBusyStatus is what a second intent gets while the first is in flight.
// It names the state rather than failing silently: the operator pressed a key
// and is owed an answer for it, and "nothing happened" is the one answer a
// page that runs verbs must never give.
const scopeBusyStatus = "a notebook-scope act is already running; press esc to cancel it"

// scopeCancelStatus is what esc leaves behind. It says the act was abandoned
// rather than that it failed, because a canceled share may well have already
// written half of what it was going to write, and "canceled" is the only claim
// this page can honestly make about it.
const scopeCancelStatus = "notebook-scope act canceled; re-read the state before acting again"

// How long an act may run before its context is cut.
//
// These verbs reach a sync server, and a server that accepts the connection and
// then never answers used to leave the page busy forever: `scopeBusy` was
// cleared only by the completion message, so the one thing that could clear it
// was the thing that never happened. The CLI is exempt because an operator can
// ^C it; the TUI has no such key by default, so the bound is here and esc is
// wired to it below.
//
// The fetch is one GET and gets the short bound. The acts walk every notespace
// in a notebook and can legitimately take a while, so theirs is generous — the
// point is that it is finite, not that it is tight.
const (
	scopeFetchTimeout = 30 * time.Second
	scopeActTimeout   = 2 * time.Minute
)

type (
	// joinDeltaLoadedMsg carries one answered inventory back to the page.
	joinDeltaLoadedMsg struct {
		// gen is the dispatch this answer belongs to; see Model.scopeGen. A
		// canceled act's answer still arrives, and must not be mistaken for
		// the answer to whatever the operator did next.
		gen       uint64
		inventory syncproto.InventoryResponse
		scanned   []notescope.Notebook
		err       error
	}
	// scopeActionDoneMsg carries one finished verb run — its evidence, and its
	// refusal when it refused.
	scopeActionDoneMsg struct {
		gen    uint64
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
//
// The context comes from the caller (Model.beginScopeAct) rather than being
// made here: the Model has to hold the cancel func to be able to press it.
func (m Model) fetchJoinDelta(ctx context.Context, gen uint64) tea.Cmd {
	resolve := m.resolveScope
	return func() tea.Msg {
		service, err := resolve()
		if err != nil {
			return joinDeltaLoadedMsg{gen: gen, err: err}
		}
		inventory, err := service.Inventory(ctx)
		if err != nil {
			return joinDeltaLoadedMsg{gen: gen, err: err}
		}
		table, err := coderoot.Load()
		if err != nil {
			return joinDeltaLoadedMsg{gen: gen, err: err}
		}
		scanned, err := notescope.Scan(table)
		if err != nil {
			return joinDeltaLoadedMsg{gen: gen, err: err}
		}
		return joinDeltaLoadedMsg{gen: gen, inventory: inventory, scanned: scanned}
	}
}

// runScopeAction runs one verb and reports what it printed. The error is
// carried rather than swallowed: these verbs refuse for reasons the operator
// needs (a missing root, a duplicate stamp id, a server that says no), and a
// refusal is the most useful thing this page can display.
func (m Model) runScopeAction(ctx context.Context, gen uint64, act func(context.Context, notescope.Service) (notescope.ActionResult, error)) tea.Cmd {
	resolve := m.resolveScope
	return func() tea.Msg {
		service, err := resolve()
		if err != nil {
			return scopeActionDoneMsg{gen: gen, err: err}
		}
		result, err := act(ctx, service)
		return scopeActionDoneMsg{gen: gen, result: result, err: err}
	}
}

// beginScopeAct opens the one in-flight act: it takes the gate, mints the
// bounded context the act runs under, and records the cancel func so esc (and
// the completion path) can release it.
//
// The generation counter is what makes cancel honest. Canceling a context does
// not un-launch the goroutine carrying it, so the abandoned act's message still
// arrives — possibly after the operator has started another one. Stamping each
// dispatch and dropping any answer that does not carry the current stamp means
// a canceled fetch cannot repaint the page underneath the act that replaced it.
func (m *Model) beginScopeAct(timeout time.Duration) (context.Context, uint64) {
	m.releaseScopeAct()
	m.scopeGen++
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	m.scopeCancel = cancel
	m.scopeBusy = true
	return ctx, m.scopeGen
}

// releaseScopeAct drops the current act's context. Every path out of the busy
// state goes through it, because context.WithTimeout leaks its timer until its
// cancel is called even when the work finished on its own.
func (m *Model) releaseScopeAct() {
	if m.scopeCancel != nil {
		m.scopeCancel()
		m.scopeCancel = nil
	}
	m.scopeBusy = false
}

// cancelScopeAct is esc on a busy page: cut the context, void the dispatch, and
// hand the page back immediately.
//
// The page is restored here rather than when the canceled act reports, because
// the case this exists for is precisely the one where it never reports — a
// service that ignores its context, or a connection that is accepted and then
// held open. Bumping the generation is what makes returning early safe.
func (m *Model) cancelScopeAct() {
	m.releaseScopeAct()
	m.scopeGen++
	if m.joinPage != nil {
		m.joinPage.SetLoading(false)
	}
	m.statusMsg = scopeCancelStatus
}
