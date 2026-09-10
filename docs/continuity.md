# Durable agent continuity

ChainProof missions preserve useful state across model sessions without
turning a transcript or summary into unqualified truth. Runs remain canonical
provenance evidence. Checkpoints form a second hash chain that cites exact run
proofs and carries compact resumable state.

## Start a mission

```sh
chainproof mission start \
  --role implementer \
  --objective "Ship durable continuity"
```

On the first agent-aware command, ChainProof creates the selected local profile.
An agent can do this explicitly and inspect its public identity:

```sh
chainproof agent ensure --profile builder --name Builder --harness codex
CHAINPROOF_AGENT_PROFILE=builder chainproof whoami
CHAINPROOF_AGENT_PROFILE=builder chainproof agent rename --name Builder-2
```

The profile contains a stable key-derived `agent_id` and readable
`display_name`. Rename changes the display name without changing the key or
stable ID; prior ledger records preserve the name recorded when written.
`CHAINPROOF_AGENT_PROFILE` selects among agents sharing one database. A new
`worker_id` identifies each run session. `role` is attached to work within a
mission, not permanently stored as profile identity.

Save the returned `mission_id`. A mission persists until explicitly completed.
`chainproof resume` without an ID selects the most recently updated active
mission.

Discover existing work without retaining IDs outside ChainProof:

```sh
chainproof mission list --status active
chainproof mission acquire --ttl 30m --max-evidence 20
```

Acquisition atomically selects oldest available active mission, verifies its
checkpoint chain and every anchored run prefix, writes lease claim, then
returns mission, lease, and bounded context in one JSON envelope. Missions with
live leases are skipped; expired leases are reclaimable. Invalid continuity
stops acquisition without leaving a lease.

## Coordinate competing agents

Acquire an expiring lease before assigning one mission to a worker:

```sh
chainproof mission claim MISSION_ID --ttl 30m
chainproof mission lease MISSION_ID --history
```

Only the current lease token can renew, release, or hand off ownership:

```sh
chainproof mission renew MISSION_ID LEASE_ID --ttl 30m
chainproof mission handoff MISSION_ID LEASE_ID --to worker-b --ttl 30m
chainproof mission release MISSION_ID LEASE_ID
```

Handoff atomically replaces the token, preventing the prior holder from
renewing or releasing the new lease. An expired lease cannot be renewed and a
new worker may claim the mission, providing crash recovery without manual
database repair. TTL must be between one second and 24 hours.

Lease transitions are retained as append-only local coordination history.
They are not hashed, exported in continuity bundles, or treated as agent
identity proof. Checkpoints and anchored run prefixes remain the cryptographic
continuity boundary. Omitted holders default to current stable `agent_id`;
explicit holder strings remain available for external schedulers.

`chainproof context` includes latest lease plus `lease_active`, allowing agent
harnesses to reject conflicting work before acting. v1 uses local wall-clock
expiry and serialized SQLite transactions. Multi-host coordination, clock
authority, and signed holder identity remain future protocol work.

## Work inside the mission

Start a run manually:

```sh
chainproof start --mission MISSION_ID --harness codex --model MODEL
```

Or wrap an agent process:

```sh
chainproof run --mission MISSION_ID -- codex
```

For native Codex continuity, use:

```sh
chainproof codex work --mission MISSION_ID
chainproof codex work --mission MISSION_ID --holder worker-a --lease-ttl 30m --exec --prompt "Continue pending work" -- --model MODEL
chainproof codex work --acquire --holder queue-worker --exec --prompt "Continue mission" -- --model MODEL
```

This adds verified-context and checkpoint instructions to Codex's initial
prompt. Options after `--` pass through to Codex. Omitting `--mission` selects
the most recently updated active mission. The runner verifies continuity before
claiming an expiring lease, renews it at half-TTL intervals, exposes
`CHAINPROOF_LEASE_ID`, and releases its token after Codex exits. If Codex hands
ownership to another holder, the runner preserves that handoff.
With `--acquire`, runner atomically selects and claims next available verified
mission instead of choosing a mission before lease acquisition. `--mission`
and `--acquire` are mutually exclusive.

Wrapped processes receive `CHAINPROOF_MISSION_ID`, `CHAINPROOF_RUN_ID`,
`CHAINPROOF_CONTEXT_FILE`, `CHAINPROOF_AGENT_ID`, `CHAINPROOF_AGENT_NAME`,
`CHAINPROOF_AGENT_PROFILE`, `CHAINPROOF_AGENT_ROLE`, and
`CHAINPROOF_WORKER_ID`. The context path points to a mode-`0600` JSON file
containing verified bounded mission context. ChainProof removes it when the
wrapper returns after child exit; forced wrapper termination can leave it in
the operating system temporary directory. Harness integrations read this file
before work. Inside the wrapper, `chainproof context` selects the environment
mission automatically and `chainproof checkpoint --current` writes against the
environment mission and run. See [Agent Work Protocol
v1](../spec/agent-work-v1.md).

The mission agent name is inherited unless `--agent` explicitly overrides it.
Run metadata records `mission_id` plus `chainproof.agent.v1`; provenance events
keep their existing v1 shape and carry same attribution in hashed extensions.
Run verification rejects missing or mismatched identity binding.

The profile private key and public profile use owner-only mode `0600` under
`~/.chainproof/agents/PROFILE/` by default. `agent_id` is the SHA-256
fingerprint of the Ed25519 public key. v1 does not yet sign run or checkpoint
content, so this is durable local attribution, not authentication or proof of
key possession.

