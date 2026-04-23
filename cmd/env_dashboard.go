package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/grovetools/core/pkg/daemon"
	"github.com/spf13/cobra"
)

// dashboardPortFilePath returns the path where the global daemon records the
// dashboard's ephemeral TCP port. Matches daemon/internal/daemon/server
// DashboardPortFile(); duplicated here rather than imported so grove avoids
// a compile-time dep on the daemon internals package.
func dashboardPortFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "grove", "dashboard.port")
}

func newEnvDashboardCmd() *cobra.Command {
	var noOpen bool
	var printOnly bool
	cmd := &cobra.Command{
		Use:   "dashboard",
		Short: "Open the grove env browser dashboard",
		Long: `Open the grove env browser dashboard served by the global grove daemon.

The global daemon binds an ephemeral 127.0.0.1 port for the dashboard and
writes it to ~/.local/state/grove/dashboard.port. This command reads that
file, prints the URL, and opens it in the default browser.

If the port file is missing, the command spawns the global daemon (which
auto-binds the dashboard on boot) and waits up to 5 s for the file to
appear.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			url, err := resolveDashboardURL()
			if err != nil {
				return err
			}
			fmt.Println(url)
			if printOnly || noOpen {
				return nil
			}
			return openBrowser(url)
		},
	}
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "Print the URL without opening a browser")
	cmd.Flags().BoolVar(&printOnly, "print-only", false, "Alias for --no-open (for scripts)")
	return cmd
}

// resolveDashboardURL reads the port file written by the global daemon,
// falling back to spawning the daemon and polling for up to 5 s.
func resolveDashboardURL() (string, error) {
	port, err := readPortFile()
	if err == nil && port > 0 {
		return fmt.Sprintf("http://127.0.0.1:%d/dashboard", port), nil
	}

	// Trigger autostart of the global daemon — daemon.NewWithAutoStart
	// spawns it when absent and blocks on a readiness handshake.
	_ = daemon.NewWithAutoStart()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if port, err := readPortFile(); err == nil && port > 0 {
			return fmt.Sprintf("http://127.0.0.1:%d/dashboard", port), nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return "", fmt.Errorf("dashboard port file not found at %s — is the global grove daemon running?", dashboardPortFilePath())
}

func readPortFile() (int, error) {
	data, err := os.ReadFile(dashboardPortFilePath())
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(data))
	return strconv.Atoi(s)
}

// openBrowser launches the OS's default handler. Non-fatal on failure — we
// already printed the URL, so the user can click it manually.
func openBrowser(url string) error {
	var cmdName string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmdName = "open"
		args = []string{url}
	case "linux":
		cmdName = "xdg-open"
		args = []string{url}
	case "windows":
		cmdName = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default:
		return nil
	}
	c := exec.Command(cmdName, args...)
	c.Stdout = nil
	c.Stderr = nil
	if err := c.Start(); err != nil {
		return fmt.Errorf("open browser (%s): %w", cmdName, err)
	}
	return nil
}
