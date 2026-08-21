# The local web explorer

> Product and implementation brief for the next ChainProof web interface.

ChainProof should keep a local web interface. The current dashboard is a useful
prototype, but it is not yet a compelling reason for a developer to leave the
TUI. The next interface should be rebuilt around investigation rather than
administration.

The browser is where a long agent run becomes legible: prompts lead to model
responses, responses lead to tool calls, tool calls touch files, failures lead
to retries, and every claim retains its provenance boundary. The web explorer
should make those relationships faster to understand than raw logs or a flat
event table.

This is still a local application. It is not a return to hosted SaaS.

## Product split

The TUI and web explorer are two views over the same local evidence store. They
should overlap enough to feel coherent, but each should be optimized for the
work it does best.

### TUI: awareness and speed

- see active and recent runs
- follow live evidence
- verify chain integrity
- filter common event classes
- inspect one event without breaking terminal flow
- export a proof bundle
- open the selected run or event in the browser

### Web: investigation and depth

- reconstruct an entire run
- relate inputs, outputs, actions, results, and file changes
- inspect long payloads, artifacts, and diffs
- find failures and understand recovery attempts
- compare runs
- search across every recorded agent and project
- produce an intelligible verification report

The TUI is the cockpit. The web explorer is the incident room.

### Invocation model

`chainproof` always starts in the TUI. That is the product's front door and
the default place to work. Starting ChainProof must not automatically open a
browser or replace the terminal with a web dashboard.

The local server may run behind the TUI so it is ready when needed, but the web
explorer appears only through an explicit action:

```text
chainproof                 # start in the TUI
chainproof open RUN_ID     # open one run in the local web explorer
chainproof web             # explicitly open the local runs view
```

From inside the TUI, one key opens the currently selected run or event at
`127.0.0.1`. Closing the browser returns nothing to clean up: the terminal
remains the active ChainProof session.

This hierarchy is intentional. Routine awareness stays fast, keyboard-driven,
and terminal-native. The browser is pulled in when screen width, rich diffs,
long artifacts, comparison, or a complex investigation makes it the better
tool.

## Decision

Keep:

- the Go HTTP server
- the loopback-only security model
- SQLite as the canonical local store
- the append-only hash-chained ledger
- the derived provenance search index
- the existing `/api/*` foundation
- Tokyo Night and Synthwave '84
- a dependency-light, single-binary distribution

Replace:

- the current generic dashboard layout
- the overview-first information architecture
- the flat evidence result list as the primary investigation view
- the single undifferentiated JSON payload drawer
- polling and verification patterns that do not scale with run count

The existing HTML is a prototype, not a migration constraint. Reuse its API
knowledge and visual tokens, not its page structure.

## Design principles

### Evidence before metrics

ChainProof is not an agent analytics dashboard. Counts are useful context, but
the central object is a run and the evidence that explains it.

### Make causality visible

The interface should answer:

1. What did the human ask?
2. What did the model decide or produce?
3. Which tool ran because of it?
4. What changed as a result?
5. Did the action succeed?
6. What happened next?

Where a relationship is inferred rather than explicitly recorded, label it as
derived.

### Make the proof boundary visible

Every event must visibly retain its collection mode:

- `OBSERVED` — ChainProof witnessed it directly
- `REPORTED` — a connected harness sent it
- `IMPORTED` — recovered from an existing history
- `DERIVED` — computed from other evidence

A valid hash chain proves that the stored record has not changed. It does not
prove that an imported or reported claim was true. Verification UI must say
this plainly.

### Progressive disclosure

Show a concise human-readable event first. Put canonical JSON, native IDs,
hashes, and adapter details one level deeper. Power users should reach raw
evidence in one click without making everyone read it by default.

### Local means local

- bind to loopback by default
- make no third-party requests
- load no remote fonts, scripts, telemetry, or CDNs
- work offline after installation
- retain the active content policy when displaying or exporting evidence
- never imply that opening the browser publishes a run

## Information architecture

### 1. Runs

The default view is a dense, useful run list—not a collection of metric cards.

Each row shows:

- project or agent name
- harness and model
- status and last activity
- event and tool-action counts
- failures
- collection modes present
- chain verification state

Controls:

- free-text search
- active/completed/failed filters
- project, harness, model, and time filters
- sort by activity, duration, failures, or event count
- keyboard navigation

Selecting a run opens its cockpit. Active runs update without losing scroll or
selection.

### 2. Run cockpit

The cockpit is the primary screen.

Header:

- project and run identity
- harness, model, adapter, start time, duration, and status
- parent and child runs
- evidence counts
- verification state and chain head
- actions: verify, export proof, copy deep link, compare

Main timeline:

- chronological groups by turn or causal cluster
- human input, model output, tool call, tool result, artifact, and lifecycle
  events have distinct but theme-consistent treatments
- failures and retries are visually connected
- collapsed low-value sequences can be expanded
- live runs append without moving the reader away from an inspected event

Context rail:

- files touched
- commands and tools used
- failures
- artifacts
- agents and subagents
- provenance mix

Clicking an item filters or jumps the timeline rather than opening a separate
administrative page.

### 3. Event inspector

The inspector should support event-specific views.

Common evidence:

- sequence, timestamp, actor, harness, and model
- collection mode and adapter
- native event ID
- previous hash and event hash
- canonical payload
- related events and artifacts

Specialized rendering:

- inputs and outputs: readable text when content policy permits it
- shell actions: command, working directory, exit status, duration, stdout and
  stderr evidence
- file actions: path, operation, before/after hashes, and diff when available
- HTTP actions: method, target, status, timing, and sanitized bodies
- artifacts: metadata, digest, size, preview, and save action

