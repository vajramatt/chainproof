<p align="center">
  <img src="docs/banner.svg" alt="ChainProof — local provenance for any AI agent" width="760">
</p>

<p align="center">
  <strong>See what the agent did. Know where the record came from. Verify it did not change.</strong>
</p>

<p align="center">
  <a href="https://chainproof.ai">Website</a> ·
  <a href="#install">Install</a> ·
  <a href="#the-run-cockpit">Run cockpit</a> ·
  <a href="#bring-any-agent">Integrations</a> ·
  <a href="spec/provenance-v1.md">Proof format</a>
</p>

ChainProof is local-first continuity and provenance infrastructure for AI
agents — one Go binary, one SQLite database, and hash chains you can verify
without trusting ChainProof.

Run Codex, Claude Code, Kimi, Qwen, OpenClaw, a local model, or your own
harness. ChainProof turns agent activity into a durable operational record:
inputs, tools, commands, changes, outputs, failures, policy signals, artifacts,
collection source, and cryptographic continuity.

<p align="center">
  <img src="docs/screen.svg" alt="ChainProof TUI showing local runs, chain integrity, and a live provenance feed" width="820">
</p>

It is built to answer five questions while agent work crosses sessions:

- **What happened?** Read the run as a sequence of inputs, tool calls, outputs,
  decisions, artifacts, errors, and human events.
- **Where did this record come from?** Every event says whether it was
  `observed`, `reported`, `imported`, or `derived`.
- **Has it changed?** Recompute the chain from genesis, or hand the exported
  proof to someone who has never installed or trusted your database.
- **Is it ready to ship?** Jump to failures, changes, decisions, and policy
  evidence without digging through a raw agent transcript.
- **What happens next?** Resume a durable mission from its latest verified
  checkpoint, including pending actions, blockers, and cited evidence.

No account. No API key. No tenant. No pricing page. The ledger lives on your
machine and the code is MIT licensed.

## Project status

Current `main` contains the agent-first continuity loop:

- durable missions spanning multiple agent runs and model sessions
- self-created, key-derived local agent profiles with stable IDs and ephemeral
  worker IDs
- append-only checkpoints anchored to exact verified run-proof prefixes
- stable commitments, next actions, blockers, and cited evidence
- bounded verified context for starting or resuming work
- atomic mission acquisition plus expiring leases, renewal, release, and handoff
- native Codex work execution with automatic lease lifecycle and checkpoint guidance
- explicit review of interrupted, uncheckpointed work through recovery acceptance or rejection
- terminal completion that refuses unresolved recovery tails and prevents later appends
- portable continuity proofs covering checkpoint history and every anchored run prefix
- atomic import of verified continuity proofs into a different local instance,
  preserving canonical IDs and hashes while rebuilding derived search state
- verified filesystem mission workspaces containing continuity JSON,
  deterministic event JSONL and Markdown views, manifest checksums, and
  referenced content-addressed artifacts
- mission, checkpoint, lease, context, and recovery access through the CLI, localhost API,
  TUI, and embedded read-only web explorer where appropriate
- side-effect-free `capabilities --json` and `doctor --json` discovery plus
  idempotent `init --json` state and identity bootstrap on current `main`
- release-shaped clean-install lifecycle checks on hosted macOS and Linux,
  covering bootstrap, proof flow, backup/restore, TUI, loopback web, and
  service setup/removal without deleting state

Current release line is `v0.6.0`. Source builds from `main` include these
capabilities too.

Next work deepens the same local-first architecture rather than adding a hosted
control plane:

- packaging the agent-first build into the next release
- richer native integrations beyond Codex
- deeper local web investigation across timelines, diffs, artifacts, failures,
  comparisons, and proof reports
- durable remote-executor handoff and multi-host lease coordination
- signed agent attestations and key recovery while preserving existing proof boundaries

Roadmap items are direction, not shipped claims or delivery commitments.

Ordinary logs tell you what a process printed. ChainProof preserves the
provenance around it: who or what produced an event, which adapter collected
it, whether collection was live or retrospective, where it sits in the run,
and the exact hash link that would change if history were rewritten. The
search index and cockpit are disposable views; the canonical ledger remains
the evidence.

