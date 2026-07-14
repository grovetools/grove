package depsgraph

import (
	"reflect"
	"testing"
)

func TestTopologicalSort(t *testing.T) {
	tests := []struct {
		name     string
		setup    func() *Graph
		expected [][]string
		wantErr  bool
	}{
		{
			name: "simple linear dependency",
			setup: func() *Graph {
				g := NewGraph()
				g.AddNode(&Node{Name: "A"})
				g.AddNode(&Node{Name: "B"})
				g.AddNode(&Node{Name: "C"})
				g.AddEdge("B", "A") // B depends on A
				g.AddEdge("C", "B") // C depends on B
				return g
			},
			expected: [][]string{{"A"}, {"B"}, {"C"}},
			wantErr:  false,
		},
		{
			name: "parallel dependencies",
			setup: func() *Graph {
				g := NewGraph()
				g.AddNode(&Node{Name: "core"})
				g.AddNode(&Node{Name: "context"})
				g.AddNode(&Node{Name: "proxy"})
				g.AddNode(&Node{Name: "flow"})
				g.AddEdge("context", "core") // context depends on core
				g.AddEdge("proxy", "core")   // proxy depends on core
				g.AddEdge("flow", "context") // flow depends on context
				return g
			},
			expected: [][]string{{"core"}, {"context", "proxy"}, {"flow"}},
			wantErr:  false,
		},
		{
			name: "independent modules",
			setup: func() *Graph {
				g := NewGraph()
				g.AddNode(&Node{Name: "A"})
				g.AddNode(&Node{Name: "B"})
				g.AddNode(&Node{Name: "C"})
				// No dependencies
				return g
			},
			expected: [][]string{{"A", "B", "C"}},
			wantErr:  false,
		},
		{
			// A cycle can no longer be ordered, so its members are grouped into
			// a single level and released together rather than erroring out.
			name: "circular dependency is grouped, not an error",
			setup: func() *Graph {
				g := NewGraph()
				g.AddNode(&Node{Name: "A"})
				g.AddNode(&Node{Name: "B"})
				g.AddEdge("A", "B") // A depends on B
				g.AddEdge("B", "A") // B depends on A (cycle)
				return g
			},
			expected: [][]string{{"A", "B"}},
			wantErr:  false,
		},
		{
			// The real grovetools foundation cycle: core -> tuimux -> compositor
			// -> core, with tend (-> core) and a downstream flow (-> core) hanging
			// off it. The cyclic trio collapses to one level released first, then
			// its dependents in order.
			name: "foundation cycle with dependents",
			setup: func() *Graph {
				g := NewGraph()
				for _, n := range []string{"core", "tuimux", "compositor", "tend", "flow"} {
					g.AddNode(&Node{Name: n})
				}
				g.AddEdge("core", "tuimux")       // core depends on tuimux
				g.AddEdge("tuimux", "compositor") // tuimux depends on compositor
				g.AddEdge("compositor", "core")   // compositor depends on core (cycle)
				g.AddEdge("tend", "core")         // tend depends on core
				g.AddEdge("flow", "core")         // flow depends on core
				return g
			},
			expected: [][]string{{"compositor", "core", "tuimux"}, {"flow", "tend"}},
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := tt.setup()
			result, err := g.TopologicalSort()

			if (err != nil) != tt.wantErr {
				t.Errorf("TopologicalSort() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				// Sort each level for consistent comparison
				for i := range result {
					sortStrings(result[i])
				}
				for i := range tt.expected {
					sortStrings(tt.expected[i])
				}

				if !reflect.DeepEqual(result, tt.expected) {
					t.Errorf("TopologicalSort() = %v, want %v", result, tt.expected)
				}
			}
		})
	}
}

func TestCyclicGroups(t *testing.T) {
	g := NewGraph()
	for _, n := range []string{"core", "tuimux", "compositor", "notify"} {
		g.AddNode(&Node{Name: n})
	}
	g.AddEdge("core", "tuimux")
	g.AddEdge("tuimux", "compositor")
	g.AddEdge("compositor", "core") // core/tuimux/compositor form a cycle
	g.AddEdge("notify", "core")     // notify is acyclic

	if !g.HasCycle() {
		t.Fatal("HasCycle() = false, want true")
	}

	groups := g.CyclicGroups(nil)
	if len(groups) != 1 {
		t.Fatalf("CyclicGroups() returned %d groups, want 1: %v", len(groups), groups)
	}
	if got, want := groups[0], []string{"compositor", "core", "tuimux"}; !reflect.DeepEqual(got, want) {
		t.Errorf("CyclicGroups()[0] = %v, want %v", got, want)
	}

	// An acyclic graph reports no cycles.
	acyclic := NewGraph()
	acyclic.AddNode(&Node{Name: "core"})
	acyclic.AddNode(&Node{Name: "notify"})
	acyclic.AddEdge("notify", "core")
	if acyclic.HasCycle() {
		t.Error("HasCycle() = true for acyclic graph, want false")
	}
	if groups := acyclic.CyclicGroups(nil); len(groups) != 0 {
		t.Errorf("CyclicGroups() = %v for acyclic graph, want none", groups)
	}
}

// Helper function to sort string slices for test comparison
func sortStrings(s []string) {
	// Simple bubble sort for test purposes
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			if s[i] > s[j] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}
