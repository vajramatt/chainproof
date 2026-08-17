# Security

Please report vulnerabilities privately through GitHub Security Advisories.
Do not include private agent transcripts, local databases, or proof bundles in
public issues.

ChainProof listens on `127.0.0.1` by default. Binding it beyond loopback is not
recommended in v1: the local API intentionally has no multi-user authentication.

The background service runs as the current user, never as root. Its launchd or
systemd definition contains only the installed executable path and the
`daemon` argument. Uninstalling the service preserves the local SQLite ledger.

ChainProof establishes tamper evidence for records it receives. It does not
establish that an agent, harness, importer, or machine reported truthfully.
