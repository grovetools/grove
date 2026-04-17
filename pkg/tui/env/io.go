// Package env: SSE streaming I/O for the embeddable env panel.
//
// Follows the exact streamLifecycle pattern from hooks/pkg/tui/view/io.go:
// every piece of stream lifecycle state lives on the Model (via a
// *streamLifecycle pointer) so multiple instances can coexist without sharing
// channels or cancel funcs through package globals.
package env

import (
	"context"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/env"
	"github.com/grovetools/grove/pkg/envdrift"
)

// streamLifecycle owns the SSE stream channel + cancel func + waitgroup
// for one Model instance. Referenced by pointer from Model so bubbletea
// value-receiver Update doesn't copy sync primitives.
type streamLifecycle struct {
	mu        sync.Mutex
	cancel    context.CancelFunc
	ch        <-chan daemon.StateUpdate
	wg        sync.WaitGroup
	closeOnce sync.Once
}

func newStreamLifecycle() *streamLifecycle {
	return &streamLifecycle{}
}

// daemonStreamConnectedMsg carries the SSE stream channel + cancel func
// back to the Model so it can store them and start consuming updates.
type daemonStreamConnectedMsg struct {
	ch     <-chan daemon.StateUpdate
	cancel context.CancelFunc
}

// daemonStreamErrorMsg is dispatched when the initial stream subscription
// fails. Non-fatal — panel shows initial fetched state without live updates.
type daemonStreamErrorMsg struct {
	err error
}

// daemonStateUpdateMsg wraps a single SSE update for Update() dispatch.
type daemonStateUpdateMsg struct {
	update daemon.StateUpdate
}

// envStatusFetchedMsg is the result of an async EnvStatus call.
type envStatusFetchedMsg struct {
	worktree string
	state    *env.EnvStateFile
	response *env.EnvResponse
	err      error
}

// envActionResultMsg is the result of an async env action (up/down/restart).
type envActionResultMsg struct {
	action   string
	response *env.EnvResponse
	err      error
}

// subscribeToDaemonCmd opens an SSE stream against the shared daemon
// client and returns a daemonStreamConnectedMsg. Returns nil if the
// client is unavailable so the model degrades gracefully.
func subscribeToDaemonCmd(client daemon.Client) tea.Cmd {
	return func() tea.Msg {
		if client == nil || !client.IsRunning() {
			return nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		ch, err := client.StreamState(ctx)
		if err != nil {
			cancel()
			return daemonStreamErrorMsg{err: err}
		}
		return daemonStreamConnectedMsg{ch: ch, cancel: cancel}
	}
}

// readDaemonStreamCmd pulls one update from the channel and returns it
// as a daemonStateUpdateMsg. wg.Add is called synchronously outside the
// closure so Close()'s Wait() cannot race past an Add that hasn't fired.
func (s *streamLifecycle) readDaemonStreamCmd(ch <-chan daemon.StateUpdate) tea.Cmd {
	if ch == nil || s == nil {
		return nil
	}
	s.wg.Add(1)
	return func() tea.Msg {
		defer s.wg.Done()
		u, ok := <-ch
		if !ok {
			return nil
		}
		return daemonStateUpdateMsg{update: u}
	}
}

// runDriftCmd executes the shared drift engine in a goroutine and delivers
// the result back to the model as a driftCheckFinishedMsg. The drift engine
// does its own terraform init + plan, which takes 10–30s, so callers should
// already be showing a spinner before dispatching this command.
func runDriftCmd(ctx context.Context, profile string) tea.Cmd {
	return func() tea.Msg {
		summary, err := envdrift.RunEnvDrift(ctx, profile)
		return driftCheckFinishedMsg{
			profile: profile,
			summary: summary,
			err:     err,
		}
	}
}

// fetchEnvStatusCmd asynchronously fetches env status for a worktree.
func fetchEnvStatusCmd(client daemon.Client, worktree string) tea.Cmd {
	if client == nil || worktree == "" {
		return nil
	}
	return func() tea.Msg {
		resp, err := client.EnvStatus(context.Background(), worktree)
		return envStatusFetchedMsg{worktree: worktree, response: resp, err: err}
	}
}

// close cancels the stream context and waits for any in-flight read
// goroutine to exit. Idempotent via closeOnce.
func (s *streamLifecycle) close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		s.mu.Lock()
		c := s.cancel
		s.cancel = nil
		s.mu.Unlock()
		if c != nil {
			c()
		}
	})
	s.wg.Wait()
}

// store records the connected channel/cancel for later teardown.
func (s *streamLifecycle) store(ch <-chan daemon.StateUpdate, cancel context.CancelFunc) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ch = ch
	s.cancel = cancel
}