SQLite is the local operational engine: it provides atomic appends,
transactions, leases, indexes, and safe coordination among local agents.
Portable structured proof bundles and verified mission workspaces are the
interchange boundary and verify without SQLite or a ChainProof server. Markdown
and JSONL inside a workspace are deterministic generated views, not canonical
coordination state.

## Path to autonomous use

ChainProof can support controlled local pilots today. One machine can run
cooperative agents against one ledger through the CLI, TUI, embedded web
explorer, and loopback API. Broader unattended use needs these release gates:

1. **Machine-readable bootstrap.** Current `main` includes side-effect-free
   `chainproof capabilities --json` and `chainproof doctor --json` plus
   idempotent `chainproof init --json`, versioned structured errors, and stable
   exit classes. An agent can discover installed features, create or load
   identity, inspect available work, and diagnose its environment without
   parsing prose.
2. **Process and failure hardening.** Current `main` tests independent OS
   processes concurrently appending and running the full mission lease
   lifecycle against one WAL database. Bounded transaction retries preserve
   complete verifiable event chains; serialize checkpoints plus claim, acquire,
   renew, release, and handoff; reclaim expired leases with one winner; and
   recover cleanly after forced termination during an uncommitted event or
   checkpoint. Custom service state also persists across login. Identity
   failure tests cover corrupt profiles, corrupt or mismatched keys, and either
   identity file going missing without silent replacement. Installer tests
   cover atomic executable upgrades, state preservation, and checksum-failure
   rollback. Current `main` also tests consistent full-instance backup and
   non-destructive restore.
3. **Clean-install verification.** Current `main` passes a release-shaped
   lifecycle on hosted macOS and Linux: archive and checksum installation,
   side-effect-free discovery, first-run profile creation, mission and proof
   flow, backup/restore, TUI startup, loopback web exploration, and isolated
   native service setup/status/removal with ledger and identity preservation.
4. **Portable mission rehydration.** Current `main` verifies and atomically
   imports continuity JSON or a filesystem mission workspace. Workspace export
   carries a checksum manifest, canonical proof, deterministic JSONL and
   Markdown views, and referenced artifact bodies. Import rebuilds mission,
   run, event, checkpoint, search, and artifact state in one transaction,
   without trusting source database or copying identity and lease state.
5. **Integration packaging.** Ship agent-readable setup and lifecycle guidance
   for Codex, Claude Code, OpenClaw, and generic harnesses. Each integration
   must preserve provenance mode and same local trust boundary.

Target autonomous lifecycle:

```text
discover → identify → inspect → acquire → work → checkpoint → verify → transfer → resume
```

Current `main` has passed machine-readable bootstrap, multi-process and crash
tests, clean-install validation, and portable workspace rehydration. Next
release work can package these capabilities while integration packaging
continues.
Signed attestations, key recovery, and private multi-host coordination follow;
they are not prerequisites for local cooperative use. Publicly exposing the
unauthenticated loopback service is not part of this path.

## Install

