package plugin

import "github.com/pelletier/go-toml/v2/unstable"

// Declaration order, recovered from the manifest bytes.
//
// `[panel.views.<name>]` is a table of tables, so it decodes into a map and the
// order the author wrote is gone by the time Validate sees it. That order is
// load-bearing: it is the author's preference order, and the host's whole use of
// the `drawer` bool is "the FIRST view declared drawer-suitable" (see
// config.DrawerPaneConfig.EffectiveView). So the order is read back off the
// document and frozen into the array the fragment writes.
//
// The alternative was a separate `preferred = true` key, which is a second way to
// state a preference already implied by the layout of the file — and a second way
// for it to disagree with itself. The other alternative was `[[panel.views]]`
// with an inner `name`, which orders for free but reads worse for the author: a
// view is a named thing, and naming it twice (a header and a key) is the shape
// arrays force, not the shape the declaration wants.
//
// This is the one place in the package that reaches past the decoder into
// go-toml's `unstable` parser, which is exactly what it is for — walking a
// document's expressions in the order they appear. It is isolated in this file so
// an upstream change to that API is one file to repair, and it is only ever a
// refinement of a value the package could also do without: an unrecoverable order
// degrades to sorted names (see Panel.ViewNames), never to a validation failure.

// viewOrder returns the names under `panel.views` in the order the document
// first mentions each of them.
//
// Every spelling TOML allows for the same table is handled, because the author
// picks the spelling and the host must not reward one over another: a
// `[panel.views.compact]` header, a `compact.drawer = true` line under
// `[panel.views]`, a fully dotted `panel.views.compact.drawer` under `[panel]`,
// and an inline `views = { compact = { drawer = true } }`.
//
// Parse errors are ignored rather than reported: this runs after the decoder has
// already accepted the bytes, so a failure here would be an upstream
// disagreement about valid TOML, and the caller has a working fallback.
func viewOrder(data []byte) []string {
	var (
		p      unstable.Parser
		prefix []string
		order  []string
	)
	seen := make(map[string]bool)
	record := func(path []string) {
		if len(path) < 3 || path[0] != "panel" || path[1] != "views" {
			return
		}
		if name := path[2]; !seen[name] {
			seen[name] = true
			order = append(order, name)
		}
	}

	p.Reset(data)
	for p.NextExpression() {
		expr := p.Expression()
		switch expr.Kind {
		case unstable.Table, unstable.ArrayTable:
			// A header replaces the prefix every following key-value sits under.
			prefix = nodeKeyPath(expr, nil)
			record(prefix)
		case unstable.KeyValue:
			path := nodeKeyPath(expr, prefix)
			record(path)
			recordInlineKeys(record, path, expr.Value())
		}
	}
	return order
}

// nodeKeyPath is a node's own (possibly dotted) key, appended to the table
// header it sits under. The key parts are copied into fresh strings because the
// parser reuses its node storage on the next expression.
func nodeKeyPath(n *unstable.Node, prefix []string) []string {
	path := make([]string, 0, len(prefix)+2)
	path = append(path, prefix...)
	for it := n.Key(); it.Next(); {
		path = append(path, string(it.Node().Data))
	}
	return path
}

// recordInlineKeys descends an inline table, so `views = { compact = { ... } }`
// orders its members the same way headers do.
func recordInlineKeys(record func([]string), path []string, value *unstable.Node) {
	if value == nil || value.Kind != unstable.InlineTable {
		return
	}
	for it := value.Children(); it.Next(); {
		kv := it.Node()
		if kv.Kind != unstable.KeyValue {
			continue
		}
		child := nodeKeyPath(kv, path)
		record(child)
		recordInlineKeys(record, child, kv.Value())
	}
}
