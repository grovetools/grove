package cmd

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/grovetools/grove/cmd/satelliteassets"
)

// stubDockerOnPath puts a fake `docker` binary with the given script body
// (after the shebang) at the front of PATH, so provider tests never need a
// real CLI or daemon (mirrors stubTartOnPath).
func stubDockerOnPath(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

// TestDockerProviderRegistration pins the registry resolution and axis
// defaults: "docker" resolves to the docker provider — exec-only default, no
// bootstrap script — and the container naming / provider_ref scheme the
// restart-vs-collision check depends on.
func TestDockerProviderRegistration(t *testing.T) {
	p, err := satelliteProviderFor("docker")
	if err != nil {
		t.Fatalf("satelliteProviderFor(docker): %v", err)
	}
	if p.Kind() != "docker" {
		t.Errorf("docker Kind() = %q, want docker", p.Kind())
	}
	if p.DefaultSatelliteKind() != satelliteKindExec {
		t.Errorf("docker DefaultSatelliteKind() = %q, want %q", p.DefaultSatelliteKind(), satelliteKindExec)
	}
	if p.UsesBootstrapScript(satelliteKindExec) {
		t.Error("docker UsesBootstrapScript() = true, want false")
	}

	if got := dockerContainerName("mysat"); got != "grove-sat-mysat" {
		t.Errorf("dockerContainerName = %q, want grove-sat-mysat", got)
	}
	if got := dockerProviderRef(dockerContainerName("mysat")); got != "docker:grove-sat-mysat" {
		t.Errorf("dockerProviderRef = %q, want docker:grove-sat-mysat", got)
	}
}

// TestDockerPrepareUpPreflight pins the preflight behavior: a missing docker
// binary and an unreachable daemon each yield an actionable error; with a
// reachable (stub) daemon the image resolves to the grove-owned content-hash
// tag or the infra override.
func TestDockerPrepareUpPreflight(t *testing.T) {
	// Missing binary → actionable error.
	t.Setenv("PATH", t.TempDir()) // empty dir: no docker
	p := &dockerSatelliteProvider{target: dockerSatelliteTarget}
	err := p.PrepareUp(&satelliteUpOptions{Name: "x"})
	if err == nil || !strings.Contains(err.Error(), "docker not found on PATH") {
		t.Fatalf("missing-docker preflight error = %v, want install suggestion", err)
	}

	// Daemon unreachable (stub `docker info` fails, like this machine's real
	// daemonless docker CLI) → actionable error. Unit-level so the test never
	// depends on the machine's actual daemon state.
	stubDockerOnPath(t, `echo "Cannot connect to the Docker daemon at unix:///var/run/docker.sock" >&2
exit 1
`)
	p = &dockerSatelliteProvider{target: dockerSatelliteTarget}
	err = p.PrepareUp(&satelliteUpOptions{Name: "x"})
	if err == nil || !strings.Contains(err.Error(), "docker daemon unreachable") {
		t.Fatalf("daemon-unreachable preflight error = %v, want the start-the-daemon message", err)
	}

	// Daemon reachable → the grove-owned content-hash tag from the embedded
	// build context.
	stubDockerOnPath(t, "echo 27.0.1\n")
	p = &dockerSatelliteProvider{target: dockerSatelliteTarget}
	if err := p.PrepareUp(&satelliteUpOptions{Name: "x"}); err != nil {
		t.Fatalf("PrepareUp with stub docker: %v", err)
	}
	files, err := satelliteassets.DockerBuildContext()
	if err != nil {
		t.Fatalf("DockerBuildContext: %v", err)
	}
	if want := dockerSatelliteImageTag(files); p.image != want {
		t.Errorf("default image = %q, want the embedded-context tag %q", p.image, want)
	}
	if p.imageIsOverride {
		t.Error("imageIsOverride = true for the grove-owned image")
	}

	// Infra image override wins and skips the grove-owned build path.
	p = &dockerSatelliteProvider{target: dockerSatelliteTarget}
	if err := p.PrepareUp(&satelliteUpOptions{Name: "x", Infra: satelliteInfraConfig{Image: "example.com/me/sshd:1"}}); err != nil {
		t.Fatalf("PrepareUp with image override: %v", err)
	}
	if p.image != "example.com/me/sshd:1" || !p.imageIsOverride {
		t.Errorf("image override = (%q, override=%v), want the infra value with override=true", p.image, p.imageIsOverride)
	}

	// Up without PrepareUp is a hard error.
	if _, err := (&dockerSatelliteProvider{target: dockerSatelliteTarget}).Up(t.Context(), &satelliteUpOptions{Name: "x"}); err == nil || !strings.Contains(err.Error(), "without PrepareUp") {
		t.Errorf("Up without PrepareUp = %v, want the guard error", err)
	}
}

// TestDockerDefaultPrebuiltTarget pins the provider hook the shared verb uses
// for the implied --prebuilt-target: the daemon's Os/Arch pair passes
// through, garbage is rejected with the flag remediation, and a dead daemon
// errors actionably.
func TestDockerDefaultPrebuiltTarget(t *testing.T) {
	p := &dockerSatelliteProvider{target: dockerSatelliteTarget}

	stubDockerOnPath(t, "echo linux/amd64\n")
	got, err := p.DefaultPrebuiltTarget()
	if err != nil || got != "linux/amd64" {
		t.Fatalf("DefaultPrebuiltTarget = (%q, %v), want (linux/amd64, nil)", got, err)
	}

	stubDockerOnPath(t, "echo oops\n")
	if _, err := p.DefaultPrebuiltTarget(); err == nil || !strings.Contains(err.Error(), "--prebuilt-target") {
		t.Errorf("garbage platform error = %v, want the explicit-flag remediation", err)
	}

	stubDockerOnPath(t, "exit 1\n")
	if _, err := p.DefaultPrebuiltTarget(); err == nil || !strings.Contains(err.Error(), "daemon") {
		t.Errorf("dead-daemon error = %v, want a daemon mention", err)
	}
}

// TestDockerSatelliteImageTag pins the tag derivation: deterministic,
// grove-satellite:<12 hex>, and content-sensitive (an asset edit rebuilds).
func TestDockerSatelliteImageTag(t *testing.T) {
	files := map[string][]byte{"Dockerfile": []byte("FROM x\n"), "entrypoint.sh": []byte("#!/bin/sh\n")}
	tag := dockerSatelliteImageTag(files)
	if !regexp.MustCompile(`^grove-satellite:[0-9a-f]{12}$`).MatchString(tag) {
		t.Fatalf("tag %q does not match grove-satellite:<12 hex>", tag)
	}
	if again := dockerSatelliteImageTag(map[string][]byte{"entrypoint.sh": []byte("#!/bin/sh\n"), "Dockerfile": []byte("FROM x\n")}); again != tag {
		t.Errorf("tag not deterministic across map order: %q vs %q", tag, again)
	}
	if changed := dockerSatelliteImageTag(map[string][]byte{"Dockerfile": []byte("FROM y\n"), "entrypoint.sh": []byte("#!/bin/sh\n")}); changed == tag {
		t.Error("tag unchanged after a content edit — asset changes would not rebuild")
	}
}

// TestPickFreeLocalhostPort pins the free-port allocation: a valid loopback
// port that is actually bindable right after.
func TestPickFreeLocalhostPort(t *testing.T) {
	port, err := pickFreeLocalhostPort()
	if err != nil {
		t.Fatalf("pickFreeLocalhostPort: %v", err)
	}
	if port <= 0 || port > 65535 {
		t.Fatalf("port %d out of range", port)
	}
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("returned port %d not bindable: %v", port, err)
	}
	_ = l.Close()
}

