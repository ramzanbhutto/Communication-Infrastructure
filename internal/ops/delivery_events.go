package ops

import (
	"communication-infrastructure/internal/provider"
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
)

func (s *Server) providerHook(w http.ResponseWriter, r *http.Request) {
	lab := s.Store.ProviderLab
	if lab == nil {
		writeJSON(w, 503, Problem{Code: "PROVIDER_DISABLED", Message: "No webhook provider is configured."})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if r.URL.Path == "/hooks/resend" {
		s.resendHook(w, r)
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/x-www-form-urlencoded" || r.ParseForm() != nil {
		writeJSON(w, 422, Problem{Code: "INVALID_EVENT", Message: "A valid form event is required."})
		return
	}
	canonical := s.Store.CallbackOrigin + r.URL.RequestURI()
	if !provider.VerifyTwilio(lab.Token, canonical, r.Header.Get("X-Twilio-Signature"), r.PostForm) || r.PostForm.Get("AccountSid") != lab.Account {
		writeJSON(w, 403, Problem{Code: "INVALID_SIGNATURE", Message: "Provider verification failed."})
		return
	}
	if r.URL.Path == "/hooks/twilio/inbound" {
		err = s.Store.persistInbound(r.Context(), "twilio:"+r.PostForm.Get("MessageSid"), "sms", r.PostForm.Get("From"), r.PostForm.Get("To"), "", r.PostForm.Get("Body"))
	} else {
		status := r.PostForm.Get("MessageStatus")
		sid := r.PostForm.Get("MessageSid")
		if sid == "" {
			sid = r.PostForm.Get("CallSid")
			status = r.PostForm.Get("CallStatus")
		}
		err = s.Store.applyDeliveryEvent(r.Context(), Hash(canonical+r.PostForm.Encode()), r.URL.Query().Get("job"), sid, status, r.PostForm.Get("ErrorCode"), time.Now().UTC())
	}
	if err != nil {
		eventFailure(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(200)
	w.Write([]byte("<Response/>"))
}
func (s *Server) resendHook(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, 413, Problem{Code: "EVENT_TOO_LARGE", Message: "The provider event exceeds the limit."})
		return
	}
	lab := s.Store.ProviderLab
	if !provider.VerifySvix(lab.WebhookSecret, r.Header.Get("svix-id"), r.Header.Get("svix-timestamp"), r.Header.Get("svix-signature"), raw, time.Now()) {
		writeJSON(w, 403, Problem{Code: "INVALID_SIGNATURE", Message: "Provider verification failed."})
		return
	}
	var event struct {
		Type      string    `json:"type"`
		CreatedAt time.Time `json:"created_at"`
		Data      struct {
			EmailID string   `json:"email_id"`
			From    string   `json:"from"`
			To      []string `json:"to"`
			Subject string   `json:"subject"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &event) != nil || event.Data.EmailID == "" || event.CreatedAt.IsZero() {
		writeJSON(w, 422, Problem{Code: "INVALID_EVENT", Message: "Event identity and timestamp are required."})
		return
	}
	if event.Type == "email.received" {
		receiver, receiverErr := provider.NewResend(lab.EmailKey, lab.Origin)
		if receiverErr != nil {
			fail(w, receiverErr)
			return
		}
		content, contentErr := receiver.Received(r.Context(), event.Data.EmailID)
		if contentErr != nil {
			writeJSON(w, 503, Problem{Code: "CONTENT_UNAVAILABLE", Message: "Inbound content could not be retrieved."})
			return
		}
		if len(event.Data.To) != 1 {
			writeJSON(w, 422, Problem{Code: "INVALID_EVENT", Message: "One known recipient is required."})
			return
		}
		if content.From != event.Data.From || len(content.To) != 1 || content.To[0] != event.Data.To[0] {
			writeJSON(w, 422, Problem{Code: "CONTENT_MISMATCH", Message: "Received content does not match signed metadata."})
			return
		}
		err = s.Store.persistInbound(r.Context(), "resend:"+event.Data.EmailID, "email", content.From, content.To[0], content.Subject, content.Text)
	} else {
		status := map[string]string{"email.sent": "accepted", "email.delivered": "delivered", "email.bounced": "failed", "email.complained": "failed"}[event.Type]
		if status == "" {
			writeJSON(w, 200, map[string]bool{"ignored": true})
			return
		}
		tx, e := s.Store.Begin(r.Context(), DemoWorkspace, false)
		if e != nil {
			fail(w, e)
			return
		}
		var job string
		e = tx.QueryRow(r.Context(), "SELECT id FROM delivery_jobs WHERE provider_id=$1 AND channel='email'", event.Data.EmailID).Scan(&job)
		tx.Rollback(r.Context())
		if e != nil {
			eventFailure(w, e)
			return
		}
		code := ""
		if event.Type == "email.bounced" || event.Type == "email.complained" {
			code = event.Type
		}
		err = s.Store.applyDeliveryEvent(r.Context(), r.Header.Get("svix-id"), job, event.Data.EmailID, status, code, event.CreatedAt)
	}
	if err != nil {
		eventFailure(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"recorded": true})
}
func eventFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, 404, Problem{Code: "UNKNOWN_EVENT_RECORD", Message: "No matching provider record exists."})
		return
	}
	var p Problem
	if errors.As(err, &p) {
		writeJSON(w, 422, p)
		return
	}
	fail(w, err)
}
func (s *Store) applyDeliveryEvent(ctx context.Context, eventID, jobID, providerID, status, code string, at time.Time) error {
	return s.applyDeliveryEventSource(ctx, eventID, jobID, providerID, status, code, at, "signed_local_provider")
}
func (s *Store) applyDeliveryEventSource(ctx context.Context, eventID, jobID, providerID, status, code string, at time.Time, source string) error {
	if eventID == "" || len(eventID) > 240 || providerID == "" || jobID == "" || at.IsZero() {
		return Problem{Code: "INVALID_EVENT", Message: "Event identifiers are required."}
	}
	target := map[string]string{"queued": "accepted", "sent": "accepted", "accepted": "accepted", "ringing": "accepted", "in-progress": "accepted", "delivered": "delivered", "read": "delivered", "completed": "completed", "failed": "failed", "undelivered": "failed", "busy": "failed", "no-answer": "failed", "canceled": "failed"}[status]
	if target == "" {
		return Problem{Code: "UNSUPPORTED_EVENT", Message: "The provider status is not supported."}
	}
	tx, err := s.Begin(ctx, DemoWorkspace, false)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	j, err := scanJob(tx.QueryRow(ctx, "SELECT "+jobColumns+" FROM delivery_jobs WHERE id=$1 FOR UPDATE", jobID))
	if err != nil {
		return err
	}
	if j.ProviderID != nil && *j.ProviderID != providerID {
		return Problem{Code: "PROVIDER_ID_CONFLICT", Message: "This event references a different provider resource."}
	}
	if j.State == "queued" || j.State == "suppressed" {
		return Problem{Code: "EVENT_STATE_CONFLICT", Message: "The job has not been submitted or has been suppressed."}
	}
	if j.Channel == "call" && target == "delivered" || j.Channel != "call" && target == "completed" {
		return Problem{Code: "EVENT_CHANNEL_CONFLICT", Message: "The status does not match the job channel."}
	}
	command, err := tx.Exec(ctx, "INSERT INTO delivery_events(workspace_id,id,job_id,kind,source,provider_code,occurred_at) VALUES($1,$2,$3,$4,$7,$5,$6) ON CONFLICT DO NOTHING", DemoWorkspace, eventID, jobID, status, code, at, source)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	terminal := j.State == "delivered" || j.State == "completed" || j.State == "failed"
	// Retain late facts but do not regress a terminal state. A later complaint
	// still suppresses the contact even if delivery was already confirmed.
	if !terminal {
		_, err = tx.Exec(ctx, "UPDATE delivery_jobs SET state=$2,provider_id=$3,last_error=nullif($4,''),lease_until=NULL,updated_at=now() WHERE id=$1", jobID, target, providerID, code)
		if err != nil {
			return err
		}
	}
	if j.ContactID != nil && (code == "email.bounced" || code == "email.complained") {
		_, err = tx.Exec(ctx, "UPDATE contacts SET opted_out_at=coalesce(opted_out_at,$2) WHERE id=$1", *j.ContactID, at)
		if err != nil {
			return err
		}
	}
	if j.Channel == "sms" && j.AssetID != nil && !terminal && (target == "delivered" || target == "failed") {
		_, err = assetByID(ctx, tx, *j.AssetID, true)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO observations(workspace_id,asset_id,attempts,filtered,spam_label,observed_at,source)
 SELECT $1,$2,count(*)::int,count(*) FILTER(WHERE last_error='30007')::int,
 coalesce((SELECT spam_label FROM observations WHERE asset_id=$2 ORDER BY observed_at DESC,id DESC LIMIT 1),false),clock_timestamp(),'local_provider_receipts_label_not_measured'
 FROM delivery_jobs WHERE asset_id=$2 AND channel='sms' AND state IN ('delivered','failed') AND created_at>now()-interval '24 hours'`, DemoWorkspace, *j.AssetID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "UPDATE assets SET version=version+1 WHERE id=$1", *j.AssetID)
		if err != nil {
			return err
		}
	}
	if err = audit(ctx, tx, systemUser, "provider."+status, j.AssetID, jobID, "Verified local provider event. "+code); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) persistInbound(ctx context.Context, eventID, channel, from, to, subject, body string) error {
	return s.persistInboundSource(ctx, eventID, channel, from, to, subject, body, "signed_local_provider")
}
func (s *Store) persistInboundSource(ctx context.Context, eventID, channel, from, to, subject, body, source string) error {
	if len(eventID) > 240 || strings.HasSuffix(eventID, ":") || len(body) > 16384 || strings.TrimSpace(body) == "" || len(subject) > 200 {
		return Problem{Code: "INVALID_INBOUND", Message: "Inbound identity and bounded text are required."}
	}
	tx, err := s.Begin(ctx, DemoWorkspace, false)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if generation, ok := ctx.Value(imessageGenerationKey{}).(int64); ok {
		var current int64
		if err = tx.QueryRow(ctx, "SELECT generation FROM demo_state").Scan(&current); err != nil {
			return err
		}
		if current != generation {
			return errors.New("demo reset interrupted bridge synchronization")
		}
	}
	column, kind := "phone", "phone"
	assetAddress := to
	if channel == "imessage" {
		kind = "imessage"
	}
	if channel == "email" {
		column = "email"
		kind = "email"
		parts := strings.SplitN(to, "@", 2)
		if len(parts) != 2 {
			return Problem{Code: "INVALID_INBOUND", Message: "Invalid receiving address."}
		}
		assetAddress = parts[1]
	}
	var contact, asset string
	err = tx.QueryRow(ctx, "SELECT id FROM contacts WHERE "+column+"=$1 FOR UPDATE", from).Scan(&contact)
	if err != nil {
		return err
	}
	err = tx.QueryRow(ctx, "SELECT id FROM assets WHERE address=$1 AND kind=$2", assetAddress, kind).Scan(&asset)
	if err != nil {
		return err
	}
	command, err := tx.Exec(ctx, "INSERT INTO infrastructure_inbox(workspace_id,id,contact_id,asset_id,channel,body,subject,source) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING", DemoWorkspace, eventID, contact, asset, channel, body, subject, source)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	stop := strings.ToUpper(strings.TrimSpace(body))
	if stop == "STOP" || stop == "UNSUBSCRIBE" || stop == "CANCEL" || stop == "END" || stop == "QUIT" {
		_, err = tx.Exec(ctx, "UPDATE contacts SET opted_out_at=coalesce(opted_out_at,clock_timestamp()) WHERE id=$1", contact)
	} else if channel == "sms" {
		_, err = tx.Exec(ctx, "UPDATE contacts SET warm_signal='inbound_reply',warm_at=clock_timestamp() WHERE id=$1", contact)
	}
	if err != nil {
		return err
	}
	if err = audit(ctx, tx, systemUser, "inbox.received", asset, eventID, "Verified local reply. Suppression applied when requested."); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
