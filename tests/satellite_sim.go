package tests

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"sync"

	"github.com/grovetools/tend/pkg/harness"
	"github.com/grovetools/tend/pkg/project"
	"golang.org/x/crypto/ssh"
)

// simSatellite is the local satellite simulator: an in-process SSH server on
// 127.0.0.1 with a throwaway HOME. grove's production transport is exercised
// byte-for-byte — the scenarios run the REAL `ssh`/`scp` client binaries with
// BatchMode, a pinned host key (StrictHostKeyChecking=yes + generated
// known_hosts + locked HostKeyAlgorithms), and the SFTP protocol for scp
// (delegated to the system's OpenSSH sftp-server binary, exactly as sshd
// would). Only the server side of the wire is simulated.
//
// Design note: the original plan was an unprivileged `/usr/sbin/sshd -D` (see
// the ForceCommand-wrapper shape in this file's exec env). Apple's sshd
// unconditionally sandboxes its preauth child via sandbox_init, which fails
// inside any already-sandboxed environment (CI harnesses, agent sandboxes),
// making it unrunnable exactly where E2E suites live. This server keeps the
// client-side bytes identical while staying runnable everywhere Go runs.
type simSatellite struct {
	name     string
	rootDir  string // /tmp/grove-sat-<hex>; short + safe charset for VM path validation
	homeDir  string // <rootDir>/home — the satellite HOME
	addr     string // 127.0.0.1:<port>
	userName string

	hostKeyLine  string // "ssh-ed25519 <b64>"
	identityFile string

	listener net.Listener
	execEnv  []string
	wg       sync.WaitGroup
}