ChainProof is one Go binary. Install the latest release on macOS or Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/vajramatt/chainproof/main/scripts/install.sh | sh
```

Or, with Go 1.24 or newer:

```sh
go install github.com/vajramatt/chainproof/cmd/chainproof@v0.6.0
```

Or build the checkout:

```sh
git clone https://github.com/vajramatt/chainproof.git
cd chainproof
make build
./chainproof
```

Agents using current `main` can bootstrap without parsing human output:

```sh
chainproof capabilities --json
chainproof init --json
chainproof doctor --json
```

See [`docs/agent-bootstrap.md`](docs/agent-bootstrap.md) for field and
side-effect contracts. These commands are newer than release `v0.6.0`.

Commands using `--json` automatically return failures as one versioned JSON
document on stderr. Add global `--json-errors` to any other invocation when an
agent needs same contract. Exit `1` means command failure, `2` means usage
error, and `3` means proof verification failure. Doctor health remains in its
JSON `status`; successful diagnosis exits `0` even when state needs attention.

The installer verifies the release archive against its published SHA-256
checksum, stages upgrades beside the installed binary, and atomically replaces
the executable only after verification. Existing ledger and identity state are
not changed. The database opens at `~/.chainproof/chainproof.db`; set
`CHAINPROOF_DB` to put it somewhere else. Local agent profiles live beside the
database under `agents/`.

## Back up and restore an instance

Current `main` can create a consistent backup while local writers remain
active, then verify and restore it into a new directory:

```sh
chainproof backup /secure/backups/chainproof-2026-09-10
chainproof restore /secure/backups/chainproof-2026-09-10 /srv/chainproof-restored
CHAINPROOF_DB=/srv/chainproof-restored/chainproof.db \
CHAINPROOF_AGENT_HOME=/srv/chainproof-restored/agents \
chainproof doctor --json
```

Backup format `chainproof.backup.v1` contains a transactionally consistent
SQLite snapshot, every valid local agent profile and private key, and a
SHA-256 manifest. Creation and restore stage privately, verify before
publication, validate every run and mission proof chain plus content-addressed
artifact, and refuse existing destinations. Restore never overwrites the
configured live instance. Because backups contain identity private keys, keep
them under owner-only storage controls.

## Open it

```sh
chainproof
```

That is enough. ChainProof discovers the Codex sessions under
`~/.codex/sessions`, imports what is already there, follows active session
files once a second, and opens the TUI. New turns appear without launching
Codex through ChainProof.

The local web app does the same:

```sh
chainproof serve           # collector + API + dashboard at 127.0.0.1:7331
```

The collector remembers a byte cursor per session file, so restarting catches
up and does not duplicate events. One Codex session becomes one ChainProof run.

By default, message bodies, commands, output, and changed-file details are
stored as a hash and byte count. The operational shape remains visible without
silently copying the transcript. Because the database is local, you can opt in
to full content:

```sh
CHAINPROOF_CODEX_CONTENT=full chainproof
```

Use a nonstandard Codex home—or turn discovery off—with:

```sh
CHAINPROOF_CODEX_ROOT=/path/to/codex/sessions chainproof
CHAINPROOF_CODEX_DISABLED=1 chainproof
```

## Keep it running

Install ChainProof as a per-user background service:

```sh
chainproof service install
```

On macOS this creates a private user LaunchAgent; on Linux it creates and
enables a systemd user service. It starts at login, follows Codex while the TUI
is closed, owns the localhost API, and keeps the ledger current.

Installation snapshots resolved `CHAINPROOF_DB`, `CHAINPROOF_AGENT_HOME`,
`CHAINPROOF_AGENT_PROFILE`, and configured Codex collector settings into the
native service definition. Path values become absolute, and daemon output is
written beside the configured database. Re-run `chainproof service install`
after changing these settings. Only this fixed non-secret allowlist is copied;
other environment variables and credentials are excluded.

```sh
chainproof service status
chainproof service stop
chainproof service start
chainproof service uninstall   # the SQLite ledger is preserved
```

With the service running, `chainproof` detects it and opens the TUI without
starting a second collector. The web dashboard remains at
`http://127.0.0.1:7331`, and machine-readable health lives at
`http://127.0.0.1:7331/api/status`.

For supervisors, containers, or debugging, run the same process in the
foreground:

```sh
chainproof daemon
```

## Wrap another agent

The quickest path is to let ChainProof wrap a harness:

```sh
chainproof run -- codex
chainproof run -- claude
chainproof run -- opencode
chainproof run -- your-agent --task task.json
```

Then open the ledger:

```sh
chainproof                 # the TUI is the front door
chainproof serve           # local web dashboard at 127.0.0.1:7331
```

Wrapping records the process lifecycle and exit status as **observed**. It does
not magically reveal internal tool calls or private model reasoning. A native
hook, push integration, or pull adapter provides the richer event stream.

## Give an agent durable identity

An agent can create its own local identity without an account, server, or human
registration step:

```sh
chainproof agent ensure --profile codex-main --name Forge --harness codex
CHAINPROOF_AGENT_PROFILE=codex-main chainproof whoami
CHAINPROOF_AGENT_PROFILE=codex-main chainproof agent rename --name Forge-2
```

