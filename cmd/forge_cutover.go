package cmd

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/grovetools/core/config"
)

const forgeMeshDialTimeout = 2 * time.Second

const (
	forgeDialSourceMesh     = "mesh"
	forgeDialSourceExternal = "external"
)

var (
	forgeTCPProbe = func(addr string, timeout time.Duration) error {
		conn, err := net.DialTimeout("tcp", addr, timeout)
		if err != nil {
			return err
		}
		return conn.Close()
	}
	forgeHostKeyscan = sshKeyscanHostKey
)

// forgeDialAddr selects the primary SSH route. Mesh is tried first, but a
// direct external route remains a migration fallback while its firewall rule
// exists. IAP is deliberately not automatic: it is the independent operator
// break-glass path, not a hidden dependency of normal convergence.
func forgeDialAddr(cfg *config.ForgeConfig, outputs forgeOutputs) (string, string, error) {
	meshAddr := ""
	if cfg != nil && cfg.Wireguard.IsEnabled() {
		if ip := forgeMeshIP(cfg); ip != "" {
			meshAddr = net.JoinHostPort(ip, "22")
			if err := forgeTCPProbe(meshAddr, forgeMeshDialTimeout); err == nil {
				return meshAddr, forgeDialSourceMesh, nil
			}
		}
	}
	externalAddr := ""
	if strings.TrimSpace(outputs.ExternalIP) != "" {
		externalAddr = net.JoinHostPort(strings.TrimSpace(outputs.ExternalIP), "22")
		if err := forgeTCPProbe(externalAddr, forgeMeshDialTimeout); err == nil {
			return externalAddr, forgeDialSourceExternal, nil
		}
	}
	if meshAddr == "" {
		meshAddr = "not configured"
	}
	if externalAddr == "" {
		externalAddr = "not recorded"
	}
	return "", "", fmt.Errorf("forge SSH is unreachable at both mesh %s and external %s; use the IAP break-glass path: %s", meshAddr, externalAddr, forgeIAPSSHCommand(cfg, outputs))
}

func forgeIAPSSHCommand(cfg *config.ForgeConfig, outputs forgeOutputs) string {
	vmName, zone, project := strings.TrimSpace(outputs.VMName), strings.TrimSpace(outputs.Zone), ""
	if cfg != nil && cfg.Infra != nil {
		if vmName == "" {
			vmName = cfg.Infra.EffectiveVMName()
		}
		if zone == "" {
			zone = cfg.Infra.EffectiveZone()
		}
		project = strings.TrimSpace(cfg.Infra.Project)
	}
	if vmName == "" {
		vmName = "<vm-name>"
	}
	if zone == "" {
		zone = "<zone>"
	}
	cmd := fmt.Sprintf("gcloud compute ssh %s --tunnel-through-iap --zone=%s", vmName, zone)
	if project != "" {
		cmd += " --project=" + project
	}
	return cmd
}

// scanForgeSelectedHostKey pins the selected address and, while both direct
// routes are still open during migration, proves that mesh and external names
// present the same VM key. A disagreement is never accepted as a re-pin.
func scanForgeSelectedHostKey(addr, source string, outputs forgeOutputs) (string, error) {
	selectedKey, err := forgeHostKeyscan(addr)
	if err != nil {
		return "", fmt.Errorf("scan selected %s forge host key at %s: %w", source, addr, err)
	}
	if source != forgeDialSourceMesh || strings.TrimSpace(outputs.ExternalIP) == "" {
		return selectedKey, nil
	}
	externalAddr := net.JoinHostPort(strings.TrimSpace(outputs.ExternalIP), "22")
	if externalAddr == addr || forgeTCPProbe(externalAddr, forgeMeshDialTimeout) != nil {
		return selectedKey, nil
	}
	externalKey, err := forgeHostKeyscan(externalAddr)
	if err != nil {
		return "", fmt.Errorf("scan external forge host key for same-VM verification: %w", err)
	}
	if externalKey != selectedKey {
		return "", fmt.Errorf("forge host-key mismatch: mesh %s and external %s do not identify the same VM; refusing to re-pin", addr, externalAddr)
	}
	return selectedKey, nil
}

func forgeMeshRootURL(cfg *config.ForgeConfig) string {
	if cfg == nil || !cfg.Wireguard.IsEnabled() || cfg.Services == nil || cfg.Services.Forgejo == nil {
		return ""
	}
	ip := forgeMeshIP(cfg)
	if ip == "" {
		return ""
	}
	return "http://" + net.JoinHostPort(ip, strconv.Itoa(cfg.Services.Forgejo.EffectiveHTTPPort())) + "/"
}

func reconcileForgeRootURL(w io.Writer, outputs forgeOutputs, cfg *config.ForgeConfig) error {
	target := forgeMeshRootURL(cfg)
	if target == "" {
		return nil
	}
	ssh, cleanup, err := forgeSSH(outputs, cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	out, err := ssh.outputScript(forgeRootURLReconcileScript(target))
	if err != nil {
		return fmt.Errorf("reconcile Forgejo ROOT_URL: %w", err)
	}
	changed, oldURL, newURL, err := parseForgeRootURLResult(out)
	if err != nil {
		return err
	}
	if changed {
		fmt.Fprintf(w, "\nForgejo ROOT_URL: %s -> %s (forgejo restarted)\n", oldURL, newURL)
	}
	return nil
}

func forgeRootURLReconcileScript(target string) string {
	// target is constructed solely from a parsed IP address and numeric port.
	return `set -euo pipefail
APP=/etc/forgejo/app.ini
TARGET=` + shellQuote(target) + `
OLD=$(sudo awk -F= '/^[[:space:]]*ROOT_URL[[:space:]]*=/{v=$0; sub(/^[^=]*=[[:space:]]*/, "", v)} END{print v}' "$APP")
if [ "$OLD" = "$TARGET" ]; then
  printf 'CHANGED=0\nOLD=%s\nNEW=%s\n' "$OLD" "$TARGET"
  exit 0
fi
TMP=$(mktemp)
sudo awk -v target="$TARGET" '
  /^[[:space:]]*ROOT_URL[[:space:]]*=/ { print "ROOT_URL = " target; found=1; next }
  { print }
  END { if (!found) print "\n[server]\nROOT_URL = " target }
' "$APP" > "$TMP"
# The service user is a module variable (default "git"), so ownership is
# whatever the installer chose: preserve it rather than guess a group name.
OWNER=$(sudo stat -c '%U' "$APP")
GROUP=$(sudo stat -c '%G' "$APP")
MODE=$(sudo stat -c '%a' "$APP")
sudo install -o "$OWNER" -g "$GROUP" -m "$MODE" "$TMP" "$APP"
rm -f "$TMP"
sudo systemctl restart forgejo.service
printf 'CHANGED=1\nOLD=%s\nNEW=%s\n' "${OLD:-<unset>}" "$TARGET"
`
}

func parseForgeRootURLResult(out string) (bool, string, string, error) {
	values := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			values[key] = value
		}
	}
	if values["CHANGED"] != "0" && values["CHANGED"] != "1" {
		return false, "", "", fmt.Errorf("parse Forgejo ROOT_URL reconcile output: missing CHANGED trailer")
	}
	if values["NEW"] == "" {
		return false, "", "", fmt.Errorf("parse Forgejo ROOT_URL reconcile output: missing NEW trailer")
	}
	return values["CHANGED"] == "1", values["OLD"], values["NEW"], nil
}
