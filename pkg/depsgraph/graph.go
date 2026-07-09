package depsgraph

import (
	"fmt"
	"sort"

	"github.com/sirupsen/logrus"
)

// Node represents a module in the dependency graph
type Node struct {
	Name    string   // Module name (e.g., "grove-core")
	Path    string   // Full module path (e.g., "github.com/grovetools/core")
	Dir     string   // Directory path
	Deps    []string // Direct dependencies (module paths)
	Version string   // Current version (if known)
}

// Graph represents the dependency graph of all modules
type Graph struct {
	nodes    map[string]*Node    // Key is module name
	edges    map[string][]string // Adjacency list: module -> dependencies
	revEdges map[string][]string // Reverse edges: module -> dependents
}

// NewGraph creates a new dependency graph
func NewGraph() *Graph {
	return &Graph{
		nodes:    make(map[string]*Node),
		edges:    make(map[string][]string),
		revEdges: make(map[string][]string),
	}
}

// AddNode adds a node to the graph
func (g *Graph) AddNode(node *Node) {
	g.nodes[node.Name] = node
}

// AddEdge adds a directed edge from 'from' to 'to' (from depends on to)
func (g *Graph) AddEdge(from, to string) {
	g.edges[from] = append(g.edges[from], to)
	g.revEdges[to] = append(g.revEdges[to], from)
}

// GetNode returns a node by name
func (g *Graph) GetNode(name string) (*Node, bool) {
	node, exists := g.nodes[name]
	return node, exists
}

// BuildGraph builds a dependency graph from the workspace
func BuildGraph(rootDir string, workspaces []string) (*Graph, error) {
	// Use the new builder with a default logger
	logger := logrus.New()
	logger.SetLevel(logrus.WarnLevel) // Only show warnings and errors

	builder := NewBuilder(workspaces, logger)
	return builder.Build()
}

// TopologicalSort performs a topological sort of the graph using Kahn's algorithm
// Returns modules grouped by levels that can be released in parallel
func (g *Graph) TopologicalSort() ([][]string, error) {
	return g.TopologicalSortWithFilter(nil)
}

// TopologicalSortWithFilter performs a topological sort on a subset of nodes,
// returning modules grouped into levels that can be released in parallel
// (dependencies first). If nodesToConsider is nil, the entire graph is sorted.
//
// Go module require graphs may legitimately contain cycles (Go only forbids
// package-level import cycles, not module-level ones): e.g. core requires
// tuimux, tuimux requires compositor, compositor requires core. A plain
// topological sort has no root to start from and cannot order such a graph. To
// stay robust, we condense each strongly-connected component into a single
// super-node (Tarjan), topologically order the resulting DAG, and emit every
// member of a cycle into the same level — mutually-cyclic modules are released
// together, since no ordering among them is possible or needed. Use
// CyclicGroups to report which modules were grouped this way.
func (g *Graph) TopologicalSortWithFilter(nodesToConsider map[string]bool) ([][]string, error) {
	nodesToProcess := g.resolveNodeSet(nodesToConsider)

	// If no nodes to process, return empty result
	if len(nodesToProcess) == 0 {
		return [][]string{}, nil
	}

	// Condense strongly-connected components so cycles collapse to super-nodes.
	sccs := g.stronglyConnectedComponents(nodesToProcess)
	compOf := make(map[string]int, len(nodesToProcess))
	for i, scc := range sccs {
		for _, name := range scc {
			compOf[name] = i
		}
	}

	// Build the condensation DAG: super-node -> set of super-nodes it depends on,
	// plus reverse edges and in-degrees. Intra-SCC edges are ignored.
	deps := make([]map[int]bool, len(sccs))
	dependents := make([][]int, len(sccs))
	inDegree := make([]int, len(sccs))
	for i := range sccs {
		deps[i] = make(map[int]bool)
	}
	for name := range nodesToProcess {
		ci := compOf[name]
		for _, dep := range g.edges[name] {
			if !nodesToProcess[dep] {
				continue
			}
			cj := compOf[dep]
			if ci == cj || deps[ci][cj] {
				continue
			}
			deps[ci][cj] = true
			inDegree[ci]++
			dependents[cj] = append(dependents[cj], ci)
		}
	}

	// Kahn's algorithm over the (guaranteed acyclic) condensation.
	var queue []int
	for i := range sccs {
		if inDegree[i] == 0 {
			queue = append(queue, i)
		}
	}

	var result [][]string
	processed := 0
	for len(queue) > 0 {
		var level []string
		var next []int
		for _, super := range queue {
			level = append(level, sccs[super]...)
			processed++
			for _, dep := range dependents[super] {
				inDegree[dep]--
				if inDegree[dep] == 0 {
					next = append(next, dep)
				}
			}
		}
		// Order within a level is irrelevant (parallel); sort for determinism.
		sort.Strings(level)
		result = append(result, level)
		queue = next
	}

	// The condensation of any graph is acyclic, so every super-node must be
	// processed. A shortfall would indicate a bug in the SCC computation.
	if processed != len(sccs) {
		return nil, fmt.Errorf("internal error: condensation not acyclic (processed %d of %d components)", processed, len(sccs))
	}

	return result, nil
}