`agent ensure` creates one owner-only Ed25519 key and public profile. `agent_id`
is a stable SHA-256 fingerprint of the public key. `display_name` is readable
presentation and can change without rotating identity; `profile` selects one
local identity; `worker_id` is new for each run session; `role` describes work
within one mission. Ordinary agent-aware commands auto-create the selected
profile, so `chainproof whoami` also works on a fresh install. Use
`CHAINPROOF_AGENT_PROFILE` when several agents share one ledger. Use
`CHAINPROOF_AGENT_HOME` only when profile files must live somewhere other than
beside the database.

Identity initialization occurs only when both profile and private key are
absent. If either established file is missing, corrupt, or mismatched,
ChainProof fails closed and leaves both paths unchanged. Run
`chainproof doctor --json` to distinguish fresh state from damaged identity.
There is no automatic key recovery or identity rotation in v1; restore lost
material from a protected backup or deliberately choose a new profile.

Identity data is recorded under `chainproof.agent.v1` in mission and run
metadata plus hashed event and checkpoint extensions. Run verification checks
that declared run attribution matches chain-bound event attribution and that
`agent_id` fingerprints the declared public key. Current v1 does not sign those
records or authenticate access. Anyone able to write local files or invoke the
CLI as the same operating-system user can impersonate a profile. The private
key establishes a stable identifier and supports future signed attestations;
it is not ChainProof authentication.

## Continue across sessions

A run records one bounded execution. A mission links runs into durable work.
Each checkpoint carries a required summary, next actions, blockers, and
stable commitments with acceptance criteria, and optional evidence references,
then anchors that state to an exact run-proof prefix.

```sh
chainproof mission start --objective "Ship durable continuity" --role implementer
chainproof mission list --status active
chainproof mission acquire --ttl 30m
chainproof codex work --mission MISSION_ID
chainproof checkpoint MISSION_ID RUN_ID '{
  "summary": "Storage and API tests pass",
  "commitments": [{
    "id": "mission-ui",
    "description": "Add mission inspection view",
    "status": "pending",
    "acceptance_criteria": ["mission chain visible"]
  }],
  "next_actions": ["add mission UI"],
  "blockers": []
}'
chainproof resume MISSION_ID
chainproof context --mission MISSION_ID --max-evidence 20
```

Mission-wrapped commands receive an ephemeral verified context file, mission
and run IDs, stable agent identity, mission role, and ephemeral worker identity
through environment variables. This gives every harness one stable bootstrap
contract while leaving prompt injection and checkpoint writing to its
integration. Wrapped agents can call `chainproof context` and `chainproof
checkpoint --current` without copying identifiers. See
[`spec/agent-work-v1.md`](spec/agent-work-v1.md).

`chainproof codex work` is the native Codex integration. It verifies and injects
mission context, tells Codex to checkpoint before ending, and emits a linkage
marker that the local collector validates against the mission execution run.
It atomically claims the mission, renews ownership during long sessions, and
releases ownership on exit unless Codex handed it to another worker. Lease ID
and holder are recorded in run metadata. Codex options follow `--`; use
`--exec` for non-interactive work:

```sh
chainproof codex work --mission MISSION_ID --holder builder --lease-ttl 30m -- --model gpt-5.6-sol
chainproof codex work --mission MISSION_ID --exec --prompt "Finish pending tests" -- --model gpt-5.6-sol
```

When `--mission` is omitted, the most recently updated active mission is used.

Long-running workers can atomically take next available verified mission:

```sh
chainproof codex work --acquire --holder queue-worker --exec --prompt "Continue mission" -- --model gpt-5.6-sol
```

`mission acquire` returns mission, new lease, and bounded verified context as
one JSON envelope. It selects oldest available active mission, skips live
leases, reclaims expired leases, and refuses invalid continuity. `codex work
--acquire` uses same path before starting Codex, removing list/claim race.

Competing agents coordinate with expiring mission leases. Claims are atomic;
renewal and release require the current lease token. Handoff atomically issues
a new token to the next holder:

