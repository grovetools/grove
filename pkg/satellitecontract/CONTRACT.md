# Full Tart satellite contract v1

This package freezes Phase 0 contracts. It does not expose full Tart mode or perform VM operations.

## Capability document

`CapabilityStatus` is additive JSON with `schema_version: 1`. It reports:

- stock runtime and managed package versions/platform/hash;
- auth presence, type, and optional refresh-capable `usable` status (never credential values);
- trust, policy, and guard health while naming `tart-vm` as the only isolation boundary;
- record-return generation, cleanliness, and verified escrow metadata;
- bounded artifact-fetch support.

Unknown/malformed enum values and contradictory auth/escrow metadata fail validation.

## Destructive maintenance

The normal path is `active -> draining -> quiesced -> deleting`. Dirty state adds `quiesced -> escrowed -> deleting`. Drain failures enter `failed`; deletion is not reachable directly from `active`, `draining`, or `failed`.

Immediately before provider deletion, the implementation must fetch final status and call `DeletionAllowed` with the initially reviewed status and final status. Unknown state and generation changes fail closed even with a destructive override. Dirty state requires a durable hash-verified escrow or explicit satellite-specific override.

Exit codes are fixed: dirty `20`, unknown/generation race `21`, maintenance failure `22`, storage preflight `23`, usage `64`.

Exact override warning:

> WARNING: this permanently deletes unreturned notebook changes and guest-local Pi credentials; it does not revoke upstream OAuth credentials or guarantee secure erasure.

The confirmation phrase is `discard unreturned record changes for satellite "<name>"`.

## Package activation

The archive SHA-256 selects `<store-root>/sha256/<hash>/package`. Managed Pi settings reference that absolute path. Activation writes and fsyncs a complete same-directory `settings.json.staged-<operation>`, preserves the prior bytes as `settings.json.previous`, then atomically renames the staged file. Any validation/reload failure restores `.previous` by atomic rename. Package directories are immutable after verification; settings never point at a mutable version alias.

## Capacity and mount identity

Host and guest budgets are calculated and checked independently as payload + expected growth + reserved headroom, with overflow rejected. Before any `tart clone`, host facts must prove:

1. `/Volumes/solot7` is a real mounted filesystem;
2. its persisted volume/device identity matches operator configuration;
3. `TART_HOME` is exactly `/Volumes/solot7/tart` (no fallback);
4. the volume is writable; and
5. available bytes meet the calculated host budget.

Guest free space is checked separately before notebook/worktree hydration against the guest budget. Phase 1 will connect these pure contracts to provider operations.
