package cmd

// Provider-neutral building blocks shared by the LOCAL exec-kind satellite
// providers (tart, docker): the per-satellite provider state dir, the
// dedicated ed25519 keypair, and small network waits. Factored out of the
// tart provider (S2) when the docker provider landed (S3); tart's behavior
// is unchanged — its wrappers delegate here with identical semantics.

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// satelliteProviderStateDir is a provider's slice of the per-satellite state
// dir (<StateDir>/satellites/<name>/<provider>): the dedicated ssh keypair,
// run logs, and other provider-local files live here, next to the terraform/
// and bootstrap/ dirs the gcp provider uses.
func satelliteProviderStateDir(satName, provider string) (string, error) {
	dir, err := satelliteStateDir(satName)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, provider), nil
}

// ensureSatelliteProviderKey generates (once) a satellite's dedicated ed25519
// keypair in dir and returns the private key path (0600). comment becomes
// the public key comment (the provider's machine name, for legibility in a
// guest's authorized_keys).
func ensureSatelliteProviderKey(dir, comment string) (string, error) {
	keyPath := filepath.Join(dir, "id_ed25519")
	if _, err := os.Stat(keyPath); err == nil {
		return keyPath, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-C", comment, "-f", keyPath).CombinedOutput() //nolint:gosec // G204: state-dir path
	if err != nil {
		return "", fmt.Errorf("ssh-keygen: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if err := os.Chmod(keyPath, 0o600); err != nil {
		return "", err
	}
	return keyPath, nil
}

// waitForTCPPort polls a TCP connect to addr (host:port) until it succeeds
// or timeout elapses. A canceled ctx returns its error unwrapped so callers
// can distinguish cancellation from a genuine timeout (and keep their own
// timeout message).
func waitForTCPPort(ctx context.Context, addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s: port never opened within %s", addr, timeout)
		}
		time.Sleep(2 * time.Second)
	}
}

// pickFreeLocalhostPort asks the kernel for a free 127.0.0.1 TCP port (bind
// :0, read the port, release). The tiny release-to-bind race is acceptable
// for the local providers' published ports — a lost race fails the machine
// start loudly and a re-run picks a new port.
func pickFreeLocalhostPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		return 0, err
	}
	return port, nil
}