```sh
chainproof mission lease MISSION_ID --history
chainproof mission renew MISSION_ID LEASE_ID --ttl 30m
chainproof mission handoff MISSION_ID LEASE_ID --to reviewer --ttl 30m
chainproof mission release MISSION_ID LEASE_ID
```

Lease transitions form append-only local coordination history. They are not
part of continuity proof v1, do not establish cryptographic identity, and do
not change checkpoint hashes. When `--holder` is omitted, claims use current
stable `agent_id`; an explicit holder remains supported. Expiry permits crash
recovery without rewriting history.

Wrapped Codex receives `CHAINPROOF_LEASE_ID` alongside mission, run, and
context variables. It can transfer ownership before exit:

```sh
chainproof mission handoff "$CHAINPROOF_MISSION_ID" "$CHAINPROOF_LEASE_ID" --to NEXT_HOLDER
```

`chainproof resume` without an ID loads the most recently updated active
mission. It returns the latest checkpoint together with verification state, so
an agent can reject broken inherited context instead of silently trusting it.
Agents using the localhost API can pass `mission_id` when creating a run, then
write checkpoints through `/api/missions/{mission_id}/checkpoints`. `context`
produces bounded derived model input only after verifying mission continuity
and its cited run evidence. It also flags verified run entries newer than the
latest checkpoint as uncheckpointed recovery work without silently injecting
their payloads into inherited context.

Review interrupted work before resuming it:

```sh
chainproof recovery inspect MISSION_ID RUN_ID
chainproof recovery accept MISSION_ID RUN_ID '{
  "reason": "tests and changes reviewed",
  "summary": "Recovered work is safe to continue",
  "next_actions": ["finish integration"]
}'
chainproof recovery reject MISSION_ID RUN_ID "output contradicted tests"
```

Acceptance writes reviewed state as a reported checkpoint. Rejection anchors
the exact discarded tail while carrying forward the prior trusted state. It
does not delete events or promote rejected payloads into trusted evidence.
Both decisions are stored under the hashed `chainproof.recovery.v1` checkpoint
extension, then disappear from `uncheckpointed_work` because the run prefix is
now explicitly reconciled.

Export the mission and every anchored run prefix as one portable session proof:

```sh
chainproof mission export MISSION_ID continuity-proof.json
chainproof verify-continuity-file continuity-proof.json
chainproof mission complete MISSION_ID
```

Move an active mission into another ChainProof instance:

```sh
chainproof mission export MISSION_ID continuity-proof.json
CHAINPROOF_DB=/path/to/other/chainproof.db \
  chainproof mission import continuity-proof.json
CHAINPROOF_DB=/path/to/other/chainproof.db \
  chainproof resume MISSION_ID
```

Import verifies full checkpoint chain, every anchored run proof, and consistent
prefixes when several checkpoints cite same run before starting one SQLite
transaction. It preserves canonical mission, checkpoint, run, and event IDs and
hashes; rebuilds search rows; imports no leases, private keys, or artifact
bodies; and refuses any destination ID collision without partial writes. Source
and destination must not continue same active mission independently because v1
has no multi-host coordination.

Carry proof, readable views, and referenced artifact bodies as one verified
filesystem workspace:

```sh
chainproof mission workspace export MISSION_ID ./mission-workspace
chainproof mission workspace verify ./mission-workspace
CHAINPROOF_DB=/path/to/other/chainproof.db \
  chainproof mission workspace import ./mission-workspace
CHAINPROOF_DB=/path/to/other/chainproof.db \
  chainproof resume MISSION_ID
```

Workspace verification is offline and does not initialize local state. Export
refuses an existing destination and publishes only after staged files verify.
Import checks complete inventory, file hashes, continuity, deterministic JSONL
and Markdown projections, and artifact bodies before atomically rebuilding
destination state. See
[`spec/mission-workspace-v1.md`](spec/mission-workspace-v1.md).

Completion is terminal local control state. ChainProof refuses completion while
any associated run has uncheckpointed events; accept or reject every recovery
tail first. Once completed, associated runs reject new appends so later work
cannot silently appear beyond final checkpoint.

