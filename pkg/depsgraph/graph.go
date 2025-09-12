package depsgraph

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sirupsen/logrus"
)

// Node represents a module in the dependency graph
type Node struct {
	Name    string   // Module name (e.g., "grove-core")
	Path    string   // Full module path (e.g., "github.com/mattsolo1/grove-core")
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

// TopologicalSortWithFilter performs a topological sort on a subset of nodes
// If nodesToConsider is nil, sorts the entire graph
// If nodesToConsider is provided, only considers dependencies within that set
func (g *Graph) TopologicalSortWithFilter(nodesToConsider map[string]bool) ([][]string, error) {
	// Determine which nodes to process
	var nodesToProcess map[string]bool
	if nodesToConsider == nil {
		// Process all nodes
		nodesToProcess = make(map[string]bool)
		for name := range g.nodes {
			nodesToProcess[name] = true
		}
	} else {
		nodesToProcess = nodesToConsider
	}

	// If no nodes to process, return empty result
	if len(nodesToProcess) == 0 {
		return [][]string{}, nil
	}

	// Calculate in-degrees (number of dependencies each node has)
	// Only count dependencies that are also in nodesToProcess
	inDegree := make(map[string]int)
	for name := range nodesToProcess {
		count := 0
		for _, dep := range g.edges[name] {
			if nodesToProcess[dep] {
				count++
			}
		}
		inDegree[name] = count
	}

	// Find all nodes with no dependencies (within the considered set)
	var queue []string
	for name, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, name)
		}
	}

	var result [][]string
	processed := 0

	// Process nodes level by level
	for len(queue) > 0 {
		// All nodes in current queue can be processed in parallel
		currentLevel := make([]string, len(queue))
		copy(currentLevel, queue)
		result = append(result, currentLevel)
		processed += len(currentLevel)

		// Prepare next level
		var nextQueue []string
		for _, node := range queue {
			// For each module that depends on the current node
			if dependents, ok := g.revEdges[node]; ok {
				for _, dep := range dependents {
					// Only process if dependent is in our set
					if nodesToProcess[dep] {
						inDegree[dep]--
						if inDegree[dep] == 0 {
							nextQueue = append(nextQueue, dep)
						}
					}
				}
			}
		}
		queue = nextQueue
	}

	// Check for cycles (only within the nodes we're processing)
	if processed != len(nodesToProcess) {
		// Identify nodes in the cycle
		var cycleNodes []string
		for name, degree := range inDegree {
			if degree > 0 {
				cycleNodes = append(cycleNodes, name)
			}
		}
		sort.Strings(cycleNodes)
		return nil, fmt.Errorf("dependency cycle detected among modules: [%s]", strings.Join(cycleNodes, ", "))
	}

	return result, nil
}

// GetDependencies returns the direct dependencies of a module
func (g *Graph) GetDependencies(module string) []string {
	return g.edges[module]
}

// GetDependents returns the modules that depend on the given module
func (g *Graph) GetDependents(module string) []string {
	return g.revEdges[module]
}

// HasCycle checks if the graph contains a cycle
func (g *Graph) HasCycle() bool {
	_, err := g.TopologicalSort()
	return err != nil
}

// GetAllNodes returns all nodes in the graph
func (g *Graph) GetAllNodes() map[string]*Node {
	return g.nodes
}
