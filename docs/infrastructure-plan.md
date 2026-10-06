# Infrastructure milestones

All six milestones below are implemented and verified in the isolated local lab. Provider outcomes are simulated through HTTP adapters. PostgreSQL persistence, signature checks, state transitions and the local SIP OPTIONS exchange run as actual application code. See [the capability map and walkthrough](infrastructure.md) for each boundary.

1. Add an isolated provider lab and HTTP adapters for Twilio SMS, voice, numbers and SIP trunk resources plus Resend email. No provider credentials are available. Runtime activation stays local.
2. Persist delivery jobs before submission. Separate provider acceptance from delivery. Deduplicate submissions and callbacks. Retry only definite rejection with bounded backoff. Preserve ambiguous submissions for investigation.
3. Verify webhook signatures before processing. Persist replies and apply STOP, unsubscribe, bounce and complaint suppression atomically. Retain event evidence.
4. Add recorded delivery health, DNS record inspection and a bounded email volume ramp for explicitly opted-in test recipients. Do not fabricate inbox placement, spam labels or engagement.
5. Connect a functional Infrastructure page to jobs, evidence, inbox triage, provisioning and the local scenarios. Preserve the existing desk and safeguards.
6. Run security and integration tests against isolated PostgreSQL and local HTTP providers. Document the runnable workflow and the remaining production requirements.

Live carrier delivery, SIP audio, Apple Messages for Business onboarding, real mailbox synchronization and production rollout require provider accounts and deployment access. Ordinary iMessage now has an optional Mac bridge adapter and isolated simulator. It is separate from Apple Messages for Business and remains unverified on a live Mac. Protection means access controls, verified events, suppression and bounded delivery rather than evasion of provider rules.
