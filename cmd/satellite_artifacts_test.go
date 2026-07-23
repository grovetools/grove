package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/grovetools/core/pkg/models"
)

func returnBundle(origin, job, name string, data []byte) *models.ArtifactBundle {
	sum := sha256.Sum256(data)
	return &models.ArtifactBundle{
		Manifest: models.ArtifactManifest{SchemaVersion: 1, Origin: origin, JobID: job, TotalBytes: int64(len(data)), Files: []models.ArtifactManifestEntry{{Path: name, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}}},
		Files:    []models.ArtifactFile{{Path: name, Data: data}},
	}
}

func TestWriteArtifactReturnAndIdentitySafeRefetch(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "remote", "sat")
	if err := writeArtifactReturn(dest, returnBundle("sat", "job", "nested/report.md", []byte("one")), "sat", "job"); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(dest, "nested", "report.md")); err != nil || string(got) != "one" {
		t.Fatalf("got %q, %v", got, err)
	}
	if err := writeArtifactReturn(dest, returnBundle("sat", "job", "nested/report.md", []byte("two")), "sat", "job"); err != nil {
		t.Fatal(err)
	}
	if err := writeArtifactReturn(dest, returnBundle("other", "job", "report.md", []byte("x")), "other", "job"); err == nil {
		t.Fatal("cross-origin replacement succeeded")
	}
	if err := writeArtifactReturn(dest, returnBundle("sat", "other", "report.md", []byte("x")), "sat", "other"); err == nil {
		t.Fatal("cross-job replacement succeeded")
	}
}

func TestWriteArtifactReturnRejectsSymlinkParent(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := writeArtifactReturn(filepath.Join(link, "dest"), returnBundle("sat", "job", "report.md", []byte("x")), "sat", "job"); err == nil {
		t.Fatal("symlink parent accepted")
	}
}