// resolveNodeSet returns the set of node names to operate on: a copy of the
// whole graph when nodesToConsider is nil, otherwise the caller's set verbatim.
func (g *Graph) resolveNodeSet(nodesToConsider map[string]bool) map[string]bool {
	if nodesToConsider != nil {
		return nodesToConsider
	}
	all := make(map[string]bool, len(g.nodes))
	for name := range g.nodes {
		all[name] = true
	}
	return all
}

// stronglyConnectedComponents computes the SCCs of the subgraph induced by
// nodesToProcess using Tarjan's algorithm. Nodes are visited in sorted order so
// the result is deterministic. A single node with a self-loop, or any set of
// mutually-reachable nodes, forms one component.
func (g *Graph) stronglyConnectedComponents(nodesToProcess map[string]bool) [][]string {
	index := 0
	indices := make(map[string]int)
	lowlink := make(map[string]int)
	onStack := make(map[string]bool)
	var stack []string
	var sccs [][]string

	var strongconnect func(v string)
	strongconnect = func(v string) {
		indices[v] = index
		lowlink[v] = index
		index++
		stack = append(stack, v)
		onStack[v] = true

		for _, w := range g.edges[v] {
			if !nodesToProcess[w] {
				continue
			}
			if _, seen := indices[w]; !seen {
				strongconnect(w)
				if lowlink[w] < lowlink[v] {
					lowlink[v] = lowlink[w]
				}
			} else if onStack[w] {
				if indices[w] < lowlink[v] {
					lowlink[v] = indices[w]
				}
			}
		}

		if lowlink[v] == indices[v] {
			var scc []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			sort.Strings(scc)
			sccs = append(sccs, scc)
		}
	}

	order := make([]string, 0, len(nodesToProcess))
	for name := range nodesToProcess {
		order = append(order, name)
	}
	sort.Strings(order)
	for _, name := range order {
		if _, seen := indices[name]; !seen {
			strongconnect(name)
		}
	}

	return sccs
}

// CyclicGroups returns the sets of mutually-dependent modules (strongly-connected
// components of size > 1, or single nodes with a self-loop) within the given
// node set. TopologicalSortWithFilter releases each such group in a single level;
// callers can use this to warn the user that a genuine dependency cycle exists.
func (g *Graph) CyclicGroups(nodesToConsider map[string]bool) [][]string {
	nodesToProcess := g.resolveNodeSet(nodesToConsider)
	if len(nodesToProcess) == 0 {
		return nil
	}

	var groups [][]string
	for _, scc := range g.stronglyConnectedComponents(nodesToProcess) {
		if len(scc) > 1 {
			groups = append(groups, scc)
			continue
		}
		// A single node is cyclic only if it depends on itself.
		name := scc[0]
		for _, dep := range g.edges[name] {
			if dep == name {
				groups = append(groups, scc)
				break
			}
		}
	}
	return groups
}

// GetDependencies returns the direct dependencies of a module
func (g *Graph) GetDependencies(module string) []string {
	return g.edges[module]
}

// GetDependents returns the modules that depend on the given module
func (g *Graph) GetDependents(module string) []string {
	return g.revEdges[module]
}

// HasCycle reports whether the graph contains a dependency cycle.
func (g *Graph) HasCycle() bool {
	return len(g.CyclicGroups(nil)) > 0
}

// GetAllNodes returns all nodes in the graph
func (g *Graph) GetAllNodes() map[string]*Node {
	return g.nodes
}
