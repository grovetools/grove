# Contributing

We use the parallel `grove` orchestrator to manage local state across 20+ repositories.

## Developer Loop
1. Run `grove fmt` to auto-format.
2. Run `grove test` for fast, wave-sorted unit tests.
3. Run `grove check --affected` as your pre-push gate (runs fmt-check, vet, lint, and unit tests using daemon-backed caching).
4. Run `grove status` to visualize your ecosystem state.

