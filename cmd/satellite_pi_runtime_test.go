package cmd

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writePiFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for path, content := range map[string]string{
		"agent/package/package.json":    `{"name":"@grovetools/grove-pi","version":"0.1.0"}`,
		"agent/package/extensions/a.ts": "export default () => {};\n",
		"agent/tools/pi-codex-auth.mjs": "#!/usr/bin/env node\n",
	} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestBuildPiRuntimeBundleIsContentAddressedAndDeterministic(t *testing.T) {
	root := writePiFixture(t)
	one, err := buildPiRuntimeBundle(root, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	two, err := buildPiRuntimeBundle(root, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if one.ManifestHash != two.ManifestHash || one.ArchiveHash != two.ArchiveHash {
		t.Fatalf("bundle is not deterministic: %#v %#v", one, two)
	}
	if len(one.ManifestHash) != 64 || len(one.ArchiveHash) != 64 {
		t.Fatal("hashes must be sha256")
	}
	f, err := os.Open(one.ArchivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tr := tar.NewReader(f)
	names := []string{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	if strings.Join(names, ",") != "manifest.json,bin/pi-codex-auth.mjs,package/extensions/a.ts,package/package.json" {
		t.Fatalf("archive order/content = %v", names)
	}

	path := filepath.Join(root, "agent/package/extensions/a.ts")
	if err := os.WriteFile(path, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	three, err := buildPiRuntimeBundle(root, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if three.ManifestHash == one.ManifestHash {
		t.Fatal("content change did not change address")
	}
}

func TestPiRuntimeInstallScriptPinsAndActivatesSafely(t *testing.T) {
	b := piRuntimeBundle{ManifestHash: strings.Repeat("a", 64), ArchiveHash: strings.Repeat("b", 64)}
	script := renderPiRuntimeInstallScript("/tmp/grove-pi.tar", b)
	for _, want := range []string{
		stockPiPackage + "@" + stockPiVersion,
		stockPiIntegrity,
		"sha256/$manifest_hash",
		"settings+'.staged-'",
		"settings+'.previous'",
		"if(fs.existsSync(settings)){ fs.copyFileSync(settings,settings+'.previous'); fs.chmodSync(settings+'.previous',0o600); }",
		"trap cleanup_runtime_install EXIT",
		"cp \"$settings.previous\" \"$settings\"",
		"GROVE_PI_HEALTH_FILE",
		"extensions-loaded.json",
		"auth_path",
		"support_exclusions",
		"auth.json.lock",
		"isolation_boundary\":\"tart-vm",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("install script missing %q", want)
		}
	}
	for _, forbidden := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "CODEX_ACCESS_TOKEN", "OPENAI_API_KEY", "auth.json.staged"} {
		if strings.Contains(script, forbidden) {
			t.Errorf("install script contains forbidden auth representation %q", forbidden)
		}
	}
	if strings.Count(script, stockPiIntegrity) != 1 {
		t.Fatal("integrity pin should be compared exactly once")
	}
	check := exec.Command("bash", "-n")
	check.Stdin = strings.NewReader(script)
	if out, err := check.CombinedOutput(); err != nil {
		t.Fatalf("install script syntax: %v: %s", err, out)
	}
}

func TestManifestHashMatchesManifestBytes(t *testing.T) {
	bundle, err := buildPiRuntimeBundle(writePiFixture(t), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(bundle.ArchivePath)
	defer f.Close()
	tr := tar.NewReader(f)
	h, err := tr.Next()
	if err != nil || h.Name != "manifest.json" {
		t.Fatalf("manifest first: %v %v", h, err)
	}
	data, err := io.ReadAll(tr)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if !bytes.Equal(sum[:], mustHex(t, bundle.ManifestHash)) {
		t.Fatal("manifest address mismatch")
	}
}

func mustHex(t *testing.T, value string) []byte {
	t.Helper()
	b, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