Checkpoint integrity does not make a summary true. Key-derived agent IDs are
attribution metadata in continuity v1, not cryptographic signatures over
checkpoint content. See
[`docs/continuity.md`](docs/continuity.md) for the workflow and
[`spec/continuity-v1.md`](spec/continuity-v1.md) for the exact proof boundary.

## The run cockpit

The TUI is a deterministic run cockpit: repository and objective fingerprint,
duration, tool/change/failure/policy signals, chain integrity, a full-width
evidence table, operational filters, canonical event inspection, and proof
export. Every summary fact resolves to ledger evidence.

```text
  ⬡ CHAINPROOF  TOKYO NIGHT  27 RUNS  1 ACTIVE  11 AGENTS  100% INTEGRITY
  RUNS                    // LIVE PROVENANCE
  ▌ chainproof  428       428 EVENTS · 196 TOOLS · 13 FAILURES · 4m12s
    lodestone    70       REPO      /work/chainproof
    stillpoint   25       OBJECTIVE sha256:4b7885c… · 62 B
                          EVIDENCE · FAILURES
                          0400 11:38:15 IMPORTED tool.result shell · failed
```

### Keys

| key | what |
| --- | --- |
| `j` / `k` · arrows | move through runs, evidence, search results, or inspected payloads |
| `Tab` | switch focus between runs and evidence |
| `Enter` | inspect the selected canonical event or search result |
| `f` / `c` / `d` / `p` / `a` | failures / changes / decisions / policy / all |
| `/` | search evidence; supports `tool:`, `status:`, `agent:`, `kind:`, `mode:`, `file:` |
| `x` | export the selected portable proof to `~/.chainproof/exports` |
| `v` / `r` | reload and verify the selected run |
| `t` | change the light: Tokyo Night ↔ Synthwave '84 |
| `q` / `ctrl-c` | leave |

Tokyo Night is the house light. Synthwave '84 repaints the room in violet,
electric pink, and cyan. The local web dashboard carries both palettes too;
its switch is remembered in the browser. ChainProof explicitly enables
truecolor for the interactive TUI because terminal launchers often leak
`TERM=dumb` or `NO_COLOR`; use `CHAINPROOF_COLOR=never` for intentional
monochrome output.

## Investigate what happened

The append-only ledger is the source of truth. Alongside it, ChainProof keeps
a rebuildable SQLite provenance index so the evidence is useful while an
incident—or an agent—is still moving.

Press `/` in the TUI, open **Investigate** in the web dashboard, or query from
the shell:

```sh
chainproof search "failed"
chainproof search "internal/store/search.go"
chainproof search "e4be0f5dbd629073"
```

The web interface combines free-text search with facets for agent, event kind,
tool, status, and collection mode. Selecting a result reveals its canonical
payload, native source identity, previous hash, and event hash. You can search
tool names, paths, working directories, outcomes, and hashes even when message
content is protected by the default hashes-only policy.

The index is deliberately not part of the proof. It can be deleted and rebuilt
from canonical ledger events without changing a chain head. See
[`docs/investigation.md`](docs/investigation.md) for the boundary and query API.

The next-generation local browser experience is specified in
[`docs/web-explorer.md`](docs/web-explorer.md): a run cockpit for timelines,
diffs, artifacts, failure analysis, comparison, and proof reports—not a hosted
SaaS dashboard.

## Bring any agent

The proof format knows nothing about model vendors. Integrations sit at the
edge and normalize into one stable event shape. There are four ways in:
automatic discovery, push, pull, and process wrapping.

### Codex — discovered automatically

The built-in `codex-local-v1` adapter reads Codex's local JSONL session files.
It normalizes:

- session metadata and working directory
- model, approval policy, and turn context
- turn start and completion
- user and agent messages
- shell execution, status, exit code, stdout, and stderr
- file changes, extension calls, and image views

Reasoning records are deliberately skipped. Imported Codex records are marked
**imported**: ChainProof is protecting Codex's local account of the session,
not claiming to have independently observed the model.

Run a one-shot catch-up or watch without opening either interface:

```sh
chainproof codex sync
chainproof codex watch
```

### Push — the harness tells ChainProof

Create a run and append events over the localhost API or CLI:

