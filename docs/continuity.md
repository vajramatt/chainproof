# Durable agent continuity

ChainProof missions preserve useful state across model sessions without
turning a transcript or summary into unqualified truth. Runs remain canonical
provenance evidence. Checkpoints form a second hash chain that cites exact run
proofs and carries compact resumable state.

## Start a mission

```sh
chainproof mission start \
  --agent builder \
  --objective "Ship durable continuity"
```

Save the returned `mission_id`. A mission persists until explicitly completed.
`chainproof resume` without an ID selects the most recently updated active
mission.

## Work inside the mission

Start a run manually:

```sh
chainproof start --mission MISSION_ID --harness codex --model MODEL
```

Or wrap an agent process:

```sh
chainproof run --mission MISSION_ID -- codex
```

The mission's agent name is inherited unless `--agent` explicitly overrides
it. Run metadata records `mission_id`; provenance events keep their existing v1
shape and proof semantics.

## Create a checkpoint

```sh
chainproof checkpoint MISSION_ID RUN_ID '{
  "summary": "Storage and API tests pass",
  "next_actions": ["add mission UI", "test restart recovery"],
  "blockers": [],
  "evidence": [
    {"event_id": "EVENT_ID", "note": "test command completed"}
  ]
}'
```

`summary` is required. Evidence IDs must belong to the anchored run prefix.
ChainProof captures the run's current entry count and chain head inside the
same SQLite transaction that appends the checkpoint.

Checkpoint state defaults to `source.mode: reported`. Set an explicit valid
provenance mode and adapter when another collection path applies. The source is
part of hashed checkpoint bytes.

## Resume safely

```sh
chainproof resume MISSION_ID
chainproof resume
```

Resume output contains the mission, latest checkpoint, and verification result.
Treat `summary`, `next_actions`, and `blockers` as agent-authored state. Their
integrity is verifiable; their correctness is not.

Mission objectives, summaries, next actions, blockers, and evidence notes are
stored as full local content. Do not place secrets in checkpoint state.

An agent should refuse inherited state when `verification.valid` is false.

## Export session proof

```sh
chainproof mission export MISSION_ID continuity-proof.json
chainproof verify-continuity-file continuity-proof.json
```

The portable bundle contains every checkpoint plus the exact provenance run
prefix anchored by each checkpoint. Verification needs no database or hosted
ChainProof service.

## Complete the mission

```sh
chainproof mission complete MISSION_ID
```

Completion requires at least one valid checkpoint. This prevents an empty
mission from being marked complete without resumable or reviewable evidence.

## Local API

```text
POST /api/missions
POST /api/missions/{mission_id}/checkpoints
GET  /api/missions/{mission_id}/resume
POST /api/runs   {"mission_id":"MISSION_ID", ...}
```

Checkpoint requests accept `run_id`, `summary`, `next_actions`, `blockers`,
`evidence`, and `extensions`. The API remains loopback-only by default and has
no multi-user authentication.

## Architecture boundary

```text
mission objective
      │
      ▼
checkpoint 0 ──hash──▶ checkpoint 1 ──hash──▶ mission head
      │                         │
      ▼                         ▼
run A proof prefix            run B proof prefix
```

Canonical run events and checkpoints are append-only proof material. Search
rows, UI summaries, relevance scores, and future context compilations are
derived views. Derived memory must retain links to canonical evidence and must
never be presented as directly observed.

Continuity v1 includes one complete anchored run prefix per checkpoint in a
portable bundle. This keeps offline verification small in concept and free of
database assumptions, but repeated checkpoints against one long run can repeat
events and increase bundle size. A future bundle format may deduplicate run
proofs by anchor without changing checkpoint hashes.
