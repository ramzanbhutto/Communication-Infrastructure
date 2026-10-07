# Campaigns and email diagnostics

This extension adds an email and consented SMS sequence engine to the existing operations desk. PostgreSQL owns scheduling, execution identity and assessment history. Provider delivery remains synthetic. Native DNS queries are optional and separate from outreach.

## Run and demonstrate

Use the existing setup:

```sh
make up
make dev-api
```

In another terminal:

```sh
make dev-web
```

Open `http://localhost:5173` as the operator. Startup applies the additive migration and fills absent local DNS assessments. If you reset the demo, run diagnostics again before activating an email campaign. Reset clears campaign history in the same isolated workspace.

### A reply stops the follow-up

1. Open **Email diagnostics**, select North and run diagnostics with the default `mail` selector and synthetic source.
2. Open **Campaigns** and select **Create campaign**. Save the default two-email sequence. Its first step is immediate and the follow-up waits ten minutes after confirmed delivery. The demonstration window covers all hours; edit it for a time-zone scenario.
3. Activate the draft. Enroll c102 with its explicitly selected recipient time zone. The default is UTC. c103 and c104 demonstrate rejected DNC and opt-out enrollment.
4. Observe the first delivery, inspect its authenticated event and open its recorded policy decision. The enrollment advances to the delayed second step.
5. In **Infrastructure → Provider inbox**, simulate an email reply for c102. Return to the campaign. Its enrollment is replied, no follow-up can be submitted and the audit records the transition.
6. Pause, resume or permanently cancel a campaign. Reviewers can inspect but cannot mutate.

### Configuration failure defers outreach

1. Select East in **Email diagnostics**. Apply the valid configuration fixture and inspect it.
2. Create a campaign using East, activate it and enroll c102. To demonstrate a failure before the first job, change East to the timeout fixture before enrollment. A longer first-step delay also gives time to change configuration before scheduling.
3. The enrollment defers with `EMAIL_DIAGNOSTICS_REQUIRED`. A timeout is unavailable, never a missing-record claim.
4. Apply the valid fixture and inspect it again. The same enrollment becomes eligible for reevaluation. The worker rechecks permission and capacity before creating or submitting work.
5. Inspect both assessments and the delivery evidence. Correction makes work eligible; it does not override consent or guarantee a provider outcome.

### Message authentication

The **Message authentication lab** signs a synthetic message with an ephemeral Ed25519 key and verifies the actual signature. Compare aligned, unaligned and altered-body scenarios. A signature can pass while strict DMARC alignment fails. An altered body fails verification.

The verifier intentionally supports only unfolded, single-signature, simple/simple Ed25519 fixtures. It is not a general inbound email verifier. Its generated key is separate from the public DNS configuration fixtures. It performs no send and does not measure inbox placement.

## Scheduling and transactions

Campaign definitions have up to six ordered email/SMS steps and a selected pool of existing sending assets. Supported template fields are `{name}` and `{contact_id}`. Activated definitions cannot be edited. The version field detects conflicting state changes; it is not a count of definition revisions alone.

Each contact has one enrollment per campaign. The enrollment records the recipient IANA time zone rather than guessing from an area code. Sending windows use local calendar dates, include daylight-saving handling and optionally exclude weekends. Step delays follow the previous confirmed delivery timestamp.

The scheduler reserves a delivery job and its unique `(workspace, enrollment, step)` execution in one transaction. It calls the same queueing service as manual requests. It does not send through an internal HTTP request or a second provider path. A rollback leaves neither execution nor job.

A workspace advisory lock gives campaign controls, scheduling, submission checks and imported reply handling a consistent order. This intentionally serializes the local demo's campaign work. Row security and composite foreign keys still enforce workspace boundaries. The worker currently processes the synthetic workspace only and runs in the API process.

An enrollment awaiting confirmation advances only after a verified delivered state. Unknown outcomes remain held. Accepted is not delivered. A failed or suppressed step stops the sequence. No scheduler timer retries an unknown send. Explicit verification of an existing provider record remains available in Infrastructure.

Pause is checked again before submission. Cancellation suppresses unsent campaign jobs and stops enrollments atomically with its audit event. A reply stops active enrollments across campaigns and suppresses their queued jobs in the reply persistence transaction. Opt-outs, bounces and complaints retain the existing suppression behavior. Already submitted requests cannot be recalled.

Activating a campaign configures shared sending-asset limits: ten reserved jobs per UTC day and thirty seconds between submissions. Manual infrastructure jobs and email ramps share those limits. Rejected provider requests still consume a reservation. These limits supplement the existing combined three-contact-touch rolling limit. They are demonstration policy, not legal or provider guarantees. Multiple email mailboxes per domain and adaptive mailbox selection are not implemented; the pool contains existing domain assets.

## Diagnostics and evidence

The DNS inspector uses the same resolver interface for named fixtures and optional native queries. Each assessment records domain, selector, source, timestamp, raw records and explanations. The UI exposes the latest ten checks and historical differences. The database retains older checks.