```sh
chainproof start --agent qwen-local --harness my-runner --model qwen3

chainproof append RUN_ID '{
  "kind": "tool.call",
  "source": { "adapter": "my-runner", "mode": "reported" },
  "payload": { "tool": "shell", "command": "git status" }
}'

chainproof complete RUN_ID
```

The same operations are available at `POST /api/runs` and
`POST /api/runs/{id}/events` when `chainproof serve` is running.

### Pull — ChainProof reads a local history

Normalize a harness transcript to one JSON object per line:

```sh
chainproof pull RUN_ID ~/.local/share/my-agent/events.jsonl my-agent
```

ChainProof remembers a byte cursor for the adapter and source path. Run it
again and only appended records come across. Pulled history is always marked
**imported**, even if the source file claims otherwise.

### Wrap — ChainProof watches the process

```sh
chainproof run -- codex
```

The wrapper observes process start, exit, and final status. Claude Code and
other harnesses still need wrapping or a generic push/pull integration today;
Codex has native automatic discovery.

The adapter contract and integration guidance live in
[`docs/integrations.md`](docs/integrations.md).

## OpenClaw

The OpenClaw integration ships in
[`integrations/openclaw`](integrations/openclaw). Start the local server, build
the hook, and install that directory in OpenClaw:

```sh
chainproof serve
cd integrations/openclaw
npm ci && npm run build
```

It records session lifecycle, human messages, tool results, and model outputs.
Message and tool bodies are hashed by default. Set
`CHAINPROOF_STORE_CONTENT=true` to store their content as local,
content-addressed artifacts.

No ChainProof API key is involved. The hook talks to
`http://127.0.0.1:7331` unless `CHAINPROOF_URL` says otherwise.

## What a proof proves

Each event includes the hash of the event before it:

```text
genesis = 0000000000000000000000000000000000000000000000000000000000000000

event[0] = sha256(canonical event[0] + genesis)
event[1] = sha256(canonical event[1] + event[0])
event[2] = sha256(canonical event[2] + event[1])
...
chain head = final event hash
```

Verification pins the genesis hash and checks contiguous sequence numbers, run
identity, every previous-hash link, every event hash, the declared entry count,
and the declared chain head. That catches edits, reordering, missing middle
records, and prefix truncation.

Export a portable bundle and verify it without opening a database:

```sh
chainproof export RUN_ID proof.json
chainproof verify-file proof.json
```

The verifier works on a clean machine because the proof bundle contains the
run declaration and canonical events. The format is documented in
[`spec/provenance-v1.md`](spec/provenance-v1.md).

> ChainProof records what an agent, harness, adapter, or observer reports. A
> valid chain proves continuity of those recorded bytes. It does **not** prove
> that the report was truthful, complete, or produced by the model it names.

## Collection marks

| mark | meaning |
| --- | --- |
| `observed` | ChainProof captured the event directly while execution occurred |
| `reported` | an agent or harness submitted the event |
| `imported` | an adapter collected an existing record after the fact |
| `derived` | ChainProof calculated the event from other recorded material |

These are provenance labels, not trust scores. A perfectly valid imported chain
is still an imported chain.

## Local, by design

ChainProof stores runs, canonical events, adapter cursors, and artifacts in a
local SQLite database using WAL mode and serialized writes. Bounded retries
around whole event, checkpoint, and mission lease transactions resolve
independent-process contention from fresh state instead of leaking raw lock
errors.
Artifact hashes are computed over raw bytes—not decoded text—and
content-addressed by SHA-256.

The web server accepts only loopback listen addresses and defaults to
`127.0.0.1:7331`. It also rejects non-local host headers. ChainProof
intentionally has no multi-user authentication and cannot be bound directly to
a non-loopback interface.

The things ChainProof writes are its own:

- `~/.chainproof/chainproof.db` — ledger, rebuildable search index, cursors, and artifacts
- `~/.chainproof/chainproof.db-wal` — SQLite's write-ahead log while active
- `~/.chainproof/agents/PROFILE/` — owner-only public profile and Ed25519 private key
- `~/.chainproof/exports/` when you press `x` in the TUI
- another export path only when you ask for one with `chainproof export`

