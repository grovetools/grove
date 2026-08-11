package doctorchecks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/doctor"
	"github.com/grovetools/core/pkg/notespace"
)

func TestNotespaceIdentityCheckRejectsDuplicatePhysicalID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GROVE_HOME", home)
	dir := filepath.Join(home, "config", "grove")
	nb := filepath.Join(home, "notebook")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notebooks.toml"), []byte("default=\"nb\"\n[notebooks.nb]\nroot=\""+nb+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "roots.toml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := notespace.InstallNotebook(nb, notespace.NotebookStamp{ID: "01ABCDEFGHJKMNPQRSTVWXYZ01", Name: "nb"}); err != nil {
		t.Fatal(err)
	}
	stamp := notespace.NotespaceStamp{ID: "01ABCDEFGHJKMNPQRSTVWXYZ02", Name: "one", Subject: "local:01ABCDEFGHJKMNPQRSTVWXYZ03", Kind: "notes"}
	for _, name := range []string{"one", "two"} {
		root := filepath.Join(nb, "notespaces", name)
		stamp.Name = name
		if _, err := notespace.InstallNotespace(root, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "machine.toml"), []byte("[primaries]\n\"local:01ABCDEFGHJKMNPQRSTVWXYZ03\"=\"01ABCDEFGHJKMNPQRSTVWXYZ02\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	config.ResetLoadCache()
	t.Cleanup(config.ResetLoadCache)
	got := (&notespaceIdentityCheck{}).Run(context.Background(), doctor.RunOptions{})
	if got.Status != doctor.StatusFail || !strings.Contains(got.Error, "2 physical roots") {
		t.Fatalf("result=%+v", got)
	}
}
