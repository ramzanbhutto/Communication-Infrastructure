# API contracts

All application routes begin with `/api/v1`. JSON property names use camelCase. Timestamps are RFC 3339 with UTC offsets. The browser displays local time; CSV timestamps are UTC. Responses are not cached.

Mutation requests require `Content-Type: application/json`, a trusted local `Origin` and a valid session. Unknown fields, multiple JSON objects and bodies above 16 KiB are rejected. The reviewer may read and export, but mutations require the operator role.

Errors have `{ code, message, decisionId?, details? }`. Statuses include 401 for an absent session, 403 for authorization or origin rejection, 404 for an unavailable workspace record, 409 for a conflict, 422 for validation or eligibility rejection and 503 for an unconfirmed dependency result. A blocked attempt includes its stored decision ID. A 503 is not confirmation that a mutation failed to commit.

| Method and route | Contract |
| --- | --- |
| `POST /session` | `{ userId: "demo-operator" \| "demo-viewer" }`. Creates the HttpOnly session cookie. The only unauthenticated application mutation. |
| `GET /session` | `{ user: { id, workspaceId, name, role }, demo: true }`. |
| `DELETE /session` | Revokes the current session and clears its cookie. |
| `GET /desk` | Assets, measured featured history, issues, recent decisions, redacted audit activity, metrics, queue state, service states, generation, resetAt and observedAt. |
| `GET /assets` | Filters `kind=phone\|email`, `status=active\|quarantined`, `search`. Returns `{ items, total, observedAt }`. The fixed demo has four assets. |
| `GET /assets/{id}` | `{ asset, samples, audit, restoreProblems, observedAt }`. Up to 100 chronological samples and 30 recent audit entries. |
| `POST /assets/{id}/quarantine` | `{ reason, expectedVersion }`. Phone assets only. Reason is 3 to 300 characters. Returns `{ asset, changed }`. |
| `POST /assets/{id}/restore` | Same input and result. Validates post-quarantine observations and active calls. Rejections include prerequisites in `details`. |
| `POST /assets/{id}/recovery` | Same input. Records a clearly simulated clean sample for a quarantined phone line and increments its version. |
| `GET /contacts` | `{ items: [{ id, label, phone, email }] }`. Masked values, maximum 100 records. |
| `GET /contacts/{id}` | `{ contact, eligibility, source }`. Full authorized synthetic detail and current preview reasons keyed by phone line and channel. Preview does not include the time-dependent touch cap or workspace call concurrency. |
| `GET /decisions` | Filters `channel=sms\|call\|email`, `outcome=allowed\|blocked\|deferred`, `search`. Pagination `page` starts at 1. `pageSize` defaults to 20 and is capped at 100. Returns `{ items, total, page, pageSize, window, observedAt }`. Window is always the last 24 hours. |
| `GET /decisions/{id}` | `{ decision, explanation, explanationSource }`. Stored evidence is separated from a derived reason-code explanation. Detail remains available after the list's time window. |
| `GET /decisions/export` | Same filters. CSV contains all matching rows from the last 24 hours regardless of pagination, with masked contacts. More than 500 matching rows returns 422, never a truncated success. Header `X-Export-Scope: all-filtered-last-24-hours-masked`. |
| `POST /scrub` | `{ contactIds, lineId, channel }`. One to 50 contacts. Atomically records suppression/consent checks without calling or sending. `{ items: [{ contactId, reason, decisionId }], sent: false }`. Does not check transient call concurrency or the touch cap. |
| `POST /dial` | `{ contactId, lineId, message: "", requestKey }`. Uses an existing synthetic contact and line. Returns 201 `{ id, decisionId, status: "active", simulated: true }` or a recorded rejection. |
| `POST /sms` | `{ contactId, lineId, message, requestKey }`. Message is 1 to 480 Unicode characters after trimming. Returns 201 `{ id, decisionId, status: "delivered", simulated: true }` or a recorded rejection. |
| `GET /conversations` | `{ replies, calls, assets, queue, observedAt, demo: true }`. Up to 30 recent persisted replies and 20 calling sessions. |
| `POST /demo/replies` | `{ paused: boolean }`. Persists the consumer setting and audits changes. Does not claim events are processed before the worker commits them. |
| `POST /demo/reset` | `{ confirmation: "RESET DEMO" }`. Only the fixed demo workspace. Returns `{ generation, simulated: true }`. |

`requestKey` must contain 8 to 100 characters. Reuse it only to replay the identical request. Conflicting input returns `REQUEST_KEY_CONFLICT`. The UI does not retry mutations automatically.

An asset has `id`, `kind`, `name`, `address`, `status`, `version`, nullable quarantine fields, nullable `sample` and nullable `emailConfig`. A sample has attempts, filtered attempts, a spam label, observedAt and source. Filter rate is derived from its counts, not stored as an arbitrary score.

A decision has its IDs, channel, outcome, reason, recordedAt and `evidence`. Evidence captures masked contact identifiers, DNC status, opt-out and consent timestamps, consent source, warm signal, email-open timestamp, asset status/version, the sample, checkedAt, source and unavailable fields. The related-record links resolve current state separately.

A queue has `state`, nullable `pending`, `unpublished`, `paused`, nullable `error` and `checkedAt`. `pending` is Redis group pending plus lag. `unpublished` is the PostgreSQL outbox count. Null means no reliable Redis count was obtained. The API can return real persisted replies while reporting Redis unavailable.

`GET /health` is outside the application prefix and does not require a session. It reports coarse API, PostgreSQL and Redis availability with a checkedAt timestamp. It returns 503 when a dependency check fails. Operational metrics are session-scoped in `/desk`; no separate Prometheus endpoint or five-service interface is claimed.

Infrastructure jobs, signed provider events, inbox, SIP and volume-ramp routes are specified in [Infrastructure](infrastructure.md#api-additions). Demo reset also accepts `discardUnconfirmed: true` only after the operator explicitly chooses to discard unresolved synthetic lab jobs.

The optional iMessage compatibility, synchronization and scenario routes are documented in [the iMessage API contracts](imessage.md#application-api).

## Campaign and diagnostic APIs

See the [campaign contracts](campaigns-and-email.md#api) for the additive campaign and email diagnostic routes. Existing provider and outreach routes keep their contracts.
