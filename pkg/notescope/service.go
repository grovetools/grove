package notescope

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/grovetools/core/pkg/syncproto"
)

// The seam between a page that renders the scope model and the verbs that
// change it.
//
// The TUI must not re-implement share, pull or move, and it must not shell out
// to them either: a second implementation drifts, and a subprocess turns the
// verb's structured refusals into an exit code and a blob of text. So the pages
// call the SAME functions `grove notebook share|pull` and `grove notespace
// move` call, through this interface, and what they display is the evidence
// those verbs printed.
//
// The interface exists (rather than a direct import) for two reasons: grove's
// cmd package owns the device session, and importing cmd from a TUI package
// would be a cycle; and a test must be able to drive every keypath with a fake
// that touches no server and no live config.

// ActionResult is one verb's run: what it was, and the evidence it printed.
type ActionResult struct {
	// Action names the verb, e.g. "notebook share".
	Action string
	// Output is the verb's own rendered evidence, verbatim. It is what the
	// operator would have seen on a terminal, and the TUI shows it rather than
	// summarizing it — "shared 12 notespaces" is not evidence.
	Output string
}

// Summary is the last non-empty line of the evidence, for a status bar that
// has one line to spend. The tail is the verb's conclusion; the head is its
// working.
func (r ActionResult) Summary() string {
	lines := strings.Split(r.Output, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if trimmed := strings.TrimSpace(lines[i]); trimmed != "" {
			return trimmed
		}
	}
	return r.Action
}

// Service is the set of acts the P3 pages reach for. Every method is
// keypress-initiated; nothing here is called on Init, Focus or Refresh.
type Service interface {
	// Inventory answers GET /sync/inventory through this machine's device
	// session. It is a read, but it is still only called from a keypress: a
	// page that fetched on focus would talk to a server nobody asked it to.
	Inventory(ctx context.Context) (syncproto.InventoryResponse, error)
	// Share runs `grove notebook share <name>`.
	Share(ctx context.Context, notebook string) (ActionResult, error)
	// Pull runs `grove notebook pull <name>`.
	Pull(ctx context.Context, notebook string) (ActionResult, error)
	// Move runs `grove notespace move <ns> --to <notebook>`.
	Move(ctx context.Context, notespaceRef, toNotebook string) (ActionResult, error)
}

// ErrNoService is returned by ResolveService in a host that registered none.
// It names the verbs rather than apologizing: the acts exist, this binary just
// does not carry them.
var ErrNoService = errors.New("this host provides no notebook scope service; run these acts as `grove notebook share|pull <name>` or `grove notespace move <ns> --to <notebook>`")

var (
	serviceMu       sync.RWMutex
	serviceProvider func() (Service, error)
)

// RegisterService records how this binary builds a Service. grove's cmd
// package calls it from init; a host that cannot build one (no device session
// of its own) registers nothing, and the pages say so instead of pretending.
func RegisterService(provider func() (Service, error)) {
	serviceMu.Lock()
	defer serviceMu.Unlock()
	serviceProvider = provider
}

// ServiceRegistered reports whether this binary carries the acts at all,
// without building anything.
//
// It exists so a page can say so BEFORE the keypress. A host that links the
// pages but not the verbs — treemux's embedded config panel is the one in the
// tree today — otherwise renders `p pulls · s shares` exactly like the host
// where those keys work, and the operator learns the difference only by
// pressing. Advertising an act a binary cannot perform is the same defect as
// performing one nobody asked for, read from the other end.
func ServiceRegistered() bool {
	serviceMu.RLock()
	defer serviceMu.RUnlock()
	return serviceProvider != nil
}

// ResolveService builds the registered Service, or returns ErrNoService.
func ResolveService() (Service, error) {
	serviceMu.RLock()
	provider := serviceProvider
	serviceMu.RUnlock()
	if provider == nil {
		return nil, ErrNoService
	}
	return provider()
}
