package ops

import (
	"errors"
	"github.com/jackc/pgx/v5"
	"net/http"
	"sort"
	"strings"
	"time"
)

func (s *Server) desk(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := user(r)
	tx, err := s.Store.BeginRead(ctx, u.WorkspaceID)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback(ctx)
	assets, err := listAssets(ctx, tx)
	if err != nil {
		fail(w, err)
		return
	}
	featuredHistory := []Sample{}
	rows, err := tx.Query(ctx, "SELECT id,attempts,filtered,spam_label,observed_at,source FROM observations WHERE asset_id='line-delta' ORDER BY observed_at,id LIMIT 100")
	if err != nil {
		fail(w, err)
		return
	}
	for rows.Next() {
		var sample Sample
		if err = rows.Scan(&sample.ID, &sample.Attempts, &sample.Filtered, &sample.SpamLabel, &sample.ObservedAt, &sample.Source); err != nil {
			rows.Close()
			fail(w, err)
			return
		}
		featuredHistory = append(featuredHistory, sample)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		fail(w, err)
		return
	}
	recent, _, err := decisions(ctx, tx, "", "", "", 5, 0)
	if err != nil {
		fail(w, err)
		return
	}
	activity, err := auditList(ctx, tx, "")
	if err != nil {
		fail(w, err)
		return
	}
	// Overview activity omits free-form reasons. Full notes are available in authorized asset details.
	for i := range activity {
		activity[i].Reason = nil
	}
	var generation, unpublished int64
	var paused bool
	var resetAt time.Time
	if err = tx.QueryRow(ctx, "SELECT generation,replies_paused,reset_at FROM demo_state").Scan(&generation, &paused, &resetAt); err != nil {
		fail(w, err)
		return
	}
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM outbox WHERE published_at IS NULL").Scan(&unpublished); err != nil {
		fail(w, err)
		return
	}
	var checked, blocked, sent int64
	if err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE outcome='blocked'),(SELECT count(*) FROM messages WHERE delivered_at>now()-interval '24 hours') FROM decisions WHERE recorded_at>now()-interval '24 hours'`).Scan(&checked, &blocked, &sent); err != nil {
		fail(w, err)
		return
	}
	queueCtx, cancel := deadline(ctx)
	defer cancel()
	q := s.Store.Queue(queueCtx, generation, paused, unpublished)
	issues := []map[string]any{}
	for _, a := range assets {
		if a.Status == "quarantined" {
			issues = append(issues, map[string]any{"id": "restore-" + a.ID, "kind": "asset", "title": a.Name + " is quarantined", "detail": "Restoration requires a new clean observation. Review the stored prerequisites.", "lane": "Action required", "assetId": a.ID, "source": "PostgreSQL asset state", "severity": "warning"})
			continue
		}
		if a.Kind == "phone" && a.Sample != nil && a.Sample.Attempts > 0 && (float64(a.Sample.Filtered)/float64(a.Sample.Attempts) > .05 || a.Sample.SpamLabel) {
			issues = append(issues, map[string]any{"id": "degraded-" + a.ID, "kind": "asset", "title": a.Name + " is deteriorating", "detail": "The latest simulated provider observation exceeds the 5% filter threshold.", "lane": "Investigate", "assetId": a.ID, "source": "Stored provider observations", "severity": "warning"})
		}
		if a.Kind == "email" && a.EmailConfig != nil && (!a.EmailConfig.DKIM || a.EmailConfig.DMARC == "none") {
			issues = append(issues, map[string]any{"id": "email-" + a.ID, "kind": "asset", "title": a.Name + " needs configuration review", "detail": "The synthetic DNS fixture reports DKIM missing and DMARC unset. No live DNS check was made.", "lane": "Investigate", "assetId": a.ID, "source": "Synthetic email fixture", "severity": "warning"})
		}
	}
	// Keep a relevant blocked decision visible even when it leaves the recent-five list.
	var missingID, missingContact string
	var emailOpened bool
	err = tx.QueryRow(ctx, `SELECT d.id,d.contact_id,(d.evidence->>'emailOpenedAt') IS NOT NULL
 FROM decisions d JOIN contacts c ON c.workspace_id=d.workspace_id AND c.id=d.contact_id
 WHERE d.reason='NO_SMS_CONSENT' AND d.recorded_at>now()-interval '24 hours'
 AND (c.sms_consent_at IS NULL OR c.consent_source IS NULL) AND NOT c.dnc AND c.opted_out_at IS NULL
 ORDER BY d.recorded_at DESC,d.id DESC LIMIT 1`).Scan(&missingID, &missingContact, &emailOpened)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		fail(w, err)
		return
	}
	if err == nil {
		detail := "No explicit SMS consent was recorded at the latest blocked check."
		if emailOpened {
			detail = "An email was opened but explicit SMS consent was not recorded at the check."
		}
		issues = append(issues, map[string]any{"id": missingID, "kind": "decision", "title": "SMS permission is missing", "detail": detail, "lane": "Action required", "decisionId": missingID, "contactId": missingContact, "source": "Recorded eligibility decision", "severity": "blocked"})
	}
	if q.State == "unavailable" || q.Paused && (q.Pending == nil || *q.Pending > 0 || q.Unpublished > 0) {
		issues = append(issues, map[string]any{"id": "reply-queue", "kind": "queue", "title": "Reply processing needs attention", "detail": "Review the consumer state and transport backlog before resuming.", "lane": "Infrastructure", "source": "Redis stream and PostgreSQL outbox", "severity": "warning"})
	}
	priority := func(issue map[string]any) int {
		if issue["kind"] == "decision" {
			return 1
		}
		if issue["kind"] == "queue" {
			return 2
		}
		if id, ok := issue["assetId"].(string); ok && strings.HasPrefix(id, "email-") {
			return 3
		}
		return 0
	}
	sort.SliceStable(issues, func(i, j int) bool { return priority(issues[i]) < priority(issues[j]) })
	workerState := "available"
	last := s.Store.ReplySuccess.Load()
	if last == 0 {
		workerState = "starting"
	} else if s.Store.ReplyFailed.Load() || time.Now().UnixMilli()-last > 5000 {
		workerState = "degraded"
	} else if paused {
		workerState = "paused"
	}
	writeJSON(w, 200, map[string]any{"assets": assets, "featuredHistory": featuredHistory, "issues": issues, "decisions": recent, "activity": activity, "queue": q, "metrics": map[string]any{"checked": checked, "blocked": blocked, "delivered": sent, "window": "last_24_hours", "source": "PostgreSQL recorded decisions and simulated SMS receipts"}, "services": []map[string]any{{"name": "API", "state": "available", "source": "This successful response"}, {"name": "PostgreSQL", "state": "available", "source": "Completed workspace queries"}, {"name": "Redis", "state": map[bool]string{true: "unavailable", false: "available"}[q.State == "unavailable"], "source": "XINFO GROUPS"}, {"name": "Reply consumer", "state": workerState, "lastSuccessfulTick": last, "source": "Actual in-process worker"}}, "generation": generation, "resetAt": resetAt, "observedAt": time.Now().UTC(), "demo": true})
}
func (s *Server) conversations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tx, err := s.Store.BeginRead(ctx, user(r).WorkspaceID)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, "SELECT id,contact_id,asset_id,body,tag,received_at,persisted_at FROM replies ORDER BY received_at DESC,id LIMIT 30")
	if err != nil {
		fail(w, err)
		return
	}
	items := []Reply{}
	for rows.Next() {
		var item Reply
		if err = rows.Scan(&item.ID, &item.ContactID, &item.AssetID, &item.Body, &item.Tag, &item.ReceivedAt, &item.PersistedAt); err != nil {
			rows.Close()
			fail(w, err)
			return
		}
		item.ContactLabel = "Contact · " + item.ContactID
		items = append(items, item)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		fail(w, err)
		return
	}
	recentCalls, err := calls(ctx, tx)
	if err != nil {
		fail(w, err)
		return
	}
	assets, err := listAssets(ctx, tx)
	if err != nil {
		fail(w, err)
		return
	}
	var generation, unpublished int64
	var paused bool
	if err = tx.QueryRow(ctx, "SELECT generation,replies_paused FROM demo_state").Scan(&generation, &paused); err != nil {
		fail(w, err)
		return
	}
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM outbox WHERE published_at IS NULL").Scan(&unpublished); err != nil {
		fail(w, err)
		return
	}
	queueCtx, cancel := deadline(ctx)
	defer cancel()
	q := s.Store.Queue(queueCtx, generation, paused, unpublished)
	writeJSON(w, 200, map[string]any{"replies": items, "calls": recentCalls, "assets": assets, "queue": q, "observedAt": time.Now().UTC(), "demo": true})
}
