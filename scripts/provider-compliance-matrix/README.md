# Provider Compliance Matrix (#316)

Manual integration test script that verifies every provider passes the 6-case
compliance matrix before being released.

## When to Run

- Provider adapter layer change
- Daemon release (vX.Y.Z tag)
- New provider integration

## Cases (Parker product spec)

| # | Scenario | Expectation | Verification |
|---|----------|-------------|--------------|
| 1 | DM substantive question ("现在几点了？请回答") | Visible text reply, no must_reply_failure, no leak | Check task result: mrf=false, output non-empty, no JSON/protocol text |
| 2 | DM greeting ("hi") | Visible response (sticker/reaction OK), no text rationale | Check chat for visible output, no "no reply" explanation |
| 3 | Channel @ question | Visible reply in channel | Check channel message exists |
| 4 | Ambient chat (no @ "随便聊聊天气") | Silence or 👋 acceptable; NO "no reply" rationale text output | Check no channel message OR only reaction; no explanation text |
| 5 | Two directed messages from same person | Both get replies, no silent disappearance | Check both messages have visible responses |
| 6 | Leak check (across 1-5) | Zero JSON envelopes / protocol text / internal diagnostics in visible output | Regex check output for protocol patterns |

## Pass Criteria

- Cases 1-3: 100% pass (allows one auto-retry from #314)
- Case 4: zero rationale leak + acceptable reply rate
- Case 6: zero leak across all cases

## Usage

```bash
# Run all cases for a specific provider
go run scripts/provider-compliance-matrix/main.go --provider pi --server-url https://api.leagent.me

# Run a specific case
go run scripts/provider-compliance-matrix/main.go --provider pi --case 3

# Output format
go run scripts/provider-compliance-matrix/main.go --provider claude --output json
```
