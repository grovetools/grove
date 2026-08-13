package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	corenotespace "github.com/grovetools/core/pkg/notespace"
	"github.com/grovetools/tend/pkg/fs"
	"github.com/grovetools/tend/pkg/harness"
	"github.com/grovetools/tend/pkg/verify"
)

// V2 for the sibling verbs (P4 W4.1/W4.4): `grove notespace new`, `primary` and
// `list`, driven as the real binary against a sandboxed machine.
//
// The decomposition asked for this scenario explicitly, because the P3 verbs
// never got a grove-side one either — every proof of these three was a Go test
// in `grove/cmd`, which exercises the run functions rather than the command the
// operator types. This closes the P3 V2 gap in grove at the same time.
//
// Hermetic by construction, and it has to be: primariness is a MACHINE-LOCAL
// routing pointer, so every fact these verbs read and write is on this
// machine's disk and no server is involved. The recorded machine is seeded as
// plain files under the harness's sandboxed XDG_CONFIG_HOME — notebooks.toml
// and machine.toml written as text, stamps installed with
// `notespace.InstallNotespace`. Nothing here calls core's config WRITER
// in-process: this test binary is not itself sandboxed, so
// `config.MachineConfigPath()` would resolve the operator's real machine.toml.
// The sandboxed `grove` subprocess is the only thing allowed to write config,
// which is also what makes the write path the thing under test.
//
// No `[[test_scopes]]` entry, per the decomposition and core/CLAUDE.md: config
// loading is shared source, so these stay in the default sweep.

const (
	nsScenarioSubject      = "example.com/org/core"
	nsScenarioOtherSubject = "example.com/org/other"
	nsScenarioPrimaryID    = "01ARZ3NDEKTSV4RRFFQ69G5F01"
	nsScenarioOtherID      = "01ARZ3NDEKTSV4RRFFQ69G5F03"
)

// nsListing mirrors the `notespace list --json` document. It is decoded rather
// than grepped so the assertions are about the arrangement, not about spacing.
type nsListing struct {
	Subjects []struct {
		Subject            string `json:"subject"`
		PrimaryNotespaceID string `json:"primary_notespace_id"`
		Notespaces         []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Dir      string `json:"dir"`
			Notebook string `json:"notebook"`
			Root     string `json:"root"`
		} `json:"notespaces"`
	} `json:"subjects"`
	Problems []string `json:"problems,omitempty"`
}

func (l nsListing) group(subject string) (int, bool) {
	for i, group := range l.Subjects {
		if group.Subject == subject {
			return i, true
		}
	}
	return 0, false
}

// nsGroveConfigDir is where the sandboxed grove reads notebooks.toml and
// machine.toml.
func nsGroveConfigDir(ctx *harness.Context) string {
	return filepath.Join(ctx.ConfigDir(), "grove")
}

// nsStamp installs a notespace directory with a stamp and one note in it.
func nsStamp(root, id, name, subject string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	if err := fs.WriteString(filepath.Join(root, "note.md"), "# "+filepath.Base(root)+"\n"); err != nil {
		return err
	}
	_, err := corenotespace.InstallNotespace(root, corenotespace.NotespaceStamp{
		ID: id, Name: name, Subject: subject, Kind: "repo",
	})
	return err
}

