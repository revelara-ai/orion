# STPA Hazard Decision Record

| UCA | Control action | Type | Hazard | Disposition | Rationale | By |
|---|---|---|---|---|---|---|
| UCA1 | CA1 | not_provided | handler never responds (hang) | **controlled** | handler always responds; bounded by server timeouts | developer |
| UCA2 | CA1 | provided_incorrectly | wrong/stale time or wrong format returned | **controlled** | UTC + JSON ResponseContract verified by behavioral + empirical proof | developer |
| UCA3 | CA1 | wrong_timing | response exceeds acceptable latency / request timeout | **controlled** | server read/write timeouts bound latency | developer |
| UCA4 | CA2 | wrong_duration | connection held open indefinitely (slowloris) | **controlled** | ReadHeaderTimeout mitigates slowloris | developer |
| UCA5 | CA3 | not_provided | SLO breached but no alert fires (silent SLA miss) | **accepted_gap** | observability/alerting not yet implemented; accepted gap — revisit when telemetry lands (brownfield-closable) | developer |
| UCA6 | CA3 | wrong_timing | alert fires only after a sustained breach | **accepted_gap** | SLO burn-rate alerting timeliness not yet implemented; accepted gap | developer |
| UCA7 | CA4 | not_provided | alert fires but no ack/remediation (prolonged MTTR) | **accepted_gap** | on-call/runbook/escalation not yet wired; accepted gap — ties to delivery F2 runbook+escalation | developer |

- controlled or accepted: 7/7
- **accepted gaps (to revisit): 3**
- **open (blocking): 0**