Raw canonical JSON remains available in a dedicated tab.

### 4. Investigate

Global investigation searches across the derived index while always linking
back to canonical events.

It includes:

- free-text query
- facets for project, agent, harness, model, event kind, tool, outcome,
  collection mode, and time
- saved filters stored locally
- failure-only and file-path shortcuts
- grouped results by run or chronological result stream
- URL-encoded query state for local deep links

Search results must state that the index is derived and not the evidence
authority.

### 5. Compare

Compare two runs side by side:

- model and harness
- prompts or prompt hashes
- tools and commands
- files changed
- failures and retries
- duration and event counts
- verification and provenance mix

Comparison is descriptive. It should not invent a quality score.

### 6. Verification report

Verification deserves a readable report rather than a green percentage.

Show:

- valid or invalid chain state
- checked entry count
- chain head
- first invalid sequence, when applicable
- content policy and omitted-content representation
- evidence modes present
- a concise explanation of what verification proves and does not prove
- export and independent verification command

## Navigation and visual system

The web explorer should share the TUI's identity:

- powerline-style status ribbon
- Tokyo Night and Synthwave '84 with persisted selection
- full-canvas background painting
- compact monospace typography
- cyan for identity and human input
- pink/purple for model output
- yellow for tools and actions
- green for verified state
- red reserved for failure or invalid proof

Do not reproduce terminal limitations in a browser. Use responsive columns,
real scrolling regions, accessible controls, copy affordances, and readable
long-form payloads.

Desktop is the primary environment, but the run timeline and event inspector
must remain usable on a tablet or narrow browser window.

## TUI integration

Add a command that opens the current selection in the local browser:

```text
chainproof open RUN_ID
chainproof open RUN_ID --event EVENT_ID
```

The TUI should expose this as a single key. Deep links use stable local routes:

```text
/runs/{run_id}
/runs/{run_id}?event={event_id}
/investigate?q=failed&tool=shell
/compare?left={run_id}&right={run_id}
```

These are local navigation URLs, not shareable internet URLs.

## API work

Retain the current endpoints and evolve them deliberately.

Existing foundation:

```text
GET /api/status
GET /api/runs
GET /api/runs/{id}
GET /api/runs/{id}/events
GET /api/runs/{id}/verify
GET /api/runs/{id}/lineage
GET /api/search
GET /api/events/{id}
GET /api/artifacts/{hash}
```

Likely additions:

```text
GET /api/runs/{id}/summary
GET /api/runs/{id}/facets
GET /api/runs/{id}/files
GET /api/runs/{id}/failures
GET /api/runs/{id}/proof
GET /api/compare?left={id}&right={id}
GET /api/stream
```

Before adding endpoints, measure whether indexed queries and existing event
responses can provide the view efficiently. Prefer server-computed summaries
when they prevent repeated full-ledger transfer or client-side N+1 requests.

Replace the current pattern of verifying every run every three seconds. Cache
verification by chain head and invalidate it only when a run appends an event.
Use server-sent events for active status and event updates; fall back to modest
polling if streaming is unavailable.

## Frontend architecture

Constraints matter more than framework preference:

- embedded into the Go binary
- reproducible release build
- no runtime network dependencies
- small asset footprint
- semantic HTML and keyboard accessibility
- deterministic theme tokens shared with the TUI
- testable rendering and navigation

A small compiled frontend is acceptable if it materially improves state,
routing, components, and testing. Avoid rebuilding an application framework by
hand inside one HTML file. The output remains static assets embedded with
`go:embed` and served by the local Go server.

## Build sequence

### Phase 1 — run cockpit

1. Introduce browser routes and stable deep links.
2. Replace the dashboard with the run list and run cockpit shell.
3. Build the grouped event timeline.
4. Build the event inspector with specialized shell and file renderers.
5. Add cached verification and the verification report.
6. Add TUI-to-browser open behavior.

This phase is the new minimum viable web explorer.

### Phase 2 — investigation

1. Build global search around the existing index.
2. Add URL-backed filters and local saved searches.
3. Add files, failures, artifacts, and subagent context rails.
4. Add live updates for active runs.
5. Add proof bundle export from the browser.

### Phase 3 — comparison and polish

1. Add run comparison.
2. Add artifact and diff previews for supported evidence.
3. Improve causal grouping using explicit relationships first and clearly
   labeled derived relationships second.
4. Complete narrow-screen and accessibility passes.
5. Add representative large-run performance fixtures.

## Acceptance criteria for the first rebuild

A developer can:

- open ChainProof and reach a recent run in one action
- understand the run's inputs, outputs, tool actions, failures, and file changes
  without reading raw JSON
- click any event to see canonical evidence and its provenance boundary
- verify a run and understand what the result proves
- search for a command, path, failure, hash, agent, or model
- open the exact run and event from the TUI
- export a proof bundle
- use the explorer entirely offline on loopback
- switch between Tokyo Night and Synthwave '84

The interface remains responsive with a representative run containing at least
10,000 events and does not re-verify or refetch every run on a timer.

## Non-goals

- user accounts, teams, billing, or tenancy
- a hosted ChainProof control plane
- remote collaboration or public run URLs
- model evaluation scores
- claims that hash integrity establishes factual truth
- vendor-specific assumptions in the canonical experience
- editing, replaying, or approving agent actions from the provenance explorer

## The test for every screen

Every screen should help answer at least one of these questions:

1. What did the agent do?
2. Why did it do that?
3. What changed?
4. Where did this evidence come from?
5. Has the record changed?

If a screen cannot answer one of them, it probably does not belong in
ChainProof.
