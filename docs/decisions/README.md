# Architecture decision records

One file per decision, named `ADR-NNNN-short-title.md`. Phase-local choices live
in that phase directory instead.

| ADR | Decision | Status | Applies from |
|---|---|---|---|
| [0001](ADR-0001-mcp-tools-as-the-only-agent-hands.md) | The model reaches money only through MCP tools this process serves. No credentials, no HTTP client, no shell. | Accepted | Phase 3 |
| [0002](ADR-0002-no-razorpay-sdk.md) | A typed `net/http` client for the six `razorpay.Port` calls instead of the Razorpay SDK. | Accepted | Phase 1 |
| [0003](ADR-0003-server-side-policy-gate-two-layers.md) | The policy gate runs server side in two layers: tool middleware, then `policy.Evaluate` in every action handler. | Accepted | Phase 2, enforced phase 3 |
| [0004](ADR-0004-three-measurement-layers.md) | Live, replay, and fake results are reported separately and never merged into one table. | Accepted | Phase 1 |
