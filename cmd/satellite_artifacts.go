package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/grovetools/core/cli"
	coredaemon "github.com/grovetools/core/pkg/daemon"
	"github.com/grovetools/core/pkg/models"
	"github.com/spf13/cobra"
)

const artifactReturnManifestName = "artifact-return-manifest.json"

func newSatelliteArtifactsCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("artifacts", "Fetch bounded job artifacts from a satellite")
	cmd.AddCommand(newSatelliteArtifactsFetchCmd())
	return cmd
}

func newSatelliteArtifactsFetchCmd() *cobra.Command {
	var dest string
	cmd := cli.NewStandardCommand("fetch <satellite> <job-id>", "Fetch a completed remote job's transcript and artifacts")
	cmd.Args = cobra.ExactArgs(2)
	cmd.SilenceUsage = true
	cmd.Long = `Fetches a bounded, hash-verified artifact manifest over the laptop daemon's
pinned satellite transport. By default files land under
.artifacts/<job-id>/remote/<satellite>. Re-fetch replaces only a directory
whose return manifest has the same origin-qualified job identity.

Live local pane projection remains deferred. For the interactive operator loop,
use "grove satellite ssh <satellite>" and run guest treemux.`
	cmd.Flags().StringVar(&dest, "dest", "", "destination directory (default .artifacts/<job-id>/remote/<satellite>)")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		origin, jobID := args[0], args[1]
		if filepath.Base(origin) != origin || filepath.Base(jobID) != jobID || origin == "." || jobID == "." || strings.ContainsAny(origin+jobID, `/\\`) {
			return fmt.Errorf("satellite and job ID must be single path-safe names")
		}
		if dest == "" {
			dest = filepath.Join(".artifacts", jobID, "remote", origin)
		}
		client := coredaemon.New()
		defer client.Close()
		if !client.IsRunning() {
			return fmt.Errorf("global groved is not running; artifact fetch requires its pinned satellite transport")
		}
		remote, ok := client.(*coredaemon.RemoteClient)
		if !ok {
			return fmt.Errorf("artifact fetch requires the remote daemon client")
		}
		bundle, err := remote.FetchSatelliteArtifacts(cmd.Context(), origin, jobID)
		if err != nil {
			return err
		}
		if err := writeArtifactReturn(dest, bundle, origin, jobID); err != nil {
			return err
		}
		fmt.Printf("Fetched %d files (%d bytes) from %s:%s to %s\n", len(bundle.Manifest.Files), bundle.Manifest.TotalBytes, origin, jobID, dest)
		return nil
	}
	return cmd
}

func writeArtifactReturn(dest string, bundle *models.ArtifactBundle, origin, jobID string) error {
	if err := coredaemon.ValidateArtifactBundle(bundle, origin, jobID); err != nil {
		return err
	}
	dest, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return fmt.Errorf("inspect destination parent: %w", err)
	}
	if parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() {
		return fmt.Errorf("destination parent is not a real directory: %s", parent)
	}

	if info, statErr := os.Lstat(dest); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("existing destination is not a real directory: %s", dest)
		}
		data, readErr := os.ReadFile(filepath.Join(dest, artifactReturnManifestName))
		if readErr != nil {
			return fmt.Errorf("refusing to replace destination without an identity manifest: %s", dest)
		}
		var prior models.ArtifactManifest
		if json.Unmarshal(data, &prior) != nil || prior.Origin != origin || prior.JobID != jobID {
			return fmt.Errorf("refusing cross-origin/job destination replacement at %s", dest)
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}

	stage, err := os.MkdirTemp(parent, ".artifact-return-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := os.Chmod(stage, 0o700); err != nil {
		return err
	}
	for _, file := range bundle.Files {
		path := filepath.Join(stage, filepath.FromSlash(file.Path))
		rel, err := filepath.Rel(stage, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("artifact path escaped staging root: %q", file.Path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, file.Data, 0o600); err != nil {
			return err
		}
	}
	manifestBytes, err := json.MarshalIndent(bundle.Manifest, "", "  ")
	if err != nil {
		return err
	}
	manifestBytes = append(manifestBytes, '\n')
	if err := os.WriteFile(filepath.Join(stage, artifactReturnManifestName), manifestBytes, 0o600); err != nil {
		return err
	}

	backup := dest + ".previous"
	_ = os.RemoveAll(backup)
	if _, err := os.Lstat(dest); err == nil {
		if err := os.Rename(dest, backup); err != nil {
			return err
		}
	}
	if err := os.Rename(stage, dest); err != nil {
		_ = os.Rename(backup, dest)
		return err
	}
	_ = os.RemoveAll(backup)
	return nil
}
