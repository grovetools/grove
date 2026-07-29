package plugin

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestLockRoundTrip(t *testing.T) {
	isolate(t)

	lock, err := LoadLock()
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}
	if len(lock.Plugins) != 0 {
		t.Fatalf("a missing lockfile must load as empty, got %+v", lock.Plugins)
	}

	lock.Set("demo", &Pin{Spec: "github.com/u/r@v1.0.0", URL: "https://github.com/u/r", Ref: "v1.0.0", Commit: "abc123"}, time.Unix(1700000000, 0))
	if err := lock.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := LoadLock()
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}
	pin := reloaded.Get("demo")
	if pin == nil || pin.Commit != "abc123" || pin.Ref != "v1.0.0" {
		t.Fatalf("pin did not survive the round trip: %+v", pin)
	}
	if pin.InstalledAt != "2023-11-14T22:13:20Z" {
		t.Errorf("installed_at = %q", pin.InstalledAt)
	}
	if names := reloaded.Names(); len(names) != 1 || names[0] != "demo" {
		t.Errorf("Names = %v", names)
	}

	if !reloaded.Remove("demo") {
		t.Error("Remove must report that it dropped the pin")
	}
	if reloaded.Remove("demo") {
		t.Error("Remove must report false for a pin it does not hold")
	}
}

// The lockfile must not live where core/config globs for plugin manifests, or
// grove would try to merge its own pin file as configuration.
func TestLockfileIsNotGlobbedAsAConfigFragment(t *testing.T) {
	isolate(t)

	path, err := LockPath()
	if err != nil {
		t.Fatalf("LockPath: %v", err)
	}
	if strings.HasSuffix(path, ".toml") {
		t.Errorf("lockfile %s would be picked up by the ~/.config/grove/plugins/*.toml glob", path)
	}
	dir, err := ConfigPluginsDir()
	if err != nil {
		t.Fatalf("ConfigPluginsDir: %v", err)
	}
	if !strings.HasPrefix(path, dir) {
		t.Errorf("lockfile %s should live under the user config dir %s", path, dir)
	}
}

// A lockfile from a newer grove is refused rather than rewritten: rewriting it
// would drop pins this binary cannot represent.
func TestLockFromANewerGroveIsRefused(t *testing.T) {
	isolate(t)

	path, err := LockPath()
	if err != nil {
		t.Fatalf("LockPath: %v", err)
	}
	lock := &Lock{}
	if err := lock.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"version":99,"plugins":{}}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := LoadLock(); err == nil {
		t.Error("a future lockfile version must be an error, not a silent downgrade")
	}
}

func TestUsesSourceDirIgnoresThePluginBeingRemoved(t *testing.T) {
	lock := &Lock{Plugins: map[string]*Pin{
		"a": {SourceDir: "/src/shared"},
		"b": {SourceDir: "/src/shared"},
	}}
	if !lock.UsesSourceDir("/src/shared", "a") {
		t.Error("b still uses the checkout, so removing a must not delete it")
	}
	delete(lock.Plugins, "b")
	if lock.UsesSourceDir("/src/shared", "a") {
		t.Error("nothing else uses the checkout, so removing a may delete it")
	}
}
