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

// identitySandbox records one notebook under a temp GROVE_HOME and installs the
// supplied notespace stamps beneath it, returning the notebook root.
func identitySandbox(t *testing.T, machineTOML string, stamps ...notespace.NotespaceStamp) string {
	t.Helper()
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
	for _, stamp := range stamps {
		if _, err := notespace.InstallNotespace(filepath.Join(nb, "notespaces", stamp.Name), stamp); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "machine.toml"), []byte(machineTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	config.ResetLoadCache()
	t.Cleanup(config.ResetLoadCache)
	return nb
}

func TestNotespaceIdentityCheckRejectsDuplicatePhysicalID(t *testing.T) {
	shared := notespace.NotespaceStamp{ID: "01ABCDEFGHJKMNPQRSTVWXYZ02", Subject: "local:01ABCDEFGHJKMNPQRSTVWXYZ03", Kind: "notes"}
	one, two := shared, shared
	one.Name, two.Name = "one", "two"
	identitySandbox(t,
		"[primaries]\n\"local:01ABCDEFGHJKMNPQRSTVWXYZ03\"=\"01ABCDEFGHJKMNPQRSTVWXYZ02\"\n",
		one, two)

	got := (&notespaceIdentityCheck{}).Run(context.Background(), doctor.RunOptions{})
	if got.Status != doctor.StatusFail || !strings.Contains(got.Error, "2 physical roots") {
		t.Fatalf("result=%+v", got)
	}
}

// TestNotespaceIdentityCheckPrimaryInvariants pins the P4 half: siblings are
// legal, and the three ways a recorded primary can be wrong are each named.
func TestNotespaceIdentityCheckPrimaryInvariants(t *testing.T) {
	subject := "local:01ABCDEFGHJKMNPQRSTVWXYZ03"
	other := "local:01ABCDEFGHJKMNPQRSTVWXYZ04"
	primary := notespace.NotespaceStamp{ID: "01ABCDEFGHJKMNPQRSTVWXYZ02", Name: "one", Subject: subject, Kind: "notes"}
	sibling := notespace.NotespaceStamp{ID: "01ABCDEFGHJKMNPQRSTVWXYZ05", Name: "two", Subject: subject, Kind: "notes"}

	cases := []struct {
		name    string
		machine string
		status  doctor.Status
		want    string
	}{
		{
			name:    "siblings are legal",
			machine: "[primaries]\n\"" + subject + "\"=\"" + primary.ID + "\"\n",
			status:  doctor.StatusOK,
			want:    "exactly one primary each",
		},
		{
			name:    "missing",
			machine: "",
			status:  doctor.StatusFail,
			want:    "missing primary for subject " + subject,
		},
		{
			name:    "dangling",
			machine: "[primaries]\n\"" + subject + "\"=\"01ABCDEFGHJKMNPQRSTVWXYZ09\"\n",
			status:  doctor.StatusFail,
			want:    "dangling primary for subject",
		},
		{
			name:    "one notespace claimed by two subjects",
			machine: "[primaries]\n\"" + subject + "\"=\"" + primary.ID + "\"\n\"" + other + "\"=\"" + primary.ID + "\"\n",
			status:  doctor.StatusFail,
			want:    "duplicate primary for subject",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			identitySandbox(t, tc.machine, primary, sibling)
			got := (&notespaceIdentityCheck{}).Run(context.Background(), doctor.RunOptions{})
			if got.Status != tc.status {
				t.Fatalf("status=%s, want %s (%+v)", got.Status, tc.status, got)
			}
			haystack := got.Error + " " + got.Message + " " + got.Resolution
			if !strings.Contains(haystack, tc.want) {
				t.Fatalf("result %+v does not mention %q", got, tc.want)
			}
		})
	}
}
