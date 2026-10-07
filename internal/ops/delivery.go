package ops

import (
	"communication-infrastructure/internal/provider"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"net/mail"
	"regexp"
	"strings"
	"time"
)

type DeliveryInput struct {
	Channel     string `json:"channel"`
	ContactID   string `json:"contactId"`
	AssetID     string `json:"assetId"`
	RequestKey  string `json:"requestKey"`
	Subject     string `json:"subject"`
	Body        string `json:"body"`
	Destination string `json:"destination"`
	Scenario    string `json:"scenario"`
	Purpose     string `json:"purpose"`
}
type DeliveryJob struct {
	ID            string    `json:"id"`
	DecisionID    *string   `json:"decisionId"`
	Channel       string    `json:"channel"`
	ContactID     *string   `json:"contactId"`
	AssetID       *string   `json:"assetId"`
	State         string    `json:"state"`
	Attempts      int       `json:"attempts"`
	ProviderID    *string   `json:"providerId"`
	LastError     *string   `json:"lastError"`
	Subject       string    `json:"subject"`
	Destination   string    `json:"destination"`
	Purpose       string    `json:"purpose"`
	Scenario      string    `json:"scenario"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
	NextAttemptAt time.Time `json:"nextAttemptAt"`
}

const jobColumns = "id,decision_id,channel,contact_id,asset_id,state,attempts,provider_id,last_error,subject,destination,purpose,scenario,created_at,updated_at,next_attempt_at"

var e164 = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)

func scanJob(row pgx.Row) (DeliveryJob, error) {
	var j DeliveryJob
	err := row.Scan(&j.ID, &j.DecisionID, &j.Channel, &j.ContactID, &j.AssetID, &j.State, &j.Attempts, &j.ProviderID, &j.LastError, &j.Subject, &j.Destination, &j.Purpose, &j.Scenario, &j.CreatedAt, &j.UpdatedAt, &j.NextAttemptAt)
	return j, err
}
func publicJob(j DeliveryJob) DeliveryJob {
	if j.Channel == "imessage" {
		j.Destination = "Existing iMessage conversation"
	} else if j.Channel == "email" {
		j.Destination = MaskEmail(j.Destination)
	} else if j.Channel != "trunk" {
		j.Destination = MaskPhone(j.Destination)
	}
	return j
}

func (s *Store) EnsureInfrastructure(ctx context.Context) error {
	tx, err := s.Begin(ctx, DemoWorkspace, false)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO channel_permissions(workspace_id,contact_id,channel,granted_at,evidence) VALUES($1,'c102','email',now(),'Explicit synthetic lab opt-in') ON CONFLICT DO NOTHING`, DemoWorkspace)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO email_ramps(workspace_id,asset_id) VALUES($1,'email-north') ON CONFLICT DO NOTHING`, DemoWorkspace)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) QueueDelivery(ctx context.Context, u User, in DeliveryInput) (Result, error) {
	return s.queueDeliveryInTx(ctx, u, in, nil)
}

// Campaign execution and its job share the caller's transaction.
func (s *Store) queueDeliveryInTx(ctx context.Context, u User, in DeliveryInput, provided pgx.Tx) (Result, error) {
	if u.Role != "operator" {
		return Result{403, Problem{Code: "FORBIDDEN", Message: "An operator role is required."}}, nil
	}
	if s.Gateway == nil {
		return Result{503, Problem{Code: "INFRASTRUCTURE_DISABLED", Message: "Start the API with INFRA_LAB=true to use the isolated provider lab."}}, nil
	}
	in.Subject = strings.TrimSpace(in.Subject)
	in.Body = strings.TrimSpace(in.Body)
	if in.Scenario == "" {
		in.Scenario = "success"
	}
	if in.Purpose == "" {
		in.Purpose = "outreach"
	}
	validChannel := in.Channel == "sms" || in.Channel == "call" || in.Channel == "email" || in.Channel == "provision" || in.Channel == "trunk" || in.Channel == "imessage"
	if in.Channel == "imessage" && s.IMessageBridge == nil {
		return Result{503, Problem{Code: "IMESSAGE_DISABLED", Message: "The optional iMessage bridge is disabled. Start the local demo with IMESSAGE_LAB=true."}}, nil
	}
	if in.Channel == "imessage" && in.Scenario != "success" {
		return Result{422, Problem{Code: "INVALID_SCENARIO", Message: "This bridge lab supports the confirmed outcome scenario."}}, nil
	}
	validScenario := in.Scenario == "success" || in.Scenario == "throttle" || in.Scenario == "ambiguous" || in.Scenario == "filtered" || in.Scenario == "bounce"
	if !validChannel || !validScenario || len(in.RequestKey) < 8 || len(in.RequestKey) > 100 || len(in.Subject) > 200 || strings.ContainsAny(in.Subject, "\r\n") || len(in.ContactID) > 80 || len(in.AssetID) > 80 || len(in.Destination) > 80 || (in.Purpose != "outreach" && in.Purpose != "ramp" && in.Purpose != "management") {
		return Result{422, Problem{Code: "INVALID_INPUT", Message: "Choose a supported channel, scenario and an 8 to 100 character request key."}}, nil
	}
	management := in.Channel == "provision" || in.Channel == "trunk"
	if management {
		in.Purpose = "management"
		if in.Subject == "" || (in.Channel == "provision" && (!e164.MatchString(in.Destination) || !strings.HasPrefix(in.Destination, "+120255501"))) {
			return Result{422, Problem{Code: "INVALID_RESOURCE", Message: "Name the resource. Lab provisioning accepts only synthetic +120255501xx numbers."}}, nil
		}
	} else {
		if in.ContactID == "" || in.AssetID == "" {
			return Result{422, Problem{Code: "INVALID_INPUT", Message: "An existing contact and asset are required."}}, nil
		}
		if in.Channel != "call" {
			if err := ValidateMessage(in.Body); err != nil {
				return Result{422, Problem{Code: "INVALID_MESSAGE", Message: err.Error()}}, nil
			}
		}
		if in.Channel == "email" && in.Subject == "" {
			return Result{422, Problem{Code: "SUBJECT_REQUIRED", Message: "Enter an email subject."}}, nil
		}
	}
	tx := provided
	var err error
	commit := func() error { return nil }
	if tx == nil {
		tx, err = s.Begin(ctx, u.WorkspaceID, false)
		if err != nil {
			return Result{}, err
		}
		defer tx.Rollback(ctx)
		commit = func() error { return tx.Commit(ctx) }
	}
	if err = campaignLock(ctx, tx, u.WorkspaceID); err != nil {
		return Result{}, err
	}
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", u.WorkspaceID+":"+u.ID+":"+in.RequestKey); err != nil {
		return Result{}, err
	}
	raw, _ := json.Marshal(in)
	hash := Hash(string(raw))
	var oldHash string
	err = tx.QueryRow(ctx, "SELECT request_hash FROM delivery_jobs WHERE actor_id=$1 AND request_key=$2", u.ID, in.RequestKey).Scan(&oldHash)
	if err == nil {
		if oldHash != hash {
			return Result{409, Problem{Code: "REQUEST_KEY_CONFLICT", Message: "This request key belongs to different input."}}, nil
		}
		j, e := scanJob(tx.QueryRow(ctx, "SELECT "+jobColumns+" FROM delivery_jobs WHERE actor_id=$1 AND request_key=$2", u.ID, in.RequestKey))
		return Result{200, map[string]any{"job": publicJob(j), "replayed": true, "simulated": true}}, e
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Result{}, err
	}
	if in.Channel == "provision" {
		if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,2))", u.WorkspaceID+":"+in.Destination); err != nil {
			return Result{}, err
		}
		var reserved bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM assets WHERE address=$1) OR EXISTS(SELECT 1 FROM delivery_jobs WHERE channel='provision' AND destination=$1 AND state NOT IN ('failed','suppressed'))`, in.Destination).Scan(&reserved); err != nil {
			return Result{}, err
		}
		if reserved {
			return Result{409, Problem{Code: "NUMBER_RESERVED", Message: "This number is already provisioned or has an unresolved provisioning request."}}, nil
		}
	}
	// Both the old demonstration dialer and provider-backed dialer use this lock.
	if in.Channel == "call" {
		if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,1))", u.WorkspaceID); err != nil {
			return Result{}, err
		}
	}
	var c Contact
	var a Asset
	var contact, asset any
	var decisionID any
	if !management {
		c, err = contactByID(ctx, tx, in.ContactID, true)
		if err != nil {
			return deliveryMissing(err)
		}
		a, err = assetByID(ctx, tx, in.AssetID, true)
		if err != nil {
			return deliveryMissing(err)
		}
		if reason, e := deliveryGate(ctx, tx, c, a, in.Channel); e != nil {
			return Result{}, e
		} else if reason != "ELIGIBLE" {
			return deliveryRejection(ctx, tx, u, c, a, in.Channel, reason, 422, commit)
		}
		var touches int
		if err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM delivery_jobs WHERE contact_id=$1 AND created_at>now()-interval '24 hours')+(SELECT count(*) FROM calls WHERE contact_id=$1 AND started_at>now()-interval '24 hours')+(SELECT count(*) FROM messages WHERE contact_id=$1 AND delivered_at>now()-interval '24 hours')`, c.ID).Scan(&touches); err != nil {
			return Result{}, err
		}
		if touches >= 3 {
			return deliveryRejection(ctx, tx, u, c, a, in.Channel, "TOUCH_LIMIT", 422, commit)
		}
		if in.Channel == "call" {
			var busy bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM calls WHERE status='active') OR EXISTS(SELECT 1 FROM delivery_jobs WHERE channel='call' AND state IN ('queued','submitting','accepted','unknown'))`).Scan(&busy); err != nil {
				return Result{}, err
			}
			if busy {
				return deliveryRejection(ctx, tx, u, c, a, in.Channel, "CALL_BUSY", 409, commit)
			}
		}
		if reason, e := sendingCapacity(ctx, tx, a.ID); e != nil {
			return Result{}, e
		} else if reason != "" {
			return deliveryRejection(ctx, tx, u, c, a, in.Channel, reason, 422, commit)
		}
		if in.Purpose == "ramp" {
			if in.Channel != "email" {
				return Result{422, Problem{Code: "EMAIL_REQUIRED", Message: "Volume ramp applies only to email."}}, nil
			}
			cap, e := rampCapacity(ctx, tx, a.ID)
			if e != nil {
				return Result{}, e
			}
			if cap < 1 {
				return Result{422, Problem{Code: "RAMP_LIMIT", Message: "Enable the email ramp and stay within its current daily limit."}}, nil
			}
		}
		contact = c.ID
		asset = a.ID
		decisionID, err = recordDecision(ctx, tx, u, c, a, in.Channel, "ELIGIBLE")
		if err != nil {
			return Result{}, err
		}
		in.Destination = c.Phone
		if in.Channel == "imessage" {
			in.Destination = provider.ChatGUID(c.Phone)
		}
		if in.Channel == "email" {
			in.Destination = c.Email
		}
	}
	id := ID("delivery")
	j, err := scanJob(tx.QueryRow(ctx, "INSERT INTO delivery_jobs(workspace_id,id,actor_id,actor_name,request_key,request_hash,channel,contact_id,asset_id,destination,subject,body,purpose,scenario,decision_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) RETURNING "+jobColumns, u.WorkspaceID, id, u.ID, u.Name, in.RequestKey, hash, in.Channel, contact, asset, in.Destination, in.Subject, in.Body, in.Purpose, in.Scenario, decisionID))
	if err != nil {
		return Result{}, err
	}
	if err = audit(ctx, tx, u, "delivery.queued", asset, id, "Local provider lab. No real outreach."); err != nil {
		return Result{}, err
	}
	return Result{202, map[string]any{"job": publicJob(j), "replayed": false, "simulated": true}}, commit()
}
func deliveryMissing(err error) (Result, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{404, Problem{Code: "NOT_FOUND", Message: "The workspace contact or asset was not found."}}, nil
	}
	return Result{}, err
}
func deliveryRejection(ctx context.Context, tx pgx.Tx, u User, c Contact, a Asset, channel, reason string, status int, commit func() error) (Result, error) {
	id, err := recordDecision(ctx, tx, u, c, a, channel, reason)
	if err != nil {
		return Result{}, err
	}
	if err = audit(ctx, tx, u, "delivery.blocked", a.ID, id, reason); err != nil {
		return Result{}, err
	}
	return Result{status, Problem{Code: reason, Message: deliveryReason(reason), DecisionID: id}}, commit()
}
func deliveryReason(code string) string {
	if text, ok := reasonText[code]; ok {
		return text
	}
	switch code {
	case "NO_EMAIL_PERMISSION":
		return "Explicit email permission is required in the local lab."
	case "EMAIL_ASSET_REQUIRED":
		return "Choose an active email asset."
	}
	return "The recorded eligibility checks did not pass."
}
func deliveryGate(ctx context.Context, tx pgx.Tx, c Contact, a Asset, channel string) (string, error) {
	if channel == "imessage" {
		if c.DNC {
			return "DNC_SUPPRESSED", nil
		}
		if c.OptedOutAt != nil {
			return "OPTED_OUT", nil
		}
		if a.Kind != "imessage" || a.Status != "active" {
			return "IMESSAGE_ASSET_REQUIRED", nil
		}
		var allowed bool
		err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM channel_permissions WHERE contact_id=$1 AND channel='imessage' AND evidence<>'')", c.ID).Scan(&allowed)
		if !allowed {
			return "NO_IMESSAGE_PERMISSION", err
		}
		return "ELIGIBLE", err
	}
	if channel != "email" {
		return Gate(c, a, channel), nil
	}
	if c.DNC {
		return "DNC_SUPPRESSED", nil
	}
	if c.OptedOutAt != nil {
		return "OPTED_OUT", nil
	}
	if a.Kind != "email" || a.Status != "active" {
		return "EMAIL_ASSET_REQUIRED", nil
	}
	address, err := mail.ParseAddress(c.Email)
	if err != nil || address.Address != c.Email {
		return "INVALID_EMAIL", nil
	}
	var consent bool
	err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM channel_permissions WHERE contact_id=$1 AND channel='email' AND evidence<>'')", c.ID).Scan(&consent)
	if !consent {
		return "NO_EMAIL_PERMISSION", err
	}
	return "ELIGIBLE", err
}