- MX inspection distinguishes routes, null MX, missing records and resolver failures. It does not verify individual recipient mailboxes.
- SPF inspection parses explicit IP mechanisms, follows bounded includes and redirects, detects conflicts and cycles and reports static lookup terms. This count is a lower bound. Contextual mechanisms and macros do not establish sender authorization. This is not a complete SPF `check_host` implementation.
- DKIM inspection parses RSA or Ed25519 public keys and identifies revoked or invalid keys. A configured selector is required; the application does not enumerate selectors.
- DMARC inspection validates the explicit domain policy and alignment tags. Organizational-domain discovery and full message alignment evaluation are outside the DNS inspector.

The campaign configuration gate requires a current, passing assessment under 24 hours old. Changing a fixture invalidates the old passing evidence until a new check matches the scenario. This gate concerns configuration only. Real reputation, DNSBL feeds, SMTP mailbox probes and inbox-placement tests are not implemented.

Native DNS is opt-in. Configure only a domain you own:

```sh
EMAIL_DIAGNOSTICS_TARGETS=email-north=your-owned-domain.example make dev-api
```

Then explicitly choose **Native DNS** in Email diagnostics. The setting maps an existing synthetic asset to a separate public diagnostic target. It does not change the provider's synthetic sender or enable live outreach. Native queries have a three-second deadline and a bounded SPF inspection budget. Failures are recorded without switching to fixtures. There are no arbitrary HTTP fetches from diagnostic input.

## API

Existing session, Origin, JSON bounds and operator checks apply. GET routes permit reviewers. All writes require an operator. Timestamps are RFC 3339 UTC.

| Route | Behavior |
| --- | --- |
| `GET /api/v1/campaigns?search=&page=1` | Name search, 20 campaigns per page, enrollment state counts and workspace assets |
| `POST /api/v1/campaigns` | Creates a validated draft with name, steps, assetIds, timezone, startHour, endHour and weekdaysOnly |
| `GET /api/v1/campaigns/{id}?page=1` | Definition, 50 enrollments per page, their executions and latest 30 audit entries |
| `POST /api/v1/campaigns/{id}/draft` | Draft update with expectedVersion; active definitions return 409 |
| `POST /api/v1/campaigns/{id}/activate`, `/pause`, `/resume`, `/cancel` | Guarded transition with expectedVersion; conflicts return 409 |
| `POST /api/v1/campaigns/{id}/enroll` | `{contactIds, timezone}`; up to 20 contacts with explicit per-contact results |
| `GET /api/v1/email-diagnostics` | Workspace email assets, latest assessments, campaign gate and seven-day simulated delivery counts |
| `POST /api/v1/email-diagnostics/{assetId}/check` | `{selector, mode: "fixture" or "native"}`; persists evidence including unavailable results |
| `POST /api/v1/email-diagnostics/{assetId}/fixture` | `{scenario: "healthy", "broken", "timeout" or "conflicting"}`; changes local fixtures, retains history |
| `POST /api/v1/email-diagnostics/message-fixture` | `{scenario: "aligned", "unaligned" or "tampered"}`; creates and verifies a local signed message |

A native target that is not configured returns `DNS_NOT_CONFIGURED`. Failed checks do not report success. Campaign transitions cannot bypass failed diagnostics, quarantine, consent or opt-out suppression. Future work includes a production identity system, independently deployed workers, richer mailbox models, standards-complete authentication diagnostics and live provider validation.

## Verification

Run `make check`, `make test` and `make build`. Backend tests cover scheduler competition, atomic rollback, role and tenant boundaries, reply/STOP suppression, unknown-state holds, shared quotas, pause, resume, cancellation and domain recovery. Unit tests cover time zones, DNS failures, parsing and actual signature verification. Browser tests exercise the two complete scenarios, read-only access, keyboard dismissal and mobile layouts alongside the original workflows.

Actual captures: [campaigns](screenshots/campaigns.png), [email diagnostics](screenshots/email-diagnostics.png), [mobile campaigns](screenshots/campaigns-mobile.png) and [mobile diagnostics](screenshots/email-diagnostics-mobile.png).

To reproduce them, finish tests first and build the application. Start a separate server using the test database:

```sh
docker compose --profile test up -d --wait postgres-test redis-test
DEMO_MODE=true INFRA_LAB=true IMESSAGE_LAB=true \
  DATABASE_URL='postgres://ops_app:ops_demo_app@127.0.0.1:55443/covent_ops_test?sslmode=disable' \
  MIGRATION_DATABASE_URL='postgres://ops_admin:ops_demo_admin@127.0.0.1:55443/covent_ops_test?sslmode=disable' \
  REDIS_URL=redis://127.0.0.1:56390/0 \
  LISTEN_ADDR=127.0.0.1:2063 ALLOWED_ORIGIN=http://127.0.0.1:2063 ./bin/ops
```

In another terminal, explicitly allow resetting those test fixtures:

```sh
OPS_SCREENSHOT_ISOLATED=true node scripts/campaign-screenshots.mjs
```

Stop the screenshot server before running tests again. The script uses port 2063 and resets its synthetic workspace. Its flag confirms your choice; it does not verify the server's database URL. Never use the main demo database for this capture server.
