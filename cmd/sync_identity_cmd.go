package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/grovetools/core/pkg/devicekey"
	"github.com/grovetools/core/pkg/paths"
	"github.com/grovetools/core/pkg/syncproto"
)

type syncConflictWire struct {
	NotespaceID, NotespaceName, Path, DocumentID, Kind, Artifact, ArtifactContent, BaseContent string
}

func newSyncConflictsCmd() *cobra.Command {
	var id string
	cmd := &cobra.Command{Use: "conflicts", Short: "List artifact-backed sync conflicts, including identity registration conflicts", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		conflicts, err := fetchSyncConflicts(cmd.Context(), id)
		if err != nil {
			return err
		}
		if len(conflicts) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No sync conflicts.")
			return nil
		}
		sort.Slice(conflicts, func(i, j int) bool {
			if conflicts[i].NotespaceID != conflicts[j].NotespaceID {
				return conflicts[i].NotespaceID < conflicts[j].NotespaceID
			}
			return conflicts[i].Artifact < conflicts[j].Artifact
		})
		for _, conflict := range conflicts {
			kind := conflict.Kind
			if kind == "" {
				kind = "merge"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s  %-14s  %s  %s\n", conflict.NotespaceID, kind, conflict.Path, conflict.Artifact)
		}
		return nil
	}}
	cmd.Flags().StringVar(&id, "notespace-id", "", "Filter by immutable notespace id")
	return cmd
}

func newSyncAdoptIDCmd() *cobra.Command {
	var survivor, loser, disposition string
	var version int64
	cmd := &cobra.Command{Use: "adopt-id <conflict-id>", Short: "Resolve a registration conflict by choosing the surviving immutable id", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if survivor == "" || loser == "" || version <= 0 {
			return fmt.Errorf("--survivor, --loser and positive --version are required; no id is inferred")
		}
		client, err := loadDeviceSessionHTTP(cmd.Context())
		if err != nil {
			return err
		}
		key, err := devicekey.Load()
		if err != nil {
			return err
		}
		req := syncproto.RegistrationResolutionRequest{RequestIdentity: syncproto.RequestIdentity{ProtocolVersion: syncproto.ProtocolVersionNotespaceID, IdempotencyKey: "adopt-id-" + args[0] + fmt.Sprintf("-%d", version), DeviceID: key.DeviceID()}, ConflictID: args[0], SurvivorNotespaceID: syncproto.NotespaceID(survivor), LoserNotespaceID: syncproto.NotespaceID(loser), LoserDisposition: disposition, ExpectedVersion: version}
		if wire := req.Validate(); wire != nil {
			return fmt.Errorf("invalid adoption request: %s", wire.Message)
		}
		var response syncproto.RegistrationResolutionResponse
		if err := client.doJSON(cmd.Context(), http.MethodPost, "/sync/registration/resolve", req, &response); err != nil {
			return err
		}
		if response.Error != nil {
			return fmt.Errorf("resolution rejected: %s: %s", response.Error.Code, response.Error.Message)
		}
		if response.InheritedNotespaceID != req.SurvivorNotespaceID {
			return fmt.Errorf("server did not bind the requested survivor: %+v", response)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "✓ subject %s now inherits %s at claim version %d; loser %s is retained as %s\n", response.Subject, response.InheritedNotespaceID, response.Version, loser, disposition)
		return nil
	}}
	cmd.Flags().StringVar(&survivor, "survivor", "", "Notespace id that keeps the subject claim")
	cmd.Flags().StringVar(&loser, "loser", "", "Conflicting notespace id")
	cmd.Flags().StringVar(&disposition, "loser-disposition", syncproto.RegistrationIntentCreateSibling, "Loser disposition (create-sibling or reconcile)")
	cmd.Flags().Int64Var(&version, "version", 0, "Expected conflict claim version (stale decisions are rejected)")
	return cmd
}

func fetchSyncConflicts(ctx context.Context, id string) ([]syncConflictWire, error) {
	socket := paths.SocketPath()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	client := &http.Client{Transport: transport}
	endpoint := "http://groved/api/sync/conflicts"
	if id != "" {
		endpoint += "?notespace_id=" + url.QueryEscape(id)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query global daemon conflicts: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon conflicts returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	// Explicit tags avoid relying on Go's case-folding for the id/name cutover.
	var raw []struct {
		NotespaceID     string `json:"notespace_id"`
		NotespaceName   string `json:"notespace_name"`
		Path            string `json:"path"`
		DocumentID      string `json:"document_id"`
		Kind            string `json:"kind"`
		Artifact        string `json:"artifact"`
		ArtifactContent string `json:"artifact_content"`
		BaseContent     string `json:"base_content"`
	}
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]syncConflictWire, len(raw))
	for i, c := range raw {
		out[i] = syncConflictWire{c.NotespaceID, c.NotespaceName, c.Path, c.DocumentID, c.Kind, c.Artifact, c.ArtifactContent, c.BaseContent}
	}
	return out, nil
}
