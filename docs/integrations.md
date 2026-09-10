# Integrating a harness

ChainProof accepts data through a built-in Codex collector and three generic
integration modes.

## Built-in Codex collector

The default TUI and server discover and follow `~/.codex/sessions/**/*.jsonl`.
Use `chainproof codex sync` for a one-shot import or `chainproof codex watch`
for a collector-only foreground process. `CHAINPROOF_CODEX_ROOT` overrides the
source directory; `CHAINPROOF_CODEX_CONTENT=full` opts into transcript content
instead of the default hashes. Reasoning records are not imported.

Codex session JSONL is a local implementation detail rather than ChainProof's
public protocol. Its parser is versioned as `codex-local-v1`; unknown records
are skipped, and fixtures must accompany parser changes.

## Native Codex mission work

```sh
chainproof codex work --mission MISSION_ID
chainproof codex work --exec --prompt "Continue pending work" -- --model MODEL
```

ChainProof starts an observed Codex execution envelope, compiles verified
mission context, and injects Agent Work Protocol instructions into the initial
prompt. Codex receives the standard `CHAINPROOF_*` environment. The initial
prompt tells it to read context before acting and write
`chainproof checkpoint --current` before ending.

The built-in collector later imports Codex's richer native session as a
separate child run. Its `CHAINPROOF_AGENT_WORK_V1` marker is accepted only when
the referenced parent run exists, uses the Codex harness, and belongs to the
same mission. This links two honest provenance views: observed process
lifecycle and imported native session detail.

`--mission` defaults to the most recently updated active mission. Use
`CHAINPROOF_CODEX_BIN` when the executable is not named `codex`. Put Codex CLI
options after `--`; use ChainProof's `--prompt` for additional user direction.

## Push

Start `chainproof serve`, create a run with `POST /api/runs`, then append events
to `POST /api/runs/{id}/events`. This works from Claude Code hooks, Codex hooks,
Kimi, Qwen, local model runners, and custom programs.

## Pull

Normalize a harness history to one `EventInput` JSON object per line, then run:

```sh
chainproof pull RUN_ID events.jsonl my-harness
```

ChainProof stores a byte cursor per adapter and absolute source path. Re-running
the command imports only appended records. Pulled data is always labeled
`imported`, even if the source file claims otherwise.

## Wrap

```sh
chainproof run -- codex
chainproof run -- claude
chainproof run -- your-agent
```

Wrapping records process lifecycle and exit status as `observed`. Rich tool and
model events still require a hook, push integration, or pull adapter.

## Event shape

```json
{
  "kind": "tool.call",
  "source": { "adapter": "my-harness", "mode": "reported" },
  "payload": { "tool": "shell", "command": "git status" }
}
```

Never label imported history as observed. Preserve native identifiers in
`source.native_event_id`, and put unstable provider fields in `extensions`.
