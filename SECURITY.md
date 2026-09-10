# Security policy

ChainProof is local-first software for cooperative agents operating inside one
local user trust boundary. It is not an agent sandbox, authentication service,
secret manager, or public multi-user server.

## Supported versions

| Version | Security fixes |
| --- | --- |
| Latest GitHub release | Supported |
| `main` | Pre-release development; reports welcome |
| Older releases | No guaranteed fixes |

Release tags are immutable compatibility and security boundaries. Source built
from `main` can contain unreleased behavior and identifies itself as a
development build.

## Report a vulnerability

Report suspected vulnerabilities privately through [GitHub Security
Advisories](https://github.com/vajramatt/chainproof/security/advisories/new).
Include affected version or commit, operating system, impact, and minimal
reproduction steps. Do not attach real transcripts, source repositories,
databases, private keys, context files, or proof bundles. Use synthetic data.

Useful reports include unexpected remote reachability, path traversal,
arbitrary file access, private-key disclosure, proof-verification bypass,
canonicalization ambiguity, unsafe release artifacts, or privilege escalation.
Public exposure of a deliberately modified build is not equivalent to a remote
vulnerability in supported default configuration.

## Local trust boundary

Profiles separate attribution among cooperative agents; they do not isolate
agents from each other. Any process running as same operating-system user can
invoke ChainProof, read files that user can read, submit records, or impersonate
another local profile. ChainProof does not defend against a compromised user
account, malicious local process, kernel compromise, or administrator.

Background service runs as current user, never as root. Generated launchd or
systemd definition contains installed executable path, `daemon` argument, and
local log path. Uninstalling service preserves local ledger.

## Network boundary

HTTP API and embedded explorer have no TLS, login, bearer token, tenant
isolation, or multi-user authorization. Server accepts only loopback listen
addresses and defaults to `127.0.0.1:7331`; non-loopback binding is rejected
before socket opens. Requests with non-local Host headers are also rejected as
defense in depth.

Do not expose API through port forwarding, container publishing, reverse
proxy, tunnel, or public ingress. Adding HTTPS alone does not supply required
authorization or change local trust model. Remote and multi-host operation need
separate authenticated protocol and are not supported by current server.

## Sensitive local data

Supported runtime platforms are macOS and Linux. Default state directory is
`~/.chainproof/`. ChainProof creates new state directories owner-only where it
controls creation and repairs ledger, profile, and private-key files to mode
`0600`. Agent profile directories use mode `0700`. Mission context files are
temporary, mode `0600`, and removed after normal wrapper exit; forced
termination can leave them in operating-system temporary directory.

Ledger, WAL files, daemon log, exports, proof bundles, artifact content, profile
keys, and temporary context can contain sensitive metadata or content. Default
Codex collection hashes and counts message bodies, commands, outputs, and file
details instead of storing full bodies, but surrounding metadata can still be
sensitive. Full-content collection is explicit opt-in. Protect backups and
user-selected export destinations under operating-system access controls.

`CHAINPROOF_DB`, `CHAINPROOF_AGENT_HOME`, adapter roots, content settings, and
integration endpoint variables are trusted local configuration. Do not let
untrusted code choose them.

## Proof and identity boundaries

Hash-chain verification detects changes relative to declared chain and a chain
head verifier already trusts. If attacker can replace entire ledger and every
external copy of chain head or bundle, local verification cannot establish
which history came first. ChainProof currently provides no timestamp authority,
external anchoring, transparency log, or signed release of individual records.

Provenance modes describe collection path:

- `observed` means ChainProof directly witnessed recorded event.
- `reported` means harness or agent submitted record.
- `imported` means adapter read existing history.
- `derived` means ChainProof computed record from other evidence.

Valid chain does not prove reported or imported claim was true, complete, or
produced by named model. Key-derived `agent_id` is stable local attribution.
Current records are not signed and do not prove private-key possession. Mission
leases coordinate cooperative workers; they are not identity or authorization
proof.

## Installation and updates

Release installer downloads archive and `checksums.txt` from same GitHub
release over HTTPS, then verifies archive SHA-256 before installation. This
detects mismatch between downloaded archive and published checksum. It is not
independent artifact signing and does not protect against compromise able to
replace both release asset and checksum. Review tag, source, dependencies, and
release provenance when stronger supply-chain assurance is required.
