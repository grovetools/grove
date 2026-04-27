package orchestrator

import (
	"context"
	"path/filepath"

	"github.com/grovetools/core/git"
	"github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/models"
)

type WorkspaceState struct {
	IsDirty     bool
	CommitHash  string
	TaskResults map[string]*models.TaskResult
}

type StateProvider interface {
	GetState(ctx context.Context, workspaces []string) (map[string]WorkspaceState, error)
}

type DaemonStateProvider struct {
	Client daemon.Client
}

func (d *DaemonStateProvider) GetState(ctx context.Context, workspaces []string) (map[string]WorkspaceState, error) {
	enriched, err := d.Client.GetEnrichedWorkspaces(ctx, &models.EnrichmentOptions{
		FetchGitStatus: true,
	})
	if err != nil {
		return nil, err
	}

	wsSet := make(map[string]bool, len(workspaces))
	for _, ws := range workspaces {
		wsSet[ws] = true
	}

	states := make(map[string]WorkspaceState, len(workspaces))
	for _, ew := range enriched {
		if ew.WorkspaceNode == nil || !wsSet[ew.Path] {
			continue
		}
		name := filepath.Base(ew.Path)
		s := WorkspaceState{
			TaskResults: ew.TaskResults,
		}
		if ew.GitStatus != nil && ew.GitStatus.StatusInfo != nil {
			s.IsDirty = ew.GitStatus.IsDirty
		}
		if hash, err := git.GetHeadCommit(ew.Path); err == nil {
			s.CommitHash = hash
		}
		states[name] = s
	}
	return states, nil
}

type LocalStateProvider struct{}

func (l *LocalStateProvider) GetState(ctx context.Context, workspaces []string) (map[string]WorkspaceState, error) {
	states := make(map[string]WorkspaceState, len(workspaces))
	for _, wsPath := range workspaces {
		name := filepath.Base(wsPath)
		s := WorkspaceState{}
		if status, err := git.GetExtendedStatus(wsPath); err == nil && status.StatusInfo != nil {
			s.IsDirty = status.IsDirty
		}
		if hash, err := git.GetHeadCommit(wsPath); err == nil {
			s.CommitHash = hash
		}
		states[name] = s
	}
	return states, nil
}