// TestDockerCreateArgs pins the exact `docker create` argv Up executes — the
// dry-run view of the container shape: named, no restart policy, sshd
// published loopback-only, the authorized_keys as a read-only bind mount
// (never an env var — it must stay out of `docker inspect`).
func TestDockerCreateArgs(t *testing.T) {
	got := dockerCreateArgs("grove-sat-mysat", 55007, "/state/mysat/docker/authorized_keys", "grove-satellite:abc123def456")
	want := []string{
		"create",
		"--name", "grove-sat-mysat",
		"--restart", "no",
		"-p", "127.0.0.1:55007:22",
		"-v", "/state/mysat/docker/authorized_keys:" + dockerAuthorizedKeysMount + ":ro",
		"grove-satellite:abc123def456",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dockerCreateArgs =\n  %q\nwant\n  %q", got, want)
	}
	t.Logf("docker %s", strings.Join(got, " "))
}

// TestDockerAssetsEmbedded pins the embedded build context through the assets
// API: both files present with the load-bearing directives (hardened sshd,
// non-root user, entrypoint consuming the provider's mount path) — and that
// the docker context does NOT leak into the terraform target enumeration.
func TestDockerAssetsEmbedded(t *testing.T) {
	files, err := satelliteassets.DockerBuildContext()
	if err != nil {
		t.Fatalf("DockerBuildContext: %v", err)
	}

	df, ok := files["Dockerfile"]
	if !ok {
		t.Fatal("embedded context has no Dockerfile")
	}
	for _, want := range []string{
		"FROM debian:bookworm-slim",
		"openssh-server",
		"openssh-sftp-server", // scp speaks SFTP on modern clients
		"useradd --create-home --uid 1000 --shell /bin/bash grove",
		"PasswordAuthentication no",
		"PermitRootLogin no",
		"ENTRYPOINT",
	} {
		if !strings.Contains(string(df), want) {
			t.Errorf("Dockerfile missing %q", want)
		}
	}

	ep, ok := files["entrypoint.sh"]
	if !ok {
		t.Fatal("embedded context has no entrypoint.sh")
	}
	if !strings.HasPrefix(string(ep), "#!") {
		t.Error("entrypoint.sh does not start with a shebang")
	}
	for _, want := range []string{
		"ssh-keygen -A",
		dockerAuthorizedKeysMount, // provider mount path and entrypoint agree
		"exec /usr/sbin/sshd -D -e",
	} {
		if !strings.Contains(string(ep), want) {
			t.Errorf("entrypoint.sh missing %q", want)
		}
	}

	// The docker build context lives under targets/ but must not surface as a
	// terraform target (Targets backs resolveSatelliteTarget/TerraformFS).
	for _, target := range satelliteassets.Targets() {
		if target == "docker" {
			t.Error("Targets() lists docker — the build context leaked into terraform-target enumeration")
		}
	}
}

// TestMergeSatelliteEntriesProviderRefDocker pins the provider_ref merge for
// docker refs: machine-derived, so the state value wins over a stale config
// view (same rule the tart test pins for tart refs).
func TestMergeSatelliteEntriesProviderRefDocker(t *testing.T) {
	merged, _ := mergeSatelliteEntries(
		map[string]satelliteConfigEntry{"sat": {User: "grove", ProviderRef: "docker:stale"}},
		map[string]satelliteConfigEntry{"sat": {SSHAddr: "127.0.0.1:55007", ProviderRef: "docker:grove-sat-sat"}},
	)
	if got := merged["sat"].ProviderRef; got != "docker:grove-sat-sat" {
		t.Errorf("state provider_ref must win: %q", got)
	}
	if got := merged["sat"].SSHAddr; got != "127.0.0.1:55007" {
		t.Errorf("state ssh_addr (loopback:port endpoint shape) must win: %q", got)
	}
}