Native Codex work produces two linked records with different proof boundaries:
an `observed` execution-envelope run from the ChainProof wrapper and an
`imported` native-session run from Codex's local JSONL. The collector only
accepts the linkage marker when its parent run exists, uses the Codex harness,
and belongs to the same mission. The validated child run inherits the parent's
`chainproof.agent.v1` attribution. Neither record upgrades imported claims to
observed evidence.

Observed wrapper-run metadata records `lease_id` and `lease_holder`, connecting
execution inspection to append-only coordination history without moving lease
state into cryptographic proof v1.

## Create a checkpoint

```sh
chainproof checkpoint MISSION_ID RUN_ID '{
  "summary": "Storage and API tests pass",
  "commitments": [
    {
      "id": "context-compiler",
      "description": "Compile bounded context from verified checkpoints",
      "status": "completed",
      "acceptance_criteria": ["reject invalid continuity", "retain evidence links"],
      "evidence": [{"event_id": "EVENT_ID", "note": "context tests passed"}]
    }
  ],
  "next_actions": ["add mission UI", "test restart recovery"],
  "blockers": [],
  "evidence": [
    {"event_id": "EVENT_ID", "note": "test command completed"}
  ]
}'
```

Wrapped agents can omit copied identifiers:

```sh
chainproof checkpoint --current '{"summary":"Session state saved"}'
```

`summary` is required. Evidence IDs must belong to the anchored run prefix.
ChainProof captures the run's current entry count and chain head inside the
same SQLite transaction that appends the checkpoint.

Commitments use stable IDs and `pending`, `blocked`, or `completed` status.
Description and acceptance criteria cannot change after first declaration;
completed commitments cannot regress. Every later checkpoint must carry every
earlier commitment, making the latest checkpoint a complete commitment
snapshot. Commitment evidence follows the same anchored-prefix rule as general
checkpoint evidence.

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

For model input, compile a bounded context envelope instead of loading a raw
transcript:

```sh
chainproof context --mission MISSION_ID --max-evidence 20
```

The compiler verifies the mission and anchored run proof first, then emits the
latest checkpoint plus ordered, deduplicated canonical events cited by its
general and commitment evidence. Output is marked `source.mode: derived` and
`evidence_truncated: true` when the requested bound omits cited events. It
refuses to produce context from invalid continuity. Every cited reference is
validated even when the output limit omits that event.

Interrupted runs can contain evidence newer than the latest checkpoint. Context
output marks this with `has_uncheckpointed_work` and lists each affected run in
`uncheckpointed_work`, including anchored and current entry counts, both chain
boundaries, and current run verification. These entries are recovery metadata;
their event payloads are not promoted into trusted context.

Inspect one verified tail before deciding:

```sh
chainproof recovery inspect MISSION_ID RUN_ID
```

Inspection returns only events after that run's latest anchored entry count,
plus exact old and current chain boundaries. It refuses unrelated runs, invalid
mission continuity, and invalid run proofs.

Accept reviewed work with explicit resumable state:

```sh
chainproof recovery accept MISSION_ID RUN_ID '{
  "reason": "reviewed diff and passing tests",
  "summary": "Recovered implementation is safe",
  "commitments": [],
  "next_actions": ["continue integration"],
  "blockers": []
}'
```

Acceptance follows normal checkpoint rules. In particular, every prior
commitment must remain in the new snapshot, and cited evidence must belong to
the recovered run prefix.

Reject a tail when it should not become resumable state:

```sh
chainproof recovery reject MISSION_ID RUN_ID "output contradicted tests"
```

Rejection does not delete or rewrite events. It creates a reported checkpoint
anchored to the rejected run head, carries forward prior summary, commitments,
next actions, and blockers, and omits prior cross-run evidence from the new
snapshot. Earlier checkpoints retain those citations. With no prior
checkpoint, rejection establishes an explicit empty trusted state.

Both decisions reserve `extensions.chainproof.recovery.v1` with decision,
reason, run ID, and exact from/to entry counts and chain heads. Because that
extension is hashed with the checkpoint, later verification detects changes to
the decision or boundary. Integrity still does not prove the review judgment
was correct.

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
It also requires every associated run to have no events beyond its latest
checkpoint anchor. Use recovery acceptance or rejection first when any tail
remains.

Completion is terminal local control state. Associated runs reject new appends
afterward, preventing evidence from silently appearing beyond final reconciled
checkpoint. Start another mission for later work.

## Local API

```text
POST /api/missions
POST /api/missions/acquire
GET  /api/missions?status=active&limit=100
POST /api/missions/{mission_id}/checkpoints
GET  /api/missions/{mission_id}/resume
GET  /api/missions/{mission_id}/context?max_evidence=20
GET  /api/missions/{mission_id}/recovery/{run_id}
POST /api/missions/{mission_id}/recovery/{run_id}/accept
POST /api/missions/{mission_id}/recovery/{run_id}/reject
POST /api/runs   {"mission_id":"MISSION_ID", ...}
GET  /api/missions/{mission_id}/lease?history=1
POST /api/missions/{mission_id}/lease/claim
POST /api/missions/{mission_id}/lease/renew
POST /api/missions/{mission_id}/lease/handoff
POST /api/missions/{mission_id}/lease/release
```

Lease mutation bodies use `holder`, `lease_id`, and integer `ttl_seconds` as
required by each action. Local API lease semantics match CLI semantics.

Checkpoint requests accept `run_id`, `summary`, `commitments`, `next_actions`,
`blockers`, `evidence`, and `extensions`. The API remains loopback-only by
default and has no multi-user authentication.

Recovery acceptance accepts `reason` plus checkpoint state fields. Recovery
rejection accepts `{"reason":"..."}`. Store logic derives and reserves the
recovery extension; callers cannot supply it.

Mission acquisition accepts `holder`, integer `ttl_seconds`, and
`max_evidence`. It returns mission, lease, and compiled context.

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
