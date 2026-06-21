package orchestrator

import (
	"testing"

	"github.com/grovetools/core/config"
)

type edge struct {
	consumer string
	deps     []string
}

func edgeOf(e edge) (string, []string) { return e.consumer, e.deps }

// TestApplyBuildAfterEdges_AddsEdgesAndOrdersWaves verifies that derived edges
// land in BuildAfter and that the resulting waves schedule the provider first.
func TestApplyBuildAfterEdges_AddsEdgesAndOrdersWaves(t *testing.T) {
	jobs := []TaskJob{
		{Name: "compositor"},
		{Name: "nav"},
		{Name: "core"}, // unrelated, does not link compositor
	}
	configs := map[string]*config.Config{
		"compositor": {Name: "compositor"},
		"nav":        {Name: "nav"},
		"core":       {Name: "core"},
	}
	items := []edge{
		{consumer: "nav", deps: []string{"compositor"}},
		{consumer: "core", deps: nil},
	}

	applyBuildAfterEdges(configs, items, edgeOf)

	if got := configs["nav"].BuildAfter; len(got) != 1 || got[0] != "compositor" {
		t.Fatalf("nav.BuildAfter = %v, want [compositor]", got)
	}
	if got := configs["core"].BuildAfter; len(got) != 0 {
		t.Fatalf("core.BuildAfter = %v, want []", got)
	}

	waves := SortIntoWaves(jobs, configs)
	compWave, navWave := -1, -1
	for i, wave := range waves {
		for _, j := range wave {
			switch j.Name {
			case "compositor":
				compWave = i
			case "nav":
				navWave = i
			}
		}
	}
	if compWave == -1 || navWave == -1 {
		t.Fatalf("missing jobs in waves: comp=%d nav=%d (%v)", compWave, navWave, waves)
	}
	if compWave >= navWave {
		t.Fatalf("compositor wave %d should precede nav wave %d", compWave, navWave)
	}
}

// TestApplyBuildAfterEdges_CreatesConfigForUnloadedConsumer verifies that a
// consumer with no loaded config still receives the edge via a synthesized
// config so the dependency survives into SortIntoWaves.
func TestApplyBuildAfterEdges_CreatesConfigForUnloadedConsumer(t *testing.T) {
	configs := map[string]*config.Config{
		"compositor": {Name: "compositor"},
		// nb has no config (load error)
	}
	items := []edge{{consumer: "nb", deps: []string{"compositor"}}}

	applyBuildAfterEdges(configs, items, edgeOf)

	cfg, ok := configs["nb"]
	if !ok || cfg == nil {
		t.Fatalf("expected synthesized config for nb")
	}
	if len(cfg.BuildAfter) != 1 || cfg.BuildAfter[0] != "compositor" {
		t.Fatalf("nb.BuildAfter = %v, want [compositor]", cfg.BuildAfter)
	}
}

// TestApplyBuildAfterEdges_DedupesAndSkipsSelf verifies that duplicate and
// self-referential deps are ignored.
func TestApplyBuildAfterEdges_DedupesAndSkipsSelf(t *testing.T) {
	configs := map[string]*config.Config{
		"nav": {Name: "nav", BuildAfter: []string{"compositor"}},
	}
	items := []edge{{consumer: "nav", deps: []string{"compositor", "nav"}}}

	applyBuildAfterEdges(configs, items, edgeOf)

	if got := configs["nav"].BuildAfter; len(got) != 1 || got[0] != "compositor" {
		t.Fatalf("nav.BuildAfter = %v, want [compositor] (deduped, no self)", got)
	}
}

// TestConsumedProviders_FallsBackToAllOnGoListError verifies the conservative
// fallback: when `go list` fails (here, a directory with no Go module), the
// member is treated as depending on every provider.
func TestConsumedProviders_FallsBackToAllOnGoListError(t *testing.T) {
	providerModule := map[string]string{"github.com/grovetools/compositor": "compositor"}
	allProviders := map[string]string{"compositor": "github.com/grovetools/compositor"}

	deps := consumedProviders(t.TempDir(), providerModule, allProviders)
	if len(deps) != 1 || deps[0] != "compositor" {
		t.Fatalf("consumedProviders fallback = %v, want [compositor]", deps)
	}
}
