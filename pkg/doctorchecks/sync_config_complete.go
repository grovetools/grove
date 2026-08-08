package doctorchecks

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/grovetools/core/config"
	"github.com/grovetools/core/pkg/doctor"
)

func init() {
	doctor.Register(&syncConfigCompleteCheck{})
}

// syncConfigCompleteCheck answers one question: would the daemon actually be
// able to replicate what this sync.toml subscribes to?
//
// It exists because the failure it catches is INVISIBLE from every other
// surface. A sync.toml with workspaces and no server produces no error, no
// warning and no log line — the daemon simply has nowhere to connect, so every
// counter stays at zero and every status screen stays plausible. The same is
// true of a `token_command` that resolves to nothing: the machine 401-loops,
// and "0 documents synced" looks exactly like "nothing has changed".
//
// The check therefore asserts the three facts a subscription needs, in the
// order a daemon needs them:
//
//	server          somewhere to connect
//	a credential    something to present
//	it resolves     the credential command actually yields bytes
//
// It deliberately does NOT verify the token against the server. That is a
// network call with a real failure mode of its own (`grove join` owns it, and
// distinguishes "rejected" from "could not ask"), and doctor must stay
// runnable on a laptop with no connectivity.
type syncConfigCompleteCheck struct{}

func (c *syncConfigCompleteCheck) ID() string   { return "sync_config_complete" }
func (c *syncConfigCompleteCheck) Name() string { return "sync.toml can actually replicate" }

// syncTokenProbeTimeout bounds the token_command probe. A secrets manager may
// do a network call or a biometric unlock; a doctor run must still finish.
const syncTokenProbeTimeout = 10 * time.Second

func (c *syncConfigCompleteCheck) Run(ctx context.Context, opts doctor.RunOptions) doctor.CheckResult {
	res := doctor.CheckResult{ID: c.ID(), Name: c.Name()}

	path := config.SyncConfigPath()
	cfg, err := config.LoadSyncConfig()
	if err != nil {
		res.Status = doctor.StatusFail
		res.Message = "sync.toml is unreadable, so this machine's sync intent cannot be checked"
		res.Error = compactError(err)
		res.Resolution = fmt.Sprintf("fix %s (or move it aside and re-run `grove join`)", path)
		return res
	}
	if cfg == nil {
		res.Status = doctor.StatusOK
		res.Message = "no sync.toml; sync is off on this machine"
		return res
	}
	if len(cfg.Workspaces) == 0 {
		// Nothing is subscribed, so nothing is silently not replicating. A
		// server with no workspaces is a staged config, not a broken one.
		res.Status = doctor.StatusOK
		res.Message = "sync.toml declares no workspace subscriptions; nothing to replicate"
		return res
	}

	var problems []string
	if strings.TrimSpace(cfg.Server) == "" {
		problems = append(problems, fmt.Sprintf("%d workspace subscription(s) but no `server` — there is nowhere for them to replicate to", len(cfg.Workspaces)))
	}

	hasCommand := strings.TrimSpace(cfg.TokenCommand) != ""
	hasLiteral := strings.TrimSpace(cfg.Token) != ""
	switch {
	case !hasCommand && !hasLiteral:
		problems = append(problems, "no `token_command` and no `token` — every request will be rejected")
	case hasCommand:
		// Resolve it the way the daemon will. Neither the command text nor its
		// output appears in the message: a command like `echo hunter2` carries
		// the secret in its own text, so failures name the CONFIG KEY.
		if err := probeSyncTokenCommand(ctx, cfg.TokenCommand); err != nil {
			problems = append(problems, "`token_command` "+err.Error())
		}
	}

	if len(problems) > 0 {
		res.Status = doctor.StatusFail
		res.Message = fmt.Sprintf("sync.toml subscribes to %d workspace(s) it cannot replicate", len(cfg.Workspaces))
		res.Error = strings.Join(problems, "; ")
		res.Resolution = "run `grove join --repair` to fill in the absent keys (it converges an existing config and mints nothing), or edit " + path + " by hand"
		return res
	}

	res.Status = doctor.StatusOK
	res.Message = fmt.Sprintf("%d subscription(s), a server, and a credential that resolves", len(cfg.Workspaces))
	return res
}

// probeSyncTokenCommand runs the command and reports whether it yields a
// token, describing the failure without quoting the command or its output.
func probeSyncTokenCommand(ctx context.Context, command string) error {
	ctx, cancel := context.WithTimeout(ctx, syncTokenProbeTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "sh", "-c", command).Output() //nolint:gosec // G204: the command is the operator's own token_command
	if ctx.Err() != nil {
		return fmt.Errorf("timed out after %s", syncTokenProbeTimeout)
	}
	if err != nil {
		return fmt.Errorf("does not run successfully (%s)", compactExit(err))
	}
	if strings.TrimSpace(string(out)) == "" {
		// The exact shape of the job-52 keychain bug: the item exists, the
		// command succeeds, and the value is the empty string.
		return fmt.Errorf("runs but produces no token — the credential it names is empty or missing")
	}
	return nil
}

// compactExit renders a failure as an exit status and nothing else — no stderr,
// no command text. Both are places a token_command's secret can land.
func compactExit(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return fmt.Sprintf("exit status %d", ee.ExitCode())
	}
	return "could not run"
}

func (c *syncConfigCompleteCheck) AutoFix(ctx context.Context) error {
	// Deliberately not fixable here. Filling the absent keys means deciding
	// WHICH server and WHICH credential, and `grove join` is where that
	// decision is made and then verified against the server.
	return fmt.Errorf("%w: run `grove join --repair` (or `grove join --mint` if this machine has no credential yet)", doctor.ErrNotFixable)
}