// newSimSatellite provisions the simulator: keys, satellite HOME skeleton,
// optional grove binary install, and the listening SSH server. A non-empty
// skip reason means an environmental prerequisite is missing (pass-with-
// notice; tend has no runtime skip).
func newSimSatellite(ctx *harness.Context, installGrove bool) (satelliteEndpoint, string, error) {
	for _, bin := range []string{"ssh", "scp", "git", "bash"} {
		if _, err := exec.LookPath(bin); err != nil {
			return nil, fmt.Sprintf("%s not found on PATH — cannot run the satellite sim", bin), nil
		}
	}
	sftpServer, err := findSFTPServer()
	if err != nil {
		return nil, "no OpenSSH sftp-server binary found — cannot serve scp: " + err.Error(), nil
	}

	// The VM-side worktree path is validated by grove against a conservative
	// charset (^/[A-Za-z0-9._/-]+$). macOS's default TMPDIR (/var/folders/…)
	// can contain characters outside it, so the satellite HOME lives under
	// /tmp when writable, with a short, safe name (same reasoning as tend's
	// XDG_RUNTIME_DIR).
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return nil, "", err
	}
	rootDir := filepath.Join(writableTmpBase(), "grove-sat-"+hex.EncodeToString(suffix[:]))
	homeDir := filepath.Join(rootDir, "home")
	for _, d := range []string{
		filepath.Join(homeDir, "code", "grovetools"),
		filepath.Join(homeDir, ".local", "share", "grove", "bin"),
		filepath.Join(homeDir, ".local", "state"),
		filepath.Join(homeDir, ".config"),
		filepath.Join(homeDir, ".cache"),
		filepath.Join(rootDir, "keys"),
		filepath.Join(rootDir, "stage"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, "", err
		}
	}
	if err := os.WriteFile(filepath.Join(homeDir, ".gitconfig"), []byte(testGitConfig), 0o644); err != nil {
		return nil, "", err
	}

	if installGrove {
		groveBin, err := project.GetBinaryPath(ctx.ProjectRoot)
		if err != nil || groveBin == "" {
			groveBin = filepath.Join(ctx.ProjectRoot, "bin", "grove")
		}
		if _, err := os.Stat(groveBin); err != nil {
			return nil, "", fmt.Errorf("built grove binary not found at %s (run `make build` first): %w", groveBin, err)
		}
		if err := os.Symlink(groveBin, filepath.Join(homeDir, ".local", "share", "grove", "bin", "grove")); err != nil {
			return nil, "", err
		}
	}

	// Host key: generated ed25519, pinned in the registry entry — the same
	// "<type> <base64>" line newSatelliteSSH writes into its known_hosts.
	hostPub, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		return nil, "", err
	}
	sshHostPub, err := ssh.NewPublicKey(hostPub)
	if err != nil {
		return nil, "", err
	}
	hostKeyLine := sshHostPub.Type() + " " + base64.StdEncoding.EncodeToString(sshHostPub.Marshal())

	// Client keypair: private key on disk (0600) for ssh -i; public key is
	// the only one the server authenticates.
	clientPub, clientPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	pemBlock, err := ssh.MarshalPrivateKey(clientPriv, "grove-satellite-sim")
	if err != nil {
		return nil, "", err
	}
	identityFile := filepath.Join(rootDir, "keys", "client_ed25519")
	if err := os.WriteFile(identityFile, pem.EncodeToMemory(pemBlock), 0o600); err != nil {
		return nil, "", err
	}
	sshClientPub, err := ssh.NewPublicKey(clientPub)
	if err != nil {
		return nil, "", err
	}
	authorizedKey := sshClientPub.Marshal()

	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if bytes.Equal(key.Marshal(), authorizedKey) {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("unknown public key")
		},
	}
	cfg.AddHostKey(hostSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}

	u, err := user.Current()
	if err != nil {
		_ = listener.Close()
		return nil, "", err
	}

	s := &simSatellite{
		name:         "sim",
		rootDir:      rootDir,
		homeDir:      homeDir,
		addr:         listener.Addr().String(),
		userName:     u.Username,
		hostKeyLine:  hostKeyLine,
		identityFile: identityFile,
		listener:     listener,
		// The exec env a ForceCommand wrapper would export: the satellite
		// HOME with matching XDG dirs. Scripts add the grove bin dir to PATH
		// themselves (satelliteRemotePATH), mirroring the VM's profile.d.
		execEnv: []string{
			"HOME=" + homeDir,
			"XDG_CONFIG_HOME=" + filepath.Join(homeDir, ".config"),
			"XDG_DATA_HOME=" + filepath.Join(homeDir, ".local", "share"),
			"XDG_STATE_HOME=" + filepath.Join(homeDir, ".local", "state"),
			"XDG_CACHE_HOME=" + filepath.Join(homeDir, ".cache"),
			"PATH=/usr/bin:/bin:/usr/sbin:/sbin:/usr/local/bin",
			"USER=" + u.Username,
			"LOGNAME=" + u.Username,
			"SHELL=/bin/bash",
		},
	}
	s.wg.Add(1)
	go s.acceptLoop(cfg, sftpServer)
	return s, "", nil
}

// testGitConfig gives both sandboxed sides a deterministic git identity and
// default branch (sandboxed HOMEs have no user gitconfig).
const testGitConfig = `[user]
	name = Tend Satellite
	email = tend-satellite@example.com
[init]
	defaultBranch = main
[commit]
	gpgsign = false
`