It does not edit the repositories or harness histories it observes.

## Commands

| command | what |
| --- | --- |
| `chainproof --json-errors COMMAND` | request one versioned JSON error document and stable exit class |
| `chainproof capabilities --json` | describe current build, paths, protocols, and shipped features without creating state |
| `chainproof init --json` | idempotently initialize ledger and stable local identity |
| `chainproof doctor --json` | diagnose platform, ledger, permissions, identity, and loopback API without creating state |
| `chainproof backup BACKUP_DIR` | create verified full-instance backup without stopping writers |
| `chainproof restore BACKUP_DIR NEW_DIR` | verify and restore into a new, non-existing instance directory |
| `chainproof agent ensure` | create or load stable local agent identity |
| `chainproof agent rename` | change readable name without rotating stable ID |
| `chainproof whoami` | print current public agent identity |
| `chainproof` / `chainproof ui` | open the terminal interface |
| `chainproof serve [address]` | run the local API and web dashboard |
| `chainproof daemon` | run the collector and local API in the foreground |
| `chainproof service install` | install and start a login service |
| `chainproof service status` | inspect the native user service |
| `chainproof mission start` | start durable work across sessions |
| `chainproof mission list` | discover missions by status |
| `chainproof mission acquire` | atomically claim available work with verified context |
| `chainproof mission complete` | close a mission after a valid checkpoint |
| `chainproof mission export` | export checkpoints and anchored run proofs |
| `chainproof mission import` | verify and atomically rebuild a portable mission proof |
| `chainproof mission workspace export` | create verified proof, views, and artifact directory |
| `chainproof mission workspace verify` | verify workspace offline without creating local state |
| `chainproof mission workspace import` | atomically rebuild mission and referenced artifacts |
| `chainproof mission claim` | atomically acquire an expiring mission lease |
| `chainproof mission lease` | inspect active ownership and coordination history |
| `chainproof mission renew` | extend a lease using its current token |
| `chainproof mission handoff` | transfer ownership with a new lease token |
| `chainproof mission release` | release current ownership |
| `chainproof start` | open a provenance run |
| `chainproof append` | append one reported event |
| `chainproof ingest` | import JSONL from stdin |
| `chainproof pull` | incrementally import a JSONL file |
| `chainproof run -- …` | wrap any harness or process |
| `chainproof complete` | close a run with a terminal status |
| `chainproof verify` | verify a run in the local ledger |
| `chainproof export` | write a portable proof bundle |
| `chainproof verify-file` | independently verify a bundle |
| `chainproof checkpoint` | anchor resumable state to a run-proof prefix |
| `chainproof resume` | verify and load the latest mission checkpoint |
| `chainproof context` | compile bounded verified mission context for an agent |
| `chainproof recovery inspect` | inspect verified uncheckpointed run events |
| `chainproof recovery accept` | checkpoint reviewed recovered state |
| `chainproof recovery reject` | reject a tail while preserving prior trusted state |
| `chainproof verify-continuity-file` | verify portable mission proof offline |
| `chainproof list` | print local runs as JSON |
| `chainproof search QUERY` | search structured local provenance evidence |
| `chainproof codex sync` | discover and import Codex sessions once |
| `chainproof codex watch` | continuously follow Codex sessions |
| `chainproof codex work` | run or atomically acquire Codex work with verified mission context |

Run `chainproof --help` for the one-screen version.

## Build the kitchen

```sh
make check
cd integrations/openclaw && npm ci && npm run typecheck
```

The Go suite includes canonicalization, lifecycle, byte-correct artifact,
tamper, truncation, and wrong-genesis tests. Provider-specific parsing belongs
behind the adapter contract; changes to canonicalization require a versioned
format transition and interoperability fixtures.

Contributions are welcome. Start with [`CONTRIBUTING.md`](CONTRIBUTING.md), and
please report security problems as described in [`SECURITY.md`](SECURITY.md).
Maintainers should follow [`docs/releasing.md`](docs/releasing.md); tags are
release contracts, not backup markers.

## License

[MIT](LICENSE) — use it, fork it, build on it; keep the copyright notice.

© Matt Williamson
