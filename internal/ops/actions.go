package ops

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"strings"
	"time"
)

type AssetAction struct {
	Reason          string `json:"reason"`
	ExpectedVersion int64  `json:"expectedVersion"`
}

func (s *Store) AssetAction(ctx context.Context, u User, id, action string, in AssetAction) (Result, error) {
	if u.Role != "operator" {
		return Result{403, Problem{Code: "FORBIDDEN", Message: "An operator role is required."}}, nil
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if len([]rune(in.Reason)) < 3 || len([]rune(in.Reason)) > 300 {
		return Result{422, Problem{Code: "INVALID_REASON", Message: "Enter an explanation between 3 and 300 characters."}}, nil
	}
	tx, err := s.Begin(ctx, u.WorkspaceID, false)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback(ctx)
	a, err := assetByID(ctx, tx, id, true)
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{404, Problem{Code: "NOT_FOUND", Message: "The asset was not found."}}, nil
	}
	if err != nil {
		return Result{}, err
	}
	if a.Kind != "phone" {
		return Result{422, Problem{Code: "UNSUPPORTED_ASSET_ACTION", Message: "State transitions are implemented for phone lines. Email configuration is read-only in this demo."}}, nil
	}
	target := "quarantined"
	if action == "restore" {
		target = "active"
	}
	// Matching desired state is idempotent, even if the original response was lost.
	if a.Status == target && action != "recovery" {
		return Result{200, map[string]any{"asset": a, "changed": false}}, tx.Commit(ctx)
	}
	if a.Version != in.ExpectedVersion {
		return Result{409, Problem{Code: "VERSION_CONFLICT", Message: "This asset changed. Refresh its details before trying again."}}, nil
	}
	var busy bool
	err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM calls WHERE asset_id=$1 AND status='active') OR EXISTS(SELECT 1 FROM delivery_jobs WHERE asset_id=$1 AND channel='call' AND state IN ('queued','submitting','accepted','unknown'))", id).Scan(&busy)
	if err != nil {
		return Result{}, err
	}
	if busy {
		return Result{409, Problem{Code: "LINE_BUSY", Message: "An active call is using this line. Wait for its recorded outcome."}}, nil
	}
	switch action {
	case "quarantine":
		_, err = tx.Exec(ctx, "UPDATE assets SET status='quarantined',quarantined_at=clock_timestamp(),quarantine_reason=$2,version=version+1 WHERE id=$1", id, in.Reason)
	case "restore":
		if reasons := RestoreProblems(a, a.Sample); len(reasons) > 0 {
			if err = audit(ctx, tx, u, "asset.restore_denied", id, nil, strings.Join(reasons, " ")); err != nil {
				return Result{}, err
			}
			return Result{409, Problem{Code: "RESTORE_NOT_READY", Message: "The latest observation does not meet restoration rules.", Details: reasons}}, tx.Commit(ctx)
		}
		_, err = tx.Exec(ctx, "UPDATE assets SET status='active',quarantined_at=NULL,quarantine_reason=NULL,version=version+1 WHERE id=$1", id)
	case "recovery":
		if a.Status != "quarantined" || a.Kind != "phone" {
			return Result{409, Problem{Code: "RECOVERY_NOT_AVAILABLE", Message: "The clean demo observation is available for quarantined phone lines."}}, nil
		}
		_, err = tx.Exec(ctx, "INSERT INTO observations(workspace_id,asset_id,attempts,filtered,spam_label,observed_at) VALUES($1,$2,100,2,false,clock_timestamp())", u.WorkspaceID, id)
		if err == nil {
			_, err = tx.Exec(ctx, "UPDATE assets SET version=version+1 WHERE id=$1", id)
		}
	default:
		return Result{404, Problem{Code: "NOT_FOUND", Message: "Unknown asset action."}}, nil
	}
	if err != nil {
		return Result{}, err
	}
	if err = audit(ctx, tx, u, "asset."+action, id, nil, in.Reason); err != nil {
		return Result{}, err
	}
	a, err = assetByID(ctx, tx, id, false)
	if err != nil {
		return Result{}, err
	}
	return Result{200, map[string]any{"asset": a, "changed": true}}, tx.Commit(ctx)
}

