# ChainProof

Local, open-source provenance for any AI agent.

ChainProof records agent and harness events in a SHA-256 hash chain that you
own. Use Claude Code, Codex, Kimi, Qwen, a local model, or a custom harness—the
ledger format is deliberately provider-agnostic.

> ChainProof proves continuity of the record it receives. It does not prove
> that an agent reported truthfully or that an imported history is complete.

## Included

- Local SQLite ledger with serialized, append-only writes
- Strict verification of genesis, sequence, links, event hashes, count, and head
- Push API, CLI ingestion, JSONL imports, and wrapped execution
- Izakaya-style Bubble Tea TUI with Tokyo Night and Synthwave '84 themes
- Localhost-only web explorer
- Canonical, implementation-independent proof specification
- No account, tenant, billing, quota, or cloud dependency

## Build and run

```sh
go build -o chainproof ./cmd/chainproof
./chainproof init
./chainproof start --agent research-agent --harness codex --model qwen3-coder
./chainproof ui
```

The database defaults to `~/.chainproof/chainproof.db`. Override it with
`CHAINPROOF_DB=/path/to/ledger.db`.

### Push, import, or wrap

```sh
chainproof append RUN_ID '{"kind":"tool.call","source":{"adapter":"custom","mode":"reported"},"payload":{"tool":"shell"}}'
chainproof ingest RUN_ID < events.jsonl
chainproof run -- claude
chainproof run -- codex
chainproof run -- opencode
```

Run `chainproof serve` for the local explorer at `http://127.0.0.1:7331`.

## Trust vocabulary

- **Observed:** captured directly while execution occurred.
- **Reported:** submitted by an agent or harness.
- **Imported:** collected later from an existing record.
- **Derived:** calculated from recorded events.

These labels describe collection—not truthfulness. The proof format is a v1
draft; see [`spec/provenance-v1.md`](spec/provenance-v1.md).

MIT licensed.