func findSFTPServer() (string, error) {
	candidates := []string{
		"/usr/libexec/sftp-server",         // macOS
		"/usr/lib/openssh/sftp-server",     // Debian/Ubuntu
		"/usr/libexec/openssh/sftp-server", // Fedora/RHEL
		"/usr/lib/ssh/sftp-server",         // Arch
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	if p, err := exec.LookPath("sftp-server"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("looked in %s and PATH", strings.Join(candidates, ", "))
}

func (s *simSatellite) Name() string          { return s.name }
func (s *simSatellite) RemoteCodeDir() string { return "~/code/grovetools" }
func (s *simSatellite) IsSim() bool           { return true }

// ExtraGroveEnv redirects the VM-side stage base into the sim's root: the
// sim "VM" shares the local filesystem, and the default /tmp base is not
// writable inside sandboxed environments.
func (s *simSatellite) ExtraGroveEnv() []string {
	return []string{"GROVE_SATELLITE_STAGE_BASE=" + filepath.Join(s.rootDir, "stage")}
}

func (s *simSatellite) RegistryEntry() satelliteRegistryEntry {
	return satelliteRegistryEntry{
		SSHAddr:      s.addr,
		User:         s.userName,
		HostKey:      s.hostKeyLine,
		IdentityFile: s.identityFile,
	}
}

// Exec runs a bash script with the satellite's environment. Test-side
// manipulation only (grove itself always goes over the wire), so this runs
// locally — byte-equivalent to what the server's exec handler would do.
func (s *simSatellite) Exec(script string) (string, error) {
	cmd := exec.Command("/bin/bash", "-s")
	cmd.Dir = s.homeDir
	cmd.Env = s.execEnv
	cmd.Stdin = strings.NewReader(script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("sim exec: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func (s *simSatellite) Close() error {
	_ = s.listener.Close()
	s.wg.Wait()
	return os.RemoveAll(s.rootDir)
}

// --- the SSH server ---

func (s *simSatellite) acceptLoop(cfg *ssh.ServerConfig, sftpServer string) {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return // listener closed
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConn(conn, cfg, sftpServer)
		}()
	}
}

func (s *simSatellite) handleConn(conn net.Conn, cfg *ssh.ServerConfig, sftpServer string) {
	sshConn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)
	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			_ = newChan.Reject(ssh.UnknownChannelType, "only session channels are supported")
			continue
		}
		channel, requests, err := newChan.Accept()
		if err != nil {
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleSession(channel, requests, sftpServer)
		}()
	}
}

// handleSession implements the subset of the SSH session protocol grove's
// transport uses: "exec" (ssh <dest> <command>, scripts over stdin) and the
// "sftp" subsystem (OpenSSH ≥9 scp), plus a bare "shell" for completeness.
func (s *simSatellite) handleSession(channel ssh.Channel, requests <-chan *ssh.Request, sftpServer string) {
	defer channel.Close()
	for req := range requests {
		switch req.Type {
		case "env":
			// Ignored (parity with sshd's default AcceptEnv none).
			_ = req.Reply(true, nil)
		case "exec":
			var payload struct{ Command string }
			if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
				_ = req.Reply(false, nil)
				continue
			}
			_ = req.Reply(true, nil)
			s.runInSession(channel, exec.Command("/bin/bash", "-c", payload.Command))
			return
		case "shell":
			_ = req.Reply(true, nil)
			s.runInSession(channel, exec.Command("/bin/bash"))
			return
		case "subsystem":
			var payload struct{ Name string }
			if err := ssh.Unmarshal(req.Payload, &payload); err != nil || payload.Name != "sftp" {
				_ = req.Reply(false, nil)
				continue
			}
			_ = req.Reply(true, nil)
			// Delegate to the system sftp-server, exactly as sshd's
			// Subsystem directive would; cwd = satellite HOME so relative
			// remote paths resolve as they would in a real login.
			s.runInSession(channel, exec.Command(sftpServer, "-e"))
			return
		default:
			_ = req.Reply(false, nil)
		}
	}
}

// runInSession wires a command to the session channel, waits, and reports
// the exit status (which ssh/scp propagate as their own exit codes).
func (s *simSatellite) runInSession(channel ssh.Channel, cmd *exec.Cmd) {
	cmd.Dir = s.homeDir
	cmd.Env = s.execEnv
	cmd.Stdin = channel
	cmd.Stdout = channel
	cmd.Stderr = channel.Stderr()
	status := uint32(0)
	if err := cmd.Run(); err != nil {
		status = 1
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() >= 0 {
			status = uint32(ee.ExitCode())
		}
	}
	_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
}

func (s *simSatellite) RegistryEntryJSON() json.RawMessage {
	data, _ := json.Marshal(s.RegistryEntry())
	return data
}