type OutreachInput struct {
	ContactID  string `json:"contactId"`
	LineID     string `json:"lineId"`
	Message    string `json:"message"`
	RequestKey string `json:"requestKey"`
}

func decisionEvidence(c Contact, a Asset, now time.Time) map[string]any {
	return map[string]any{
		"contactLabel": "Contact · " + c.ID, "maskedPhone": MaskPhone(c.Phone), "maskedEmail": MaskEmail(c.Email),
		"dnc": c.DNC, "optedOutAt": c.OptedOutAt, "smsConsentAt": c.SMSConsentAt, "consentSource": c.ConsentSource,
		"warmSignal": c.WarmSignal, "warmAt": c.WarmAt, "emailOpenedAt": c.EmailOpenedAt, "assetStatus": a.Status,
		"assetVersion": a.Version, "sample": a.Sample, "checkedAt": now, "source": "stored_demo_records",
		"unavailable": []string{"External DNC registry verification", "Live provider reputation"},
	}
}
func recordDecision(ctx context.Context, tx pgx.Tx, u User, c Contact, a Asset, channel, reason string) (string, error) {
	outcome := "blocked"
	if reason == "ELIGIBLE" {
		outcome = "allowed"
	}
	if reason == "CALL_BUSY" {
		outcome = "deferred"
	}
	id := ID("dec")
	evidence := decisionEvidence(c, a, time.Now().UTC())
	if channel == "email" || channel == "imessage" {
		var granted time.Time
		var source string
		e := tx.QueryRow(ctx, "SELECT granted_at,evidence FROM channel_permissions WHERE contact_id=$1 AND channel=$2", c.ID, channel).Scan(&granted, &source)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return "", e
		}
		evidence[channel+"PermissionRecorded"] = e == nil
		if e == nil {
			evidence[channel+"PermissionAt"] = granted
			evidence[channel+"PermissionSource"] = source
		}
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, "INSERT INTO decisions(workspace_id,id,contact_id,asset_id,channel,outcome,reason,evidence) VALUES($1,$2,$3,$4,$5,$6,$7,$8)", u.WorkspaceID, id, c.ID, a.ID, channel, outcome, reason, raw)
	return id, err
}
func (s *Store) Outreach(ctx context.Context, u User, channel string, in OutreachInput) (Result, error) {
	if u.Role != "operator" {
		return Result{403, Problem{Code: "FORBIDDEN", Message: "An operator role is required."}}, nil
	}
	if len(in.RequestKey) < 8 || len(in.RequestKey) > 100 || in.ContactID == "" || in.LineID == "" || len(in.ContactID) > 80 || len(in.LineID) > 80 {
		return Result{422, Problem{Code: "INVALID_INPUT", Message: "A contact, line and unique request key are required."}}, nil
	}
	if channel == "sms" {
		if err := ValidateMessage(in.Message); err != nil {
			return Result{422, Problem{Code: "INVALID_MESSAGE", Message: err.Error()}}, nil
		}
	}
	tx, err := s.Begin(ctx, u.WorkspaceID, false)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback(ctx)
	// This key lock serializes retries before either request can send or create a call.
	_, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", u.WorkspaceID+":"+u.ID+":"+in.RequestKey)
	if err != nil {
		return Result{}, err
	}
	inputJSON, _ := json.Marshal(map[string]any{"channel": channel, "input": in})
	requestHash := Hash(string(inputJSON))
	var previousHash string
	var status int
	var body []byte
	err = tx.QueryRow(ctx, "SELECT request_hash,http_status,result FROM operations WHERE actor_id=$1 AND request_key=$2", u.ID, in.RequestKey).Scan(&previousHash, &status, &body)
	if err == nil {
		if previousHash != requestHash {
			return Result{409, Problem{Code: "REQUEST_KEY_CONFLICT", Message: "This request key was used for different input."}}, nil
		}
		var parsed any
		if err = json.Unmarshal(body, &parsed); err != nil {
			return Result{}, err
		}
		return Result{status, parsed}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Result{}, err
	}
	// Every call locks the workspace before its contact and line to avoid two active calls.
	if channel == "call" {
		_, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,1))", u.WorkspaceID)
		if err != nil {
			return Result{}, err
		}
	}
	c, err := contactByID(ctx, tx, in.ContactID, true)
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{404, Problem{Code: "NOT_FOUND", Message: "The contact was not found."}}, nil
	}
	if err != nil {
		return Result{}, err
	}
	a, err := assetByID(ctx, tx, in.LineID, true)
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{404, Problem{Code: "NOT_FOUND", Message: "The line was not found."}}, nil
	}
	if err != nil {
		return Result{}, err
	}
	reason := Gate(c, a, channel)
	if reason == "ELIGIBLE" {
		var touches int
		err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM calls WHERE contact_id=$1 AND started_at>now()-interval '24 hours')+(SELECT count(*) FROM messages WHERE contact_id=$1 AND delivered_at>now()-interval '24 hours')+(SELECT count(*) FROM delivery_jobs WHERE contact_id=$1 AND created_at>now()-interval '24 hours')`, c.ID).Scan(&touches)
		if err != nil {
			return Result{}, err
		}
		if touches >= 3 {
			reason = "TOUCH_LIMIT"
		}
	}
	if reason == "ELIGIBLE" && channel == "call" {
		var active bool
		err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM calls WHERE status='active') OR EXISTS(SELECT 1 FROM delivery_jobs WHERE channel='call' AND state IN ('queued','submitting','accepted','unknown'))").Scan(&active)
		if err != nil {
			return Result{}, err
		}
		if active {
			reason = "CALL_BUSY"
		}
	}
	decisionID, err := recordDecision(ctx, tx, u, c, a, channel, reason)
	if err != nil {
		return Result{}, err
	}
	result := Result{422, Problem{Code: reason, Message: reasonText[reason], DecisionID: decisionID}}
	if reason == "CALL_BUSY" {
		result.Status = 409
	}
	if reason == "ELIGIBLE" {
		id := ID(channel)
		if channel == "call" {
			_, err = tx.Exec(ctx, "INSERT INTO calls(workspace_id,id,contact_id,asset_id,decision_id,status,finish_at,actor_id,actor_name) VALUES($1,$2,$3,$4,$5,'active',now()+interval '6 seconds',$6,$7)", u.WorkspaceID, id, c.ID, a.ID, decisionID, u.ID, u.Name)
		} else {
			_, err = tx.Exec(ctx, "INSERT INTO messages(workspace_id,id,contact_id,asset_id,decision_id,body) VALUES($1,$2,$3,$4,$5,$6)", u.WorkspaceID, id, c.ID, a.ID, decisionID, strings.TrimSpace(in.Message))
		}
		if err != nil {
			return Result{}, err
		}
		result = Result{201, map[string]any{"id": id, "decisionId": decisionID, "status": map[bool]string{true: "active", false: "delivered"}[channel == "call"], "simulated": true}}
	}
	if err = audit(ctx, tx, u, channel+"."+strings.ToLower(reason), a.ID, decisionID, nil); err != nil {
		return Result{}, err
	}
	raw, err := json.Marshal(result.Body)
	if err != nil {
		return Result{}, err
	}
	_, err = tx.Exec(ctx, "INSERT INTO operations(workspace_id,actor_id,request_key,request_hash,http_status,result) VALUES($1,$2,$3,$4,$5,$6)", u.WorkspaceID, u.ID, in.RequestKey, requestHash, result.Status, raw)
	if err != nil {
		return Result{}, err
	}
	return result, tx.Commit(ctx)
}
