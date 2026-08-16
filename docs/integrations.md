# Integrating a harness

ChainProof accepts data three ways.

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
