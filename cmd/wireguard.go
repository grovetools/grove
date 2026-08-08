package cmd

import (
	"encoding/base64"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/grovetools/core/config"
)

const wireGuardPrivateKeyPlaceholder = "__GROVE_WG_PRIVATE_KEY__"

type wireGuardPaths struct {
	StateDir      string
	PrivateKey    string
	Config        string
	InterfaceName string
}

func defaultWireGuardPaths(stateDir string) wireGuardPaths {
	return wireGuardPaths{
		StateDir:      stateDir,
		PrivateKey:    "/etc/wireguard/wg0.key",
		Config:        "/etc/wireguard/wg0.conf",
		InterfaceName: "wg0",
	}
}

type wgStatus struct {
	Pubkey          string
	HandshakeAge    int64 // -1 means no handshake has occurred
	ConfChanged     bool
	Endpoint        string
	TransferRXBytes uint64
	TransferTXBytes uint64
	ConfPresent     bool
	ServiceEnabled  bool
	InterfaceActive bool
}

// renderWG0Conf renders only public configuration. privateKeyPlaceholder must
// be a marker, never key material; the converge script replaces it by reading
// the root-only key on the VM.
func renderWG0Conf(cfg config.WireGuardConfig, privateKeyPlaceholder string) string {
	var b strings.Builder
	b.WriteString("[Interface]\n")
	fmt.Fprintf(&b, "Address = %s\n", strings.TrimSpace(cfg.Address))
	fmt.Fprintf(&b, "PrivateKey = %s\n", privateKeyPlaceholder)
	if dns := strings.TrimSpace(cfg.DNS); dns != "" {
		fmt.Fprintf(&b, "DNS = %s\n", dns)
	}
	b.WriteString("\n[Peer]\n")
	fmt.Fprintf(&b, "PublicKey = %s\n", strings.TrimSpace(cfg.HubPublicKey))
	fmt.Fprintf(&b, "Endpoint = %s\n", strings.TrimSpace(cfg.Endpoint))
	fmt.Fprintf(&b, "AllowedIPs = %s\n", strings.Join(cfg.AllowedIPsOrDefault(), ", "))
	fmt.Fprintf(&b, "PersistentKeepalive = %d\n", cfg.EffectivePersistentKeepalive())
	return b.String()
}