// nsSeed records two notebooks, stamps the subject's one notespace in `work`,
// and records it as the primary. An unrelated subject is seeded alongside it so
// a verb that rewrote the wrong [primaries] entry would be visible.
func nsSeed(ctx *harness.Context) error {
	configDir := nsGroveConfigDir(ctx)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return err
	}
	work := nbResolveSymlinks(filepath.Join(ctx.RootDir, "notebooks", "work"))
	personal := nbResolveSymlinks(filepath.Join(ctx.RootDir, "notebooks", "personal"))
	ctx.Set("ns_work", work)
	ctx.Set("ns_personal", personal)

	for _, root := range []string{work, personal} {
		if err := os.MkdirAll(filepath.Join(root, "notespaces"), 0o755); err != nil {
			return err
		}
	}
	if err := nsStamp(filepath.Join(work, "notespaces", "core"), nsScenarioPrimaryID, "core", nsScenarioSubject); err != nil {
		return err
	}
	if err := nsStamp(filepath.Join(work, "notespaces", "other"), nsScenarioOtherID, "other", nsScenarioOtherSubject); err != nil {
		return err
	}

	notebooks := fmt.Sprintf("default = \"work\"\n\n[notebooks.work]\nroot = %q\n\n[notebooks.personal]\nroot = %q\n", work, personal)
	if err := fs.WriteString(filepath.Join(configDir, "notebooks.toml"), notebooks); err != nil {
		return err
	}
	machine := fmt.Sprintf("[machine]\nname = \"tend-sandbox\"\n\n[primaries]\n%q = %q\n%q = %q\n",
		nsScenarioSubject, nsScenarioPrimaryID, nsScenarioOtherSubject, nsScenarioOtherID)
	return fs.WriteString(filepath.Join(configDir, "machine.toml"), machine)
}

// nsList runs `grove notespace list --json` and decodes it.
func nsList(ctx *harness.Context) (nsListing, string, error) {
	cmd := ctx.Bin("notespace", "list", "--json")
	result := cmd.Run()
	if err := result.AssertSuccess(); err != nil {
		return nsListing{}, result.Stdout, fmt.Errorf("notespace list --json: %w\nStdout: %s\nStderr: %s", err, result.Stdout, result.Stderr)
	}
	var listing nsListing
	if err := json.Unmarshal([]byte(result.Stdout), &listing); err != nil {
		return nsListing{}, result.Stdout, fmt.Errorf("decode notespace list --json: %w\nStdout: %s", err, result.Stdout)
	}
	return listing, result.Stdout, nil
}

// nsMachineTOML reads the sandboxed machine.toml.
func nsMachineTOML(ctx *harness.Context) (string, error) {
	return fs.ReadString(filepath.Join(nsGroveConfigDir(ctx), "machine.toml"))
}

