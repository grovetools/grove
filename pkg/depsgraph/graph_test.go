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
			name: "circular dependency",
			setup: func() *Graph {
				g := NewGraph()
				g.AddNode(&Node{Name: "A"})
				g.AddNode(&Node{Name: "B"})
				g.AddEdge("A", "B") // A depends on B
				g.AddEdge("B", "A") // B depends on A (cycle)
				return g
			},
			expected: nil,
			wantErr:  true,
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