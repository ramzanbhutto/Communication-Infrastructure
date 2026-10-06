package ops

import (
	"communication-infrastructure/internal/provider"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"net/http"
	"time"
)

func (s *Server) infrastructure(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tx, err := s.Store.BeginRead(ctx, user(r).WorkspaceID)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, "SELECT "+jobColumns+" FROM delivery_jobs ORDER BY created_at DESC LIMIT 50")
	if err != nil {
		fail(w, err)
		return
	}
	jobs := []DeliveryJob{}
	for rows.Next() {
		j, e := scanJob(rows)
		if e != nil {
			rows.Close()
			fail(w, e)
			return
		}
		jobs = append(jobs, publicJob(j))
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		fail(w, err)
		return
	}
	assets, err := listAssets(ctx, tx)
	if err != nil {
		fail(w, err)
		return
	}
	rows, err = tx.Query(ctx, "SELECT id,phone,email,opted_out_at IS NOT NULL,dnc FROM contacts ORDER BY id")
	if err != nil {
		fail(w, err)
		return
	}
	contacts := []map[string]any{}
	for rows.Next() {
		var id, phone, email string
		var opted, dnc bool
		if err = rows.Scan(&id, &phone, &email, &opted, &dnc); err != nil {
			rows.Close()
			fail(w, err)
			return
		}
		contacts = append(contacts, map[string]any{"id": id, "phone": MaskPhone(phone), "email": MaskEmail(email), "suppressed": opted || dnc})
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		fail(w, err)
		return
	}
	rows, err = tx.Query(ctx, "SELECT id,contact_id,asset_id,channel,state,version,received_at,source FROM infrastructure_inbox ORDER BY received_at DESC LIMIT 30")
	if err != nil {
		fail(w, err)
		return
	}
	inbox := []map[string]any{}
	for rows.Next() {
		var id, contact, asset, channel, state, source string
		var version int64
		var at time.Time
		if err = rows.Scan(&id, &contact, &asset, &channel, &state, &version, &at, &source); err != nil {
			rows.Close()
			fail(w, err)
			return
		}
		inbox = append(inbox, map[string]any{"id": id, "contactId": contact, "assetId": asset, "channel": channel, "state": state, "version": version, "receivedAt": at, "source": source})
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		fail(w, err)
		return
	}
	rows, err = tx.Query(ctx, "SELECT asset_id,records,checked_at FROM infrastructure_dns")
	if err != nil {
		fail(w, err)
		return
	}
	dns := []map[string]any{}
	for rows.Next() {
		var asset string
		var raw []byte
		var at time.Time
		if err = rows.Scan(&asset, &raw, &at); err != nil {
			rows.Close()
			fail(w, err)
			return
		}
		var records provider.DNSAssessment
		if err = json.Unmarshal(raw, &records); err != nil {
			rows.Close()
			fail(w, err)
			return
		}
		dns = append(dns, map[string]any{"assetId": asset, "records": records, "checkedAt": at})
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		fail(w, err)
		return
	}
	rows, err = tx.Query(ctx, `SELECT asset_id,enabled,started_at,least(maximum,daily_start+daily_step*greatest(0,floor(extract(epoch FROM (now()-started_at))/86400)::int)),(SELECT count(*) FROM delivery_jobs j WHERE j.asset_id=email_ramps.asset_id AND j.channel='email' AND j.created_at>=date_trunc('day',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC') FROM email_ramps`)
	if err != nil {
		fail(w, err)
		return
	}
	ramps := []map[string]any{}
	for rows.Next() {
		var asset string
		var enabled bool
		var at time.Time
		var cap, used int
		if err = rows.Scan(&asset, &enabled, &at, &cap, &used); err != nil {
			rows.Close()
			fail(w, err)
			return
		}
		ramps = append(ramps, map[string]any{"assetId": asset, "enabled": enabled, "startedAt": at, "dailyLimit": cap, "usedToday": used})
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		fail(w, err)
		return
	}
	var total, accepted, confirmed, unknown, filtered int
	err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE state='accepted'),count(*) FILTER(WHERE state IN ('delivered','completed')),count(*) FILTER(WHERE state='unknown'),count(*) FILTER(WHERE last_error='30007') FROM delivery_jobs WHERE created_at>now()-interval '24 hours'`).Scan(&total, &accepted, &confirmed, &unknown, &filtered)
	if err != nil {
		fail(w, err)
		return
	}
	mode := "disabled"
	if s.Store.ProviderLab != nil {
		mode = "local_lab"
	}
	var sip *provider.SIPObservation
	var sipRaw []byte
	err = tx.QueryRow(ctx, "SELECT observation FROM infrastructure_sip").Scan(&sipRaw)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		fail(w, err)
		return
	}
	if err == nil {
		sip = &provider.SIPObservation{}
		if err = json.Unmarshal(sipRaw, sip); err != nil {
			fail(w, err)
			return
		}
	}
	workerState := "disabled"
	var workerAt *time.Time
	if mode == "local_lab" {
		workerState = "initializing"
		if tick := s.Store.DeliverySuccess.Load(); tick > 0 {
			at := time.UnixMilli(tick).UTC()
			workerAt = &at
			workerState = "available"
			if time.Since(at) > 20*time.Second {
				workerState = "stale"
			}
		}
		if s.Store.DeliveryFailed.Load() {
			workerState = "unavailable"
		}
	}
	writeJSON(w, 200, map[string]any{"sipProbe": sip, "deliveryWorker": map[string]any{"state": workerState, "lastSuccessfulTick": workerAt}, "mode": mode, "liveEnabled": false, "imessageEnabled": s.Store.IMessageBridge != nil, "jobs": jobs, "assets": assets, "contacts": contacts, "inbox": inbox, "dns": dns, "ramps": ramps, "metrics": map[string]any{"total": total, "accepted": accepted, "confirmed": confirmed, "unknown": unknown, "filtered": filtered, "window": "last_24_hours", "source": "Persisted local provider jobs and verified callbacks"}, "observedAt": time.Now().UTC(), "limits": []string{"No real carrier delivery or SIP audio", "No external spam-label feed or inbox-placement measurement", "Email ramp controls volume; it does not manufacture engagement", "Optional iMessage uses a simulated Mac bridge; live activation requires a verified Mac transport", "Published demo identities are unsuitable for live activation"}})
}
func (s *Server) deliveryCreate(w http.ResponseWriter, r *http.Request) {
	var in DeliveryInput
	if !decode(w, r, &in) {
		return
	}
	result, err := s.Store.QueueDelivery(r.Context(), user(r), in)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, result.Status, result.Body)
}
func (s *Server) deliveryDetail(w http.ResponseWriter, r *http.Request) {
	tx, err := s.Store.BeginRead(r.Context(), user(r).WorkspaceID)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	j, err := scanJob(tx.QueryRow(r.Context(), "SELECT "+jobColumns+" FROM delivery_jobs WHERE id=$1", r.PathValue("id")))
	if err != nil {
		fail(w, err)
		return
	}
	rows, err := tx.Query(r.Context(), "SELECT id,kind,source,provider_code,occurred_at,received_at FROM delivery_events WHERE job_id=$1 ORDER BY received_at,id", j.ID)
	if err != nil {
		fail(w, err)
		return
	}
	events := []map[string]any{}
	for rows.Next() {
		var id, kind, source string
		var code *string
		var occurred, received time.Time
		if err = rows.Scan(&id, &kind, &source, &code, &occurred, &received); err != nil {
			rows.Close()
			fail(w, err)
			return
		}
		events = append(events, map[string]any{"id": id, "kind": kind, "source": source, "providerCode": code, "occurredAt": occurred, "receivedAt": received})
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"job": publicJob(j), "events": events, "observedAt": time.Now().UTC()})
}
func (s *Server) infrastructureScenario(w http.ResponseWriter, r *http.Request) {
	if s.Store.ProviderLab == nil {
		writeJSON(w, 503, Problem{Code: "LAB_DISABLED", Message: "The isolated provider lab is disabled."})
		return
	}
	var in struct {
		Channel   string `json:"channel"`
		ContactID string `json:"contactId"`
		AssetID   string `json:"assetId"`
		Body      string `json:"body"`
	}
	if !decode(w, r, &in) {
		return
	}
	if (in.Channel != "sms" && in.Channel != "email") || ValidateMessage(in.Body) != nil {
		writeJSON(w, 422, Problem{Code: "INVALID_INPUT", Message: "Choose SMS or email and enter bounded reply text."})
		return
	}
	tx, err := s.Store.BeginRead(r.Context(), user(r).WorkspaceID)
	if err != nil {
		fail(w, err)
		return
	}
	c, err := contactByID(r.Context(), tx, in.ContactID, false)
	if err != nil {
		tx.Rollback(r.Context())
		fail(w, err)
		return
	}
	a, err := assetByID(r.Context(), tx, in.AssetID, false)
	tx.Rollback(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	from, to := c.Phone, a.Address
	if in.Channel == "email" {
		if a.Kind != "email" {
			writeJSON(w, 422, Problem{Code: "EMAIL_ASSET_REQUIRED", Message: "Choose an email asset."})
			return
		}
		from = c.Email
		to = "operator@" + a.Address
	} else if a.Kind != "phone" {
		writeJSON(w, 422, Problem{Code: "PHONE_LINE_REQUIRED", Message: reasonText["PHONE_LINE_REQUIRED"]})
		return
	}
	if err = s.Store.ProviderLab.Inbound(r.Context(), in.Channel, from, to, in.Body); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"recorded": true, "simulated": true})
}
func (s *Server) deliveryVerify(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Confirmation string `json:"confirmation"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Confirmation != "VERIFY LAB RECORD" || s.Store.ProviderLab == nil {
		writeJSON(w, 422, Problem{Code: "CONFIRMATION_REQUIRED", Message: "Explicit verification of a local lab record is required."})
		return
	}
	tx, err := s.Store.BeginRead(r.Context(), user(r).WorkspaceID)
	if err != nil {
		fail(w, err)
		return
	}
	j, err := scanJob(tx.QueryRow(r.Context(), "SELECT "+jobColumns+" FROM delivery_jobs WHERE id=$1", r.PathValue("id")))
	tx.Rollback(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	if j.State != "unknown" && j.State != "accepted" {
		writeJSON(w, 409, Problem{Code: "NOT_UNCONFIRMED", Message: "Only accepted or unconfirmed jobs need provider verification."})
		return
	}
	if j.Channel == "imessage" {
		if j.ProviderID == nil || j.ContactID == nil {
			writeJSON(w, 409, Problem{Code: "IMESSAGE_RECONCILIATION_REQUIRED", Message: "No bridge message GUID was returned. Inspect the Mac before taking another action. This job cannot be verified automatically or safely resubmitted."})
			return
		}
		if err = s.Store.syncIMessage(r.Context(), user(r).WorkspaceID, *j.ContactID); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"verified": true, "simulated": s.Store.IMessageLab != nil})
		return
	}
	receipt, channel, inspectErr := s.Store.ProviderLab.Inspect(r.Context(), j.ID)
	if inspectErr != nil || channel != j.Channel {
		writeJSON(w, 503, Problem{Code: "PROVIDER_RECORD_UNAVAILABLE", Message: "The existing provider lab record could not be verified. A lab restart loses in-memory records. Do not resubmit."})
		return
	}
	tx, err = s.Store.Begin(r.Context(), user(r).WorkspaceID, false)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	j, err = scanJob(tx.QueryRow(r.Context(), "SELECT "+jobColumns+" FROM delivery_jobs WHERE id=$1 FOR UPDATE", j.ID))
	if err != nil {
		fail(w, err)
		return
	}
	if j.ProviderID != nil && *j.ProviderID != receipt.ID {
		writeJSON(w, 409, Problem{Code: "PROVIDER_ID_CONFLICT", Message: "Provider identities do not match."})
		return
	}
	management := j.Channel == "provision" || j.Channel == "trunk"
	if j.State == "unknown" || j.State == "accepted" {
		state := "accepted"
		if management {
			state = "completed"
		}
		if _, err = tx.Exec(r.Context(), "UPDATE delivery_jobs SET provider_id=$2,state=$3,last_error=NULL,updated_at=now() WHERE id=$1", j.ID, receipt.ID, state); err != nil {
			fail(w, err)
			return
		}
		if j.Channel == "provision" {
			if _, err = tx.Exec(r.Context(), "INSERT INTO assets(workspace_id,id,kind,name,address,status) VALUES($1,$2,'phone',$3,$4,'active') ON CONFLICT DO NOTHING", user(r).WorkspaceID, receipt.ID, j.Subject, j.Destination); err != nil {
				fail(w, err)
				return
			}
		}
		if err = audit(r.Context(), tx, user(r), "provider.record_verified", j.AssetID, j.ID, "Authenticated lookup of an existing lab resource. No new provider submission."); err != nil {
			fail(w, err)
			return
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		fail(w, err)
		return
	}
	if err = s.Store.ProviderLab.Emit(r.Context(), j.ID); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"verified": true, "simulated": true})
}
func (s *Server) inboxDetail(w http.ResponseWriter, r *http.Request) {
	tx, err := s.Store.BeginRead(r.Context(), user(r).WorkspaceID)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var body, subject, contact, channel, state string
	var version int64
	err = tx.QueryRow(r.Context(), "SELECT body,subject,contact_id,channel,state,version FROM infrastructure_inbox WHERE id=$1", r.PathValue("id")).Scan(&body, &subject, &contact, &channel, &state, &version)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"body": body, "subject": subject, "contactId": contact, "channel": channel, "state": state, "version": version})
}
func (s *Server) inboxResolve(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ExpectedVersion int64 `json:"expectedVersion"`
	}
	if !decode(w, r, &in) {
		return
	}
	tx, err := s.Store.Begin(r.Context(), user(r).WorkspaceID, false)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var version int64
	var state string
	err = tx.QueryRow(r.Context(), "SELECT version,state FROM infrastructure_inbox WHERE id=$1 FOR UPDATE", r.PathValue("id")).Scan(&version, &state)
	if err != nil {
		fail(w, err)
		return
	}
	if state != "resolved" {
		if version != in.ExpectedVersion {
			writeJSON(w, 409, Problem{Code: "VERSION_CONFLICT", Message: "This reply changed. Refresh before resolving it."})
			return
		}
		if _, err = tx.Exec(r.Context(), "UPDATE infrastructure_inbox SET state='resolved',version=version+1 WHERE id=$1", r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		if err = audit(r.Context(), tx, user(r), "inbox.resolved", nil, r.PathValue("id"), "Reviewed local provider reply."); err != nil {
			fail(w, err)
			return
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"state": "resolved"})
}
func (s *Server) dnsInspect(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Selector string `json:"selector"`
	}
	if !decode(w, r, &in) {
		return
	}
	if s.Store.ProviderLab == nil {
		writeJSON(w, 503, Problem{Code: "LAB_DISABLED", Message: "Local DNS inspection is disabled."})
		return
	}
	tx, err := s.Store.Begin(r.Context(), user(r).WorkspaceID, false)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	a, err := assetByID(r.Context(), tx, r.PathValue("id"), true)
	if err != nil {
		fail(w, err)
		return
	}
	if a.Kind != "email" {
		writeJSON(w, 422, Problem{Code: "EMAIL_ASSET_REQUIRED", Message: "Choose an email asset."})
		return
	}
	records, err := provider.InspectDNS(r.Context(), provider.LabDNS{}, a.Address, in.Selector, "local_dns_fixture")
	if err != nil {
		writeJSON(w, 422, Problem{Code: "INVALID_SELECTOR", Message: "Use a single DNS label for the DKIM selector."})
		return
	}
	raw, _ := json.Marshal(records)
	if _, err = tx.Exec(r.Context(), "INSERT INTO infrastructure_dns(workspace_id,asset_id,records) VALUES($1,$2,$3) ON CONFLICT(workspace_id,asset_id) DO UPDATE SET records=excluded.records,checked_at=clock_timestamp()", user(r).WorkspaceID, a.ID, raw); err != nil {
		fail(w, err)
		return
	}
	if err = audit(r.Context(), tx, user(r), "email.dns_inspected", a.ID, nil, "Local DNS fixtures; record presence does not prove deliverability."); err != nil {
		fail(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"records": records})
}
func (s *Server) rampUpdate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if !decode(w, r, &in) {
		return
	}
	tx, err := s.Store.Begin(r.Context(), user(r).WorkspaceID, false)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	a, err := assetByID(r.Context(), tx, r.PathValue("id"), true)
	if err != nil {
		fail(w, err)
		return
	}
	if a.Kind != "email" {
		writeJSON(w, 422, Problem{Code: "EMAIL_ASSET_REQUIRED", Message: "Choose an email asset."})
		return
	}
	if in.Enabled {
		var raw []byte
		err = tx.QueryRow(r.Context(), "SELECT records FROM infrastructure_dns WHERE asset_id=$1 AND checked_at>now()-interval '24 hours'", a.ID).Scan(&raw)
		var dns provider.DNSAssessment
		if err != nil || json.Unmarshal(raw, &dns) != nil || dns.SPF.State != "present" || dns.DKIM.State != "present" || dns.DMARC.State != "present" {
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				fail(w, err)
				return
			}
			writeJSON(w, 422, Problem{Code: "DNS_NOT_READY", Message: "Inspect DNS first. Recent SPF, DKIM and DMARC records are required for the lab volume ramp."})
			return
		}
	}
	_, err = tx.Exec(r.Context(), "INSERT INTO email_ramps(workspace_id,asset_id,enabled) VALUES($1,$2,$3) ON CONFLICT(workspace_id,asset_id) DO UPDATE SET enabled=excluded.enabled", user(r).WorkspaceID, a.ID, in.Enabled)
	if err != nil {
		fail(w, err)
		return
	}
	if err = audit(r.Context(), tx, user(r), "email.ramp_changed", a.ID, nil, "Opt-in test-recipient volume ramp setting changed."); err != nil {
		fail(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"enabled": in.Enabled})
}
