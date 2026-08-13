package cmd

import (
	"fmt"

	"github.com/grovetools/core/cli"
	"github.com/spf13/cobra"
)

// muxTool is the binary `grove mux` opens. treemux cannot be imported: its TUI
// lives entirely in internal/ and it already imports grove packages, so the
// only way in is exec.
const muxTool = "treemux"

// newMuxCmd wires `grove mux` — the front door to the cockpit.
//
// grove is the only name the install puts in the user's global namespace; the
// rest of the toolchain lives in paths.BinDir(). `grove mux` is how the
// flagship TUI is reached from there, and it hands treemux the PATH capsule so
// every pane, plugin panel and interactive shell inside the cockpit still
// resolves bare `nb`, `flow`, `cx`, `groved`.
func newMuxCmd() *cobra.Command {
	cmd := cli.NewStandardCommand("mux", "Open the treemux cockpit")
	cmd.Long = `Open the treemux cockpit.

Every argument is forwarded to treemux verbatim, so its own flags work
unchanged:

  grove mux                      # open the cockpit
  grove mux --home               # start on the Home window
  grove mux --reset-layout       # rewrite a drifted layout first
  grove mux --pprof-port 6060    # profiling
  grove mux start --onboard      # any treemux subcommand

'grove treemux ...' remains equivalent — both resolve the same binary and both
prepend grove's toolchain directory to PATH for the whole cockpit subtree.`

	// Forward EVERYTHING. treemux owns the flag namespace here; parsing it in
	// grove would swallow --home/--json/-v and reject flags grove has never
	// heard of. This also means `grove mux --help` prints treemux's help,
	// which is the help the user is actually asking for.
	cmd.DisableFlagParsing = true
	cmd.SilenceUsage = true
	cmd.RunE = func(_ *cobra.Command, args []string) error {
		return runMux(args)
	}
	return cmd
}

func runMux(args []string) error {
	res, ok, err := resolveTool(muxTool)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("treemux is not installed — run 'grove install treemux'")
	}

	// argv[0] is the tool's own name, as a shell would pass it: treemux reads
	// it for usage strings and for its own re-exec paths.
	return execTool(res.Path, append([]string{muxTool}, args...), res.Env())
}
