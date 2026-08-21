# ChainProof OSS

ChainProof is the MIT-licensed, local-first provenance ledger for AI agents. This
repository owns the Go CLI, TUI, embedded local web explorer, SQLite data model,
agent integrations, proof format, and release artifacts.

## Repository boundary

- The public marketing site lives in `../chainproof-site` and deploys to
  `chainproof.ai`. Do not add marketing pages or Cloudflare deployment config
  here.
- The hosted multi-tenant product lives in `../chainproof-saas`. Do not add
  accounts, billing, hosted storage, or SaaS administration here.
- `site/index.html` is part of the OSS application. It is the embedded,
  loopback-only web explorer served by `chainproof serve`, not the marketing
  website.

## Product invariants

- Stay local-first: the core product must work without an account, API key, or
  external service.
- Keep the canonical ledger append-only and independently verifiable.
- Describe proof boundaries precisely: a valid hash chain proves that recorded
  evidence has not changed; it does not prove that a reported or imported claim
  was true.
- Preserve provenance modes (`observed`, `reported`, `imported`, `derived`) and
  never present derived evidence as directly observed.
- Bind local HTTP services to loopback by default and avoid third-party browser
  assets, telemetry, or network requests.
- Keep the shipped product dependency-light and compatible with a single-binary
  release.

## Working conventions

- Read `README.md`, `docs/investigation.md`, and `spec/provenance-v1.md` before
  changing product semantics or the proof format.
- Treat SQLite indexes and UI summaries as rebuildable views; the append-only
  ledger is the source of truth.
- Update tests and documentation with behavior changes.
- Do not edit generated release artifacts directly.

## Validation

```sh
make build
make test
make check
```

`make check` runs formatting and may modify Go files. Review its diff before
committing. Release creation and publishing require an explicit request.
