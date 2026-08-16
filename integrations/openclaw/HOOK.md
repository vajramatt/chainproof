---
name: chainproof
description: "Record OpenClaw sessions in a local, tamper-evident ChainProof ledger"
metadata:
  openclaw:
    emoji: "🔗"
    events: [command:new, command:stop, command:reset, message:received, after_tool_call, message:sent]
    os: ["darwin", "linux", "win32"]
---

# ChainProof for OpenClaw

Start `chainproof serve`, then install this directory as an OpenClaw hook. No
account or API key is required. The hook sends events only to
`http://127.0.0.1:7331` by default.

Set `CHAINPROOF_URL` to choose another local endpoint. Message and tool bodies
are hashed but omitted by default; set `CHAINPROOF_STORE_CONTENT=true` to keep
the content in the local ledger.

Events submitted by the hook are explicitly marked `reported`: the hash chain
protects the received record from undetected edits but cannot establish that
OpenClaw or its model reported truthfully.