// NotespaceSiblingVerbsScenario drives `notespace new` (same notebook and a
// different one), `notespace primary` (the flip, and the refusal on an
// ambiguous name) and `notespace list` (primary marked, parent notebook shown,
// a duplicate id reported once).
func NotespaceSiblingVerbsScenario() *harness.Scenario {
	return harness.NewScenario(
		"notespace-sibling-verbs",
		"grove notespace new/primary/list against a sandboxed recorded machine",
		[]string{"notespace", "siblings", "config"},
		[]harness.Step{
			harness.NewStep("Seed a recorded machine with one primary", func(ctx *harness.Context) error {
				if err := nsSeed(ctx); err != nil {
					return err
				}
				listing, human, err := nsList(ctx)
				if err != nil {
					return err
				}
				ctx.ShowCommandOutput("grove notespace list --json", human, "")
				index, ok := listing.group(nsScenarioSubject)
				if !ok {
					return fmt.Errorf("the seeded subject is not listed: %s", human)
				}
				group := listing.Subjects[index]
				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("the seeded primary is recorded", nsScenarioPrimaryID, group.PrimaryNotespaceID)
					v.Equal("the subject holds exactly its one notespace", 1, len(group.Notespaces))
					v.Equal("the row names its parent notebook", "work", group.Notespaces[0].Notebook)
				})
			}),

			harness.NewStep("notespace new in the SAME notebook, routing untouched", func(ctx *harness.Context) error {
				before, err := nsMachineTOML(ctx)
				if err != nil {
					return err
				}
				cmd := ctx.Bin("notespace", "new", nsScenarioSubject, "--in", "work")
				result := cmd.Run()
				ctx.ShowCommandOutput("grove notespace new --in work", result.Stdout, result.Stderr)
				if err := ctx.Check("notespace new succeeds", result.AssertSuccess()); err != nil {
					return fmt.Errorf("notespace new: %w\nStderr: %s", err, result.Stderr)
				}
				after, err := nsMachineTOML(ctx)
				if err != nil {
					return err
				}
				work := ctx.GetString("ns_work")
				return ctx.Verify(func(v *verify.Collector) {
					// The name is uniquified even though `core-2` is free: one
					// name per subject is what keeps `primary` and `move` able
					// to resolve a notespace by name at all.
					v.Equal("the sibling is materialized", nil, fs.AssertExists(filepath.Join(work, "notespaces", "core-2", corenotespace.NotespaceStampName)))
					v.Equal("machine.toml is byte-identical", before, after)
					v.Contains("the verb says what it did not touch", result.Stdout, "machine.toml was not written")
				})
			}),

			harness.NewStep("notespace new in a DIFFERENT notebook", func(ctx *harness.Context) error {
				cmd := ctx.Bin("notespace", "new", nsScenarioSubject, "--in", "personal")
				result := cmd.Run()
				ctx.ShowCommandOutput("grove notespace new --in personal", result.Stdout, result.Stderr)
				if err := ctx.Check("notespace new succeeds", result.AssertSuccess()); err != nil {
					return fmt.Errorf("notespace new: %w\nStderr: %s", err, result.Stderr)
				}
				listing, human, err := nsList(ctx)
				if err != nil {
					return err
				}
				index, ok := listing.group(nsScenarioSubject)
				if !ok {
					return fmt.Errorf("the subject vanished from the listing: %s", human)
				}
				group := listing.Subjects[index]
				notebooks := map[string]int{}
				ids := map[string]bool{}
				for _, row := range group.Notespaces {
					notebooks[row.Notebook]++
					ids[row.ID] = true
				}
				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("the subject now holds three notespaces", 3, len(group.Notespaces))
					v.Equal("each carries its own immutable id", 3, len(ids))
					v.Equal("two are in work", 2, notebooks["work"])
					v.Equal("one is in personal", 1, notebooks["personal"])
					v.Equal("the recorded primary is still the original", nsScenarioPrimaryID, group.PrimaryNotespaceID)
					v.True("the primary is listed first", len(group.Notespaces) > 0 && group.Notespaces[0].ID == nsScenarioPrimaryID)
				})
			}),

			harness.NewStep("notespace primary flips routing and nothing else", func(ctx *harness.Context) error {
				work := ctx.GetString("ns_work")
				oldPrimaryNote := filepath.Join(work, "notespaces", "core", "note.md")
				contentBefore, err := fs.ReadString(oldPrimaryNote)
				if err != nil {
					return err
				}

				cmd := ctx.Bin("notespace", "primary", "core-2")
				result := cmd.Run()
				ctx.ShowCommandOutput("grove notespace primary core-2", result.Stdout, result.Stderr)
				if err := ctx.Check("notespace primary succeeds", result.AssertSuccess()); err != nil {
					return fmt.Errorf("notespace primary: %w\nStderr: %s", err, result.Stderr)
				}

				listing, human, err := nsList(ctx)
				if err != nil {
					return err
				}
				index, ok := listing.group(nsScenarioSubject)
				if !ok {
					return fmt.Errorf("the subject vanished from the listing: %s", human)
				}
				group := listing.Subjects[index]
				var flippedID string
				for _, row := range group.Notespaces {
					if row.Dir == "core-2" {
						flippedID = row.ID
					}
				}
				otherIndex, otherOK := listing.group(nsScenarioOtherSubject)
				contentAfter, err := fs.ReadString(oldPrimaryNote)
				if err != nil {
					return err
				}
				machine, err := nsMachineTOML(ctx)
				if err != nil {
					return err
				}
				return ctx.Verify(func(v *verify.Collector) {
					v.True("core-2 is now the recorded primary", flippedID != "" && group.PrimaryNotespaceID == flippedID)
					v.True("and it is listed first", len(group.Notespaces) > 0 && group.Notespaces[0].Dir == "core-2")
					v.Equal("the old primary's content is untouched", contentBefore, contentAfter)
					v.Equal("the old primary's stamp is untouched", nil, fs.AssertContains(filepath.Join(work, "notespaces", "core", corenotespace.NotespaceStampName), nsScenarioPrimaryID))
					v.True("the unrelated subject's primary is unchanged", otherOK && listing.Subjects[otherIndex].PrimaryNotespaceID == nsScenarioOtherID)
					// Exactly one entry was rewritten: the old id must be gone
					// from [primaries] entirely, not merely outranked.
					v.NotContains("the replaced id is no longer recorded", machine, nsScenarioPrimaryID)
					v.Contains("the unrelated binding survives", machine, nsScenarioOtherID)
				})
			}),

			harness.NewStep("an ambiguous name is refused, not resolved by sort order", func(ctx *harness.Context) error {
				// An explicit --name is judged against the DESTINATION alone, so
				// a second `core` in the other notebook is legal — and it is
				// exactly what makes the name rung ambiguous.
				create := ctx.Bin("notespace", "new", nsScenarioSubject, "--in", "personal", "--name", "core")
				created := create.Run()
				ctx.ShowCommandOutput("grove notespace new --name core", created.Stdout, created.Stderr)
				if err := ctx.Check("a same-name sibling in another notebook is legal", created.AssertSuccess()); err != nil {
					return fmt.Errorf("notespace new --name core: %w\nStderr: %s", err, created.Stderr)
				}

				cmd := ctx.Bin("notespace", "primary", "core")
				result := cmd.Run()
				ctx.ShowCommandOutput("grove notespace primary core", result.Stdout, result.Stderr)
				combined := result.Stdout + result.Stderr
				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("an ambiguous name refuses", nil, result.AssertFailure())
					v.True("and says to name it by its immutable id",
						strings.Contains(combined, "immutable id") || strings.Contains(combined, "ambiguous"))
				})
			}),

			harness.NewStep("a duplicated id is reported once, with its repair", func(ctx *harness.Context) error {
				// D8, installed last because it is fail-closed for every verb
				// that resolves by id.
				personal := ctx.GetString("ns_personal")
				dup := filepath.Join(personal, "notespaces", "core-duplicate")
				if err := nsStamp(dup, nsScenarioOtherID, "core-duplicate", nsScenarioOtherSubject); err != nil {
					return err
				}
				listing, human, err := nsList(ctx)
				if err != nil {
					return err
				}
				ctx.ShowCommandOutput("grove notespace list --json (with a duplicate)", human, "")
				mentions := 0
				for _, problem := range listing.Problems {
					if strings.Contains(problem, nsScenarioOtherID) {
						mentions++
					}
				}
				return ctx.Verify(func(v *verify.Collector) {
					v.Equal("the duplicate is reported exactly once", 1, mentions)
					v.True("and the report names the repair",
						mentions == 1 && strings.Contains(strings.Join(listing.Problems, "\n"), "--remint"))
				})
			}),

			harness.NewStep("the human listing marks the primary and names the notebook", func(ctx *harness.Context) error {
				cmd := ctx.Bin("notespace", "list")
				result := cmd.Run()
				ctx.ShowCommandOutput("grove notespace list", result.Stdout, result.Stderr)
				if err := ctx.Check("notespace list succeeds", result.AssertSuccess()); err != nil {
					return fmt.Errorf("notespace list: %w\nStderr: %s", err, result.Stderr)
				}
				return ctx.Verify(func(v *verify.Collector) {
					v.Contains("the subject is a group heading", result.Stdout, nsScenarioSubject)
					v.Contains("the recorded primary is marked", result.Stdout, "primary")
					v.Contains("each row names its parent notebook", result.Stdout, "personal")
					v.Contains("including the one the primary is in", result.Stdout, "work")
					// There is no per-row primary flag anywhere in this
					// pipeline: primariness is recorded per SUBJECT and carried
					// on the group. Appendix D principle 1.
					v.NotContains("no per-row primary attribute", result.Stdout, "is_primary")
				})
			}),
		},
	)
}
