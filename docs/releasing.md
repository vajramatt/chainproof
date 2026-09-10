# Releasing ChainProof

Release is deliberate publication, not backup. Branches and merged commits
protect work; tag creates public version contract consumed by installer and Go
users.

## Prepare version

Use GitHub tags and releases as authority. Local-only tags may belong to other
work and must not determine next public version.

For release `vX.Y.Z`, update these source defaults in one reviewed commit:

- `cmd/chainproof/main.go` fallback version to `X.Y.Z`
- `scripts/install.sh` default tag to `vX.Y.Z`
- README `go install` example to `@vX.Y.Z`

Release builder injects tag without leading `v` into binary. Source fallback
still matters for `go install`, which bypasses release builder.

After publishing, change fallback on post-release `main` back to
`development`. Untagged source builds must not identify themselves as latest
release after behavior diverges from that tag.

## Verify candidate

```sh
make build
make test
make check
git diff --check
git status --short
```

`make check` verifies linker version injection. Review formatting changes before
commit. Confirm CI passes on exact main commit.

## Tag and build

Tag only with explicit release approval:

```sh
git tag -a vX.Y.Z -m "ChainProof vX.Y.Z"
git push origin vX.Y.Z
scripts/build-release.sh vX.Y.Z
```

Run one same-platform binary from extracted archive and confirm:

```text
chainproof X.Y.Z
```

Release assets are four archives plus `checksums.txt`. Generated `dist/`
contents remain untracked.

## Publish

Create GitHub release from exact annotated tag, attach all five assets, and
describe proof-boundary or compatibility changes precisely. Test installer
against published release before announcing it. Website deployment is separate
and requires its own explicit approval.
