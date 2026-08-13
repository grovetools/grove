package cmd

// The operator's half of W3.5 adoption: `grove sync contested` and
// `grove sync adopt-notespace <id>`.
//
// Pulling a shared notebook onto a machine that already had notes of its own
// can find the same paths on both sides with different content. The daemon
// refuses that batch and marks the notespace contested — the gate is two-sided
// (daemon `ccd0d55`), so nothing enters the notespace and nothing leaves it:
// local edits queue rather than push — and records the evidence on the
// conflicts feed. These two verbs are how that state is read and resolved:
//
//	contested         — what is withheld, and the evidence for each case:
//	                    hash overlap (how much already agrees) and subject match
//	                    (whether both sides are notes about the same thing).
//	adopt-notespace   — the decision, named explicitly, one notespace at a time.
//
// Neither verb merges anything itself. Adoption records the operator's decision
// with the daemon; the daemon's pull loop resumes and applies the batch it had
// been holding. That keeps exactly one writer for the notespace tree, which is
// what the whole pipeline lifecycle depends on.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/grovetools/core/pkg/paths"
)

// contestedNotespace mirrors the daemon's GET /api/sync/contested rows.
// Explicit tags rather than case-folding, matching fetchSyncConflicts.
type contestedNotespace struct {
	NotespaceID  string `json:"notespace_id"`
	Root         string `json:"root"`
	Reason       string `json:"reason"`
	Detail       string `json:"detail"`
	Colliding    int    `json:"colliding_paths"`
	Identical    int    `json:"identical_paths"`
	Divergent    int    `json:"divergent_paths"`
	SubjectMatch string `json:"subject_match"`
}

func newSyncContestedCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "contested",
		Short: "List notespaces whose sync is held in both directions, with their adoption evidence",
		Long: `Report every notespace this machine is refusing incoming writes into.

A notespace becomes contested when an incoming batch would have written over
notes this machine has never synced. Nothing is lost and nothing is merged: no
writes enter the notespace and none leave it, local edits keep queuing, the
incoming batch stays owed by the server, and the notespace waits for a decision.
Adopting it releases both directions.

Each case carries the evidence that decision is made from:

  hash overlap    how many of the colliding paths already hold identical bytes.
                  High overlap means the two trees are the same notes that
                  drifted; zero overlap means the names collided and the
                  contents have nothing to do with each other.
  subject match   whether the stamp here and the server's row name the same
                  subject. A mismatch means one name is doing duty for two
                  subjects, and adopting would bury local work.

Adopt one with ` + "`grove sync adopt-notespace <notespace-id>`" + `.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			contested, err := fetchContestedNotespaces(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(contested)
			}
			if len(contested) == 0 {
				fmt.Fprintln(out, "No contested notespaces: nothing is being withheld from incoming writes.")
				return nil
			}
			for _, entry := range contested {
				renderContested(out, entry)
			}
			fmt.Fprintf(out, "%d contested notespace(s). Adopt one with `grove sync adopt-notespace <notespace-id>`.\n", len(contested))
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Emit the contested set as JSON")
	return cmd
}

func renderContested(out io.Writer, entry contestedNotespace) {
	fmt.Fprintf(out, "%s  %s\n", entry.NotespaceID, entry.Root)
	fmt.Fprintf(out, "  hash overlap   %d/%d colliding path(s) identical, %d divergent\n",
		entry.Identical, entry.Colliding, entry.Divergent)
	fmt.Fprintf(out, "  subject match  %s\n", orUnknown(entry.SubjectMatch))
	if entry.Reason != "" {
		fmt.Fprintf(out, "  reason         %s\n", entry.Reason)
	}
	for _, line := range strings.Split(strings.TrimRight(entry.Detail, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fmt.Fprintf(out, "  · %s\n", line)
	}
	fmt.Fprintln(out)
}

func orUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}

func newSyncAdoptNotespaceCmd() *cobra.Command {
	var confirm bool
	cmd := &cobra.Command{
		Use:   "adopt-notespace <notespace-id>",
		Short: "Adopt a contested notespace so sync resumes in both directions",
		Long: `Record that a contested notespace and the server's are the same notes.

Adoption is deliberate and per-notespace. It is never inferred — not even when
exactly one notespace is contested — because it is a statement about two
histories that only the operator can make.

