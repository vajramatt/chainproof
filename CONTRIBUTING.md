# Contributing

ChainProof welcomes adapters, verifier implementations, UI improvements, and
proof-format review. Run `make check` before opening a pull request.

Provider-specific formats belong under `integrations/` or behind the adapter
contract. The core proof package must remain provider- and harness-agnostic.
Changes to canonicalization or the bundle format require interoperability test
vectors and a documented version transition.