// wireGuardConvergeScript is public-facts-only shell. The private key is born
// and consumed on the remote machine, and is never interpolated into this text.
func wireGuardConvergeScript(cfg config.WireGuardConfig, paths wireGuardPaths) string {
	conf := renderWG0Conf(cfg, wireGuardPrivateKeyPlaceholder)
	encodedConf := base64.StdEncoding.EncodeToString([]byte(conf))
	endpointHost, _, _ := net.SplitHostPort(strings.TrimSpace(cfg.Endpoint))
	endpointHost = strings.Trim(endpointHost, "[]")
	isIP := net.ParseIP(endpointHost) != nil
	pubkey := strings.TrimSpace(cfg.HubPublicKey)
	endpoint := strings.TrimSpace(cfg.Endpoint)

	lines := []string{
		"set -euo pipefail",
		"command -v wg >/dev/null 2>&1 || sudo apt-get install -y wireguard-tools",
		"sudo install -d -m 0755 " + shellQuote(paths.StateDir),
		"sudo install -d -m 0700 /etc/wireguard",
		"if ! sudo test -f " + shellQuote(paths.PrivateKey) + "; then",
		"  sudo sh -c 'umask 077; wg genkey > \"$1\"' sh " + shellQuote(paths.PrivateKey),
		"fi",
		"sudo sh -c 'wg pubkey < \"$1\" > \"$2\"' sh " + shellQuote(paths.PrivateKey) + " " + shellQuote(paths.StateDir+"/wg-public-key.txt"),
		"sudo chmod 0644 " + shellQuote(paths.StateDir+"/wg-public-key.txt"),
		"TEMPLATE=$(mktemp)",
		"trap 'rm -f \"$TEMPLATE\"; if [ -n \"${NEW_CONF:-}\" ]; then sudo rm -f \"$NEW_CONF\"; fi' EXIT",
		"printf '%s' " + shellQuote(encodedConf) + " | base64 -d > \"$TEMPLATE\"",
		"NEW_CONF=$(sudo sh -c 'umask 077; mktemp /etc/wireguard/.wg0.conf.XXXXXX')",
		"sudo awk -v placeholder=" + shellQuote(wireGuardPrivateKeyPlaceholder) + " -v keyfile=" + shellQuote(paths.PrivateKey) + " '",
		"  $0 == \"PrivateKey = \" placeholder { if ((getline key < keyfile) <= 0) exit 40; print \"PrivateKey = \" key; close(keyfile); next }",
		"  { print }",
		"' \"$TEMPLATE\" | sudo tee \"$NEW_CONF\" >/dev/null",
		"sudo chmod 0600 \"$NEW_CONF\"",
		"CONF_CHANGED=0",
		"if ! sudo test -f " + shellQuote(paths.Config) + " || ! sudo cmp -s \"$NEW_CONF\" " + shellQuote(paths.Config) + "; then",
		"  sudo install -o root -g root -m 0600 \"$NEW_CONF\" " + shellQuote(paths.Config),
		"  CONF_CHANGED=1",
		"fi",
		"sudo systemctl enable wg-quick@" + paths.InterfaceName + ".service >/dev/null",
		"if sudo systemctl is-active --quiet wg-quick@" + paths.InterfaceName + ".service; then",
		"  if [ \"$CONF_CHANGED\" = 1 ]; then sudo systemctl restart wg-quick@" + paths.InterfaceName + ".service; fi",
		"else",
		"  sudo systemctl start wg-quick@" + paths.InterfaceName + ".service",
		"fi",
	}
	if !isIP {
		reresolve := strings.Join([]string{
			"#!/bin/sh",
			"set -eu",
			"exec wg set " + paths.InterfaceName + " peer " + shellQuote(pubkey) + " endpoint " + shellQuote(endpoint),
		}, "\n") + "\n"
		service := "[Unit]\nDescription=Re-resolve the Grove WireGuard hub endpoint\nAfter=network-online.target wg-quick@" + paths.InterfaceName + ".service\n\n[Service]\nType=oneshot\nExecStart=/usr/local/sbin/grove-wg-reresolve\n"
		timer := "[Unit]\nDescription=Periodically re-resolve the Grove WireGuard hub endpoint\n\n[Timer]\nOnBootSec=60s\nOnUnitActiveSec=60s\nUnit=grove-wg-reresolve.service\n\n[Install]\nWantedBy=timers.target\n"
		lines = append(lines,
			"printf '%s' "+shellQuote(reresolve)+" | sudo install -o root -g root -m 0755 /dev/stdin /usr/local/sbin/grove-wg-reresolve",
			"printf '%s' "+shellQuote(service)+" | sudo install -o root -g root -m 0644 /dev/stdin /etc/systemd/system/grove-wg-reresolve.service",
			"printf '%s' "+shellQuote(timer)+" | sudo install -o root -g root -m 0644 /dev/stdin /etc/systemd/system/grove-wg-reresolve.timer",
			"sudo systemctl daemon-reload",
			"sudo systemctl enable --now grove-wg-reresolve.timer >/dev/null",
		)
	}
	lines = append(lines,
		"NOW=$(date +%s)",
		"LATEST=$(sudo wg show "+paths.InterfaceName+" latest-handshakes | awk 'NR==1 {print $2}')",
		"if [ -z \"${LATEST:-}\" ] || [ \"$LATEST\" = 0 ]; then HANDSHAKE_AGE_S=never; else HANDSHAKE_AGE_S=$((NOW-LATEST)); [ \"$HANDSHAKE_AGE_S\" -lt 0 ] && HANDSHAKE_AGE_S=0; fi",
		"printf 'PUBKEY=%s\\n' \"$(cat "+shellQuote(paths.StateDir+"/wg-public-key.txt")+")\"",
		"printf 'CONF_CHANGED=%s\\n' \"$CONF_CHANGED\"",
		"printf 'HANDSHAKE_AGE_S=%s\\n' \"$HANDSHAKE_AGE_S\"",
	)
	return strings.Join(lines, "\n") + "\n"
}

// The transport-bound wrapper that used to sit here — reconcileWireGuard(ssh,
// cfg, stateDir): converge, parse the trailers, insist on a PUBKEY — left with
// the forge, its only caller (grove-plugin-forge-gcp, internal/cmd/wireguard.go).
// What stays is the part that is genuinely generic and genuinely reusable: the
// renderer, the script builder and the trailer parser, none of which knows
// which host it is aimed at or how it gets there. `grove satellite` is the
// next caller; it will bring its own transport, as the forge did.

func parseWGShow(raw string) (wgStatus, error) {
	status := wgStatus{HandshakeAge: -1}
	seen := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case "PUBKEY":
			status.Pubkey = value
			seen[key] = true
		case "CONF_CHANGED":
			status.ConfChanged = value == "1"
			seen[key] = true
		case "HANDSHAKE_AGE_S":
			seen[key] = true
			if value != "never" {
				n, err := strconv.ParseInt(value, 10, 64)
				if err != nil || n < 0 {
					return wgStatus{}, fmt.Errorf("invalid HANDSHAKE_AGE_S %q", value)
				}
				status.HandshakeAge = n
			}
		case "ENDPOINT":
			status.Endpoint = value
		case "TRANSFER_RX_BYTES":
			status.TransferRXBytes, _ = strconv.ParseUint(value, 10, 64)
		case "TRANSFER_TX_BYTES":
			status.TransferTXBytes, _ = strconv.ParseUint(value, 10, 64)
		case "CONF_PRESENT":
			status.ConfPresent = value == "1"
		case "SERVICE_ENABLED":
			status.ServiceEnabled = value == "1"
		case "INTERFACE_ACTIVE":
			status.InterfaceActive = value == "1"
		}
	}
	for _, required := range []string{"PUBKEY", "CONF_CHANGED", "HANDSHAKE_AGE_S"} {
		if !seen[required] {
			return wgStatus{}, fmt.Errorf("missing %s trailer", required)
		}
	}
	return status, nil
}