What it does: writes the decision as a durable receipt (so a daemon restart does
not ask again) and clears the hold. The daemon's pull loop resumes and applies
the batch it had been withholding, merging into the local tree by the ordinary
rules; a document that then diverges lands on the conflicts feed as an ordinary
merge conflict.

What it does not do: it does not delete, move, or upload anything itself, and it
cannot be used to adopt a notespace that is not contested.

Read the evidence first with ` + "`grove sync contested`" + `. Re-run with --confirm
to apply.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSyncAdoptNotespace(cmd.Context(), cmd.OutOrStdout(), args[0], confirm)
		},
	}
	cmd.Flags().BoolVar(&confirm, "confirm", false, "Apply the adoption (without it, the evidence is printed and nothing changes)")
	return cmd
}

func runSyncAdoptNotespace(ctx context.Context, out io.Writer, notespaceID string, confirm bool) error {
	contested, err := fetchContestedNotespaces(ctx)
	if err != nil {
		return err
	}
	var target contestedNotespace
	found := false
	for _, entry := range contested {
		if entry.NotespaceID == notespaceID {
			target, found = entry, true
			break
		}
	}
	if !found {
		ids := make([]string, 0, len(contested))
		for _, entry := range contested {
			ids = append(ids, entry.NotespaceID)
		}
		if len(ids) == 0 {
			return fmt.Errorf("notespace %s is not contested; nothing is being withheld on this machine", notespaceID)
		}
		return fmt.Errorf("notespace %s is not contested (contested here: %s)", notespaceID, strings.Join(ids, ", "))
	}

	renderContested(out, target)
	if !confirm {
		fmt.Fprintf(out, "  Nothing changed. Re-run with --confirm to adopt %s and let sync resume in both directions.\n", notespaceID)
		return nil
	}

	adopted, receipt, err := adoptContestedNotespace(ctx, notespaceID)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "  adopted      %s  %s\n", adopted.NotespaceID, adopted.Root)
	fmt.Fprintf(out, "  receipt      %s\n", receipt)
	// Both directions, because the gate withheld both. `pushDesired` is
	// `pullDesired`'s twin (daemon `ccd0d55`), so a contested notespace moved
	// neither way and its outbox was parked rather than drained — and adopting
	// releases the queue as well as the batch. This line said "Incoming writes
	// resume" alone, which is understated in exactly the direction that
	// matters: an operator reading it cannot tell whether the local edits they
	// were deciding about are still only on this machine.
	fmt.Fprintf(out, "\n  Sync resumes in both directions for %s. The batch the daemon withheld replays\n", adopted.Root)
	fmt.Fprintf(out, "  from the server's cursor, so nothing that was owed to this machine was lost, and\n")
	fmt.Fprintf(out, "  the local edits that queued while it was contested now push.\n")
	return nil
}

// ---- daemon transport ----------------------------------------------------

// daemonSyncRequest talks to the global daemon over its unix socket, the same
// transport fetchSyncConflicts uses. The contested set lives in the running
// daemon's memory (its verdicts) and its state dir (its receipts), so there is
// nothing to read from config here.
func daemonSyncRequest(ctx context.Context, method, path string, body any) ([]byte, error) {
	socket := paths.SocketPath()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	client := &http.Client{Transport: transport}

	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://groved"+path, payload)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query the daemon at %s: %w", socket, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon %s %s returned HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}

func fetchContestedNotespaces(ctx context.Context) ([]contestedNotespace, error) {
	data, err := daemonSyncRequest(ctx, http.MethodGet, "/api/sync/contested", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Contested []contestedNotespace `json:"contested"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out.Contested, nil
}

func adoptContestedNotespace(ctx context.Context, notespaceID string) (contestedNotespace, string, error) {
	data, err := daemonSyncRequest(ctx, http.MethodPost, "/api/sync/contested/adopt",
		map[string]string{"notespace_id": notespaceID})
	if err != nil {
		return contestedNotespace{}, "", err
	}
	var out struct {
		Adopted contestedNotespace `json:"adopted"`
		Receipt string             `json:"receipt"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return contestedNotespace{}, "", err
	}
	return out.Adopted, out.Receipt, nil
}