// Submission happens outside a transaction. A durable lease records the crash
// window. An expired submitting job becomes unknown, never queued again.
func (s *Store) deliveryStep(ctx context.Context) error {
	if s.Gateway == nil {
		return nil
	}
	tx, err := s.Begin(ctx, DemoWorkspace, false)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = campaignLock(ctx, tx, DemoWorkspace); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "UPDATE delivery_jobs SET state='unknown',last_error='SUBMISSION_INTERRUPTED',updated_at=now() WHERE state='submitting' AND lease_until<now()")
	if err != nil {
		return err
	}
	j, err := scanJob(tx.QueryRow(ctx, "SELECT "+jobColumns+" FROM delivery_jobs WHERE state='queued' AND next_attempt_at<=now() AND ($1 OR channel<>'imessage') ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED", s.IMessageBridge != nil))
	if errors.Is(err, pgx.ErrNoRows) {
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	if proceed, e := s.campaignDispatchGate(ctx, tx, j); e != nil {
		return e
	} else if !proceed {
		return tx.Commit(ctx)
	}
	var body string
	err = tx.QueryRow(ctx, "SELECT body FROM delivery_jobs WHERE id=$1", j.ID).Scan(&body)
	if err != nil {
		return err
	}
	from := ""
	if j.ContactID != nil && j.AssetID != nil {
		c, e := contactByID(ctx, tx, *j.ContactID, true)
		if e != nil {
			return e
		}
		a, e := assetByID(ctx, tx, *j.AssetID, true)
		if e != nil {
			return e
		}
		if reason, e := deliveryGate(ctx, tx, c, a, j.Channel); e != nil {
			return e
		} else if reason != "ELIGIBLE" {
			_, e = tx.Exec(ctx, "UPDATE delivery_jobs SET state='suppressed',last_error=$2,updated_at=now() WHERE id=$1", j.ID, reason)
			if e != nil {
				return e
			}
			if e = audit(ctx, tx, systemUser, "delivery.suppressed", a.ID, j.ID, reason); e != nil {
				return e
			}
			return tx.Commit(ctx)
		}
		from = a.Address
		if j.Channel == "email" {
			from = "operator@" + a.Address
		}
	}
	if j.AssetID != nil {
		var ready bool
		err = tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM sending_limits WHERE asset_id=$1 AND last_submitted_at+make_interval(secs=>pacing_seconds)>now())`, *j.AssetID).Scan(&ready)
		if err != nil {
			return err
		}
		if !ready {
			_, err = tx.Exec(ctx, `UPDATE delivery_jobs SET next_attempt_at=(SELECT last_submitted_at+make_interval(secs=>pacing_seconds) FROM sending_limits WHERE asset_id=$2) WHERE id=$1`, j.ID, *j.AssetID)
			if err != nil {
				return err
			}
			return tx.Commit(ctx)
		}
		if _, err = tx.Exec(ctx, "UPDATE sending_limits SET last_submitted_at=clock_timestamp() WHERE asset_id=$1", *j.AssetID); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, "UPDATE delivery_jobs SET state='submitting',attempts=attempts+1,lease_until=now()+interval '15 seconds',updated_at=now() WHERE id=$1", j.ID); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	callback := s.CallbackOrigin + "/hooks/twilio/status?job=" + j.ID
	receipt, submitErr := s.Gateway.Submit(ctx, provider.Request{ID: j.ID, Channel: j.Channel, From: from, To: j.Destination, Body: body, Subject: j.Subject, Callback: callback, Scenario: j.Scenario})
	// Persist with a fresh context even if the provider exhausted its deadline.
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	tx, err = s.Begin(finish, DemoWorkspace, false)
	if err != nil {
		return err
	}
	defer tx.Rollback(finish)
	state := "accepted"
	var code any
	if submitErr != nil {
		state = "failed"
		code = "PROVIDER_REJECTED"
		var failure *provider.Failure
		if errors.As(submitErr, &failure) {
			code = failure.Code
			if failure.Ambiguous {
				state = "unknown"
			} else if failure.Retryable && j.Attempts+1 < 3 {
				state = "queued"
			}
		}
	}
	var providerID any
	if receipt.ID != "" {
		providerID = receipt.ID
	}
	delaySeconds := 1 << uint(j.Attempts+1)
	_, err = tx.Exec(finish, `UPDATE delivery_jobs SET state=$2,provider_id=coalesce(provider_id,$3),last_error=$4,next_attempt_at=now()+make_interval(secs=>$5),lease_until=NULL,updated_at=now() WHERE id=$1 AND state='submitting'`, j.ID, state, providerID, code, float64(delaySeconds))
	if err != nil {
		return err
	}
	if submitErr == nil && (j.Channel == "provision" || j.Channel == "trunk") {
		_, err = tx.Exec(finish, "UPDATE delivery_jobs SET state='completed' WHERE id=$1 AND state='accepted'", j.ID)
		if err != nil {
			return err
		}
		if j.Channel == "provision" {
			_, err = tx.Exec(finish, "INSERT INTO assets(workspace_id,id,kind,name,address,status) VALUES($1,$2,'phone',$3,$4,'active') ON CONFLICT DO NOTHING", DemoWorkspace, receipt.ID, j.Subject, j.Destination)
			if err != nil {
				return err
			}
		}
	}
	if err = audit(finish, tx, systemUser, "delivery."+state, j.AssetID, j.ID, fmt.Sprintf("Lab provider submission attempt %d", j.Attempts+1)); err != nil {
		return err
	}
	if err = tx.Commit(finish); err != nil {
		return err
	}
	if submitErr == nil && j.Channel == "imessage" && j.ContactID != nil {
		return s.syncIMessage(finish, DemoWorkspace, *j.ContactID)
	}
	if submitErr == nil && s.ProviderLab != nil {
		return s.ProviderLab.Emit(finish, j.ID)
	}
	return nil
}
func rampCapacity(ctx context.Context, tx pgx.Tx, asset string) (int, error) {
	var capacity int
	err := tx.QueryRow(ctx, `SELECT CASE WHEN enabled THEN least(maximum,daily_start+daily_step*greatest(0,floor(extract(epoch FROM (now()-started_at))/86400)::int))-(SELECT count(*)::int FROM delivery_jobs WHERE asset_id=$1 AND channel='email' AND created_at>=date_trunc('day',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC') ELSE 0 END FROM email_ramps WHERE asset_id=$1 FOR UPDATE`, asset).Scan(&capacity)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return capacity, err
}
