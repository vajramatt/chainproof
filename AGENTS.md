# ChainProof OSS

ChainProof is the MIT-licensed, local-first continuity and provenance ledger for
AI agents. This repository owns durable missions and checkpoints, the Go CLI,
TUI, embedded local web explorer, SQLite data model, agent integrations, proof
formats, and release artifacts.

## Repository boundary

- The public marketing site lives in `../chainproof-site` and deploys to
  `chainproof.ai`. Do not add marketing pages or Cloudflare deployment config
  here.
- ChainProof is not a hosted SaaS product. Do not add accounts, billing,
  hosted storage, tenancy, or SaaS administration here.
- `internal/server/ui/index.html` is the embedded, loopback-only web explorer
  served by `chainproof serve`, not the marketing website.

## Product invariants

- Stay local-first: the core product must work without an account, API key, or
  external service.
- Keep the canonical ledger append-only and independently verifiable.
- Keep mission checkpoints append-only, bind them to exact run-proof prefixes,
  and preserve their separate proof boundary.
- Keep compiled agent context bounded, derived, evidence-linked, and gated on
  successful mission and run-anchor verification.
- Surface post-checkpoint run tails as uncheckpointed recovery metadata; never
  promote them into trusted resumable evidence automatically.
- Do not complete missions with uncheckpointed run tails. Once mission is
  completed, reject new appends to its associated runs.
- Keep queue acquisition atomic: select only active missions without a live
  lease, verify continuity before claim, and return bounded context with token.
- Under independent-process contention, preserve every successful event and
  checkpoint append and serialize every lease transition. Retry bounded SQLite
  contention around whole transactions; do not expose raw lock errors when
  fresh state can yield an ownership, lifecycle, or queue result.
- Keep Agent Work Protocol environment variables harness-neutral. Context files
  must be private, ephemeral, verified before child start, and treated as data
  rather than generic prompt text.
- Keep machine-readable bootstrap idempotent and side-effect boundaries exact:
  capability discovery and diagnostics must not initialize state, while
  initialization must converge on same stable local identity.
- Keep structured CLI errors versioned and single-document JSON. Preserve
  stable exit classes, silence parser diagnostics in JSON mode, and keep
  unknown commands side-effect-free.
- Make user services preserve resolved ledger, identity, and collector paths
  across login. Persist only explicit ChainProof configuration; never copy
  ambient environment variables or credentials into service definitions.
- Keep local agent profiles owner-only and separate stable `agent_id`, readable
  `display_name`, ephemeral `worker_id`, and mission-specific `role`. Never
  describe unsigned attribution metadata as authentication or signed proof.
- Treat partial, corrupt, or mismatched identity material as failure. Never
  silently replace a missing key or reconstruct a missing established profile.
- Bind declared run attribution to canonical event extensions and verify exact
  metadata-to-event agreement without changing provenance mode.
- Describe proof boundaries precisely: a valid hash chain proves that recorded
  evidence has not changed; it does not prove that a reported or imported claim
  was true.
- Preserve provenance modes (`observed`, `reported`, `imported`, `derived`) and
  never present derived evidence as directly observed.
- Keep search explicitly derived and rebuildable. Agents may navigate by search
  hit, but must inspect canonical event before citing payload or proof fields.
- Allow local HTTP services to bind only to loopback and avoid third-party
  browser assets, telemetry, or network requests.
- Keep the shipped product dependency-light and compatible with a single-binary
  release.

## Working conventions

- Read `README.md`, `docs/investigation.md`, and `spec/provenance-v1.md` before
  changing product semantics or the proof format.
- Treat SQLite indexes and UI summaries as rebuildable views; the append-only
  ledger is the source of truth.
- Keep portable proof formats independent from SQLite. Treat Markdown as a
  generated readable view, not canonical multi-agent coordination state.
- Keep full-instance backup and restore non-destructive: verify manifest,
  ledger, and identities before publication, and never overwrite a destination.
- Update tests and documentation with behavior changes.
- Do not edit generated release artifacts directly.

## Agent continuity

- For substantial work, run `chainproof capabilities --json` when binary is
  available. This is side-effect-free.
- Run `chainproof integration show codex|claude-code|openclaw|generic` to load
  exact lifecycle and provenance guidance for current harness.
- Use structured `chainproof search` to find evidence, then `chainproof inspect
  event EVENT_ID` to load canonical record before citing it.
- When `CHAINPROOF_MISSION_ID` is present, read verified context before acting
  and write `chainproof checkpoint --current CHECKPOINT.json` before ending
  useful work.
- Preserve provenance boundary in dogfood records: tool results directly seen
  by ChainProof may be `observed`; agent summaries and judgments are
  `reported`; collected histories remain `imported`.

## Validation

```sh
make build
make test
make check
```

`make check` runs formatting and may modify Go files. Review its diff before
committing. Release creation and publishing require an explicit request.
