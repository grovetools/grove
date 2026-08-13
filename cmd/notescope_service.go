package cmd

import (
	"bytes"
	"context"

	"github.com/grovetools/core/pkg/devicekey"
	"github.com/grovetools/core/pkg/syncproto"
	"github.com/grovetools/grove/pkg/notescope"
)

// The config TUI's P3 pages, wired to the verbs themselves.
//
// The Notes page's `m` and the join-delta page's `p`/`s` are not a second
// implementation of move, pull and share, and they are not a subprocess call
// either: they run runNotespaceMove, runNotebookPull and runNotebookShare in
// this process, with the same arguments the CLI parses into, and display the
// evidence those functions print. A refusal an operator would have read on a
// terminal is the refusal the page shows — including the ones that make this
// phase safe, like pull's missing-root refusal and every verb's D8 duplicate
// preflight.
//
// It is registered rather than imported because grove's TUI packages must not
// import cmd (grove/cmd already imports grove/pkg/tui/config, so the edge only
// goes one way), and because a test needs to drive every keypath against a
// fake that touches no server.

func init() {
	notescope.RegisterService(func() (notescope.Service, error) { return notescopeVerbs{}, nil })
}

// notescopeVerbs is stateless: each call re-reads config and re-establishes the
// device session exactly as the CLI invocation would, so a page that has been
// open across a config edit cannot act on what it read an hour ago.
type notescopeVerbs struct{}

var _ notescope.Service = notescopeVerbs{}

func (notescopeVerbs) Inventory(ctx context.Context) (syncproto.InventoryResponse, error) {
	client, err := loadDeviceSessionHTTP(ctx)
	if err != nil {
		return syncproto.InventoryResponse{}, err
	}
	key, err := devicekey.Load()
	if err != nil {
		return syncproto.InventoryResponse{}, err
	}
	inventory, err := fetchServerInventory(ctx, client, key.DeviceID())
	if err != nil {
		return syncproto.InventoryResponse{}, err
	}
	return inventory.Response, nil
}

func (notescopeVerbs) Share(ctx context.Context, notebook string) (notescope.ActionResult, error) {
	var out bytes.Buffer
	err := runNotebookShare(ctx, &out, notebook, false)
	return notescope.ActionResult{Action: "notebook share " + notebook, Output: out.String()}, err
}

func (notescopeVerbs) Pull(ctx context.Context, notebook string) (notescope.ActionResult, error) {
	var out bytes.Buffer
	err := runNotebookPull(ctx, &out, notebook, false)
	return notescope.ActionResult{Action: "notebook pull " + notebook, Output: out.String()}, err
}

func (notescopeVerbs) Move(ctx context.Context, notespaceRef, toNotebook string) (notescope.ActionResult, error) {
	var out bytes.Buffer
	err := runNotespaceMove(ctx, &out, notespaceMoveOptions{notespace: notespaceRef, to: toNotebook})
	return notescope.ActionResult{Action: "notespace move " + notespaceRef + " --to " + toNotebook, Output: out.String()}, err
}
