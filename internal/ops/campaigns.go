package ops

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"strings"
	"time"
)

type CampaignStep struct {
	Channel      string `json:"channel"`
	Subject      string `json:"subject"`
	Body         string `json:"body"`
	DelaySeconds int    `json:"delaySeconds"`
}
type CampaignInput struct {
	Name            string         `json:"name"`
	Steps           []CampaignStep `json:"steps"`
	AssetIDs        []string       `json:"assetIds"`
	Timezone        string         `json:"timezone"`
	StartHour       int            `json:"startHour"`
	EndHour         int            `json:"endHour"`
	WeekdaysOnly    bool           `json:"weekdaysOnly"`
	ExpectedVersion int64          `json:"expectedVersion"`
}
type Campaign struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	State        string         `json:"state"`
	Version      int64          `json:"version"`
	Steps        []CampaignStep `json:"steps"`
	AssetIDs     []string       `json:"assetIds"`
	Timezone     string         `json:"timezone"`
	StartHour    int            `json:"startHour"`
	EndHour      int            `json:"endHour"`
	WeekdaysOnly bool           `json:"weekdaysOnly"`
	CreatedAt    time.Time      `json:"createdAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
}
type Enrollment struct {
	ID         string    `json:"id"`
	ContactID  string    `json:"contactId"`
	Timezone   string    `json:"timezone"`
	Step       int       `json:"step"`
	State      string    `json:"state"`
	Reason     string    `json:"reason"`
	NextRunAt  time.Time `json:"nextRunAt"`
	EnrolledAt time.Time `json:"enrolledAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

const campaignColumns = "id,name,state,version,steps,asset_ids,timezone,start_hour,end_hour,weekdays_only,created_at,updated_at"

func scanCampaign(row pgx.Row) (Campaign, error) {
	var c Campaign
	var raw []byte
	err := row.Scan(&c.ID, &c.Name, &c.State, &c.Version, &raw, &c.AssetIDs, &c.Timezone, &c.StartHour, &c.EndHour, &c.WeekdaysOnly, &c.CreatedAt, &c.UpdatedAt)
	if err == nil {
		err = json.Unmarshal(raw, &c.Steps)
	}
	return c, err
}
func campaignLock(ctx context.Context, tx pgx.Tx, workspace string) error {
	_, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,6))", workspace)
	return err
}
func validateCampaign(in CampaignInput) error {
	if len(strings.TrimSpace(in.Name)) < 3 || len(in.Name) > 100 || len(in.Steps) < 1 || len(in.Steps) > 6 || len(in.AssetIDs) < 1 || len(in.AssetIDs) > 8 {
		return errors.New("Use a 3 to 100 character name, 1 to 6 steps and 1 to 8 sending assets.")
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil || in.StartHour < 0 || in.EndHour > 24 || in.EndHour <= in.StartHour {
		return errors.New("Choose an IANA time zone and a valid sending window.")
	}
	seen := map[string]bool{}
	for _, id := range in.AssetIDs {
		if len(id) > 80 || seen[id] {
			return errors.New("Sending assets must be distinct workspace records.")
		}
		seen[id] = true
	}
	for _, step := range in.Steps {
		if (step.Channel != "email" && step.Channel != "sms") || step.DelaySeconds < 0 || step.DelaySeconds > 30*86400 {
			return errors.New("Steps support email or consented SMS with delays up to 30 days.")
		}
		if err := ValidateMessage(step.Body); err != nil {
			return err
		}
		if step.Channel == "email" && (strings.TrimSpace(step.Subject) == "" || len(step.Subject) > 200 || strings.ContainsAny(step.Subject, "\r\n")) {
			return errors.New("Each email needs a single-line subject of at most 200 characters.")
		}
		for _, text := range []string{step.Body, step.Subject} {
			text = strings.ReplaceAll(strings.ReplaceAll(text, "{name}", ""), "{contact_id}", "")
			if strings.ContainsAny(text, "{}") {
				return errors.New("Supported template fields are {name} and {contact_id}.")
			}
		}
	}
	return nil
}

// Local calendar arithmetic preserves recipient windows across DST transitions.
func campaignWindow(at time.Time, zone string, start, end int, weekdays bool) time.Time {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return at.Add(24 * time.Hour)
	}
	t := at.In(loc)
	for i := 0; i < 9; i++ {
		y, m, d := t.Date()
		weekend := t.Weekday() == time.Saturday || t.Weekday() == time.Sunday
		if !weekdays || !weekend {
			if t.Hour() >= start && t.Hour() < end {
				return t.UTC()
			}
			if t.Hour() < start {
				open := time.Date(y, m, d, start, 0, 0, 0, loc)
				// Go may normalize a nonexistent spring-forward hour backwards.
				for n := 0; n < 180 && open.Hour() < start; n++ {
					open = open.Add(time.Minute)
				}
				if open.Hour() < end && !open.Before(t) {
					return open.UTC()
				}
			}
		}
		t = time.Date(y, m, d+1, 0, 0, 0, 0, loc)
	}

	return t.UTC()
}
func sendingCapacity(ctx context.Context, tx pgx.Tx, asset string) (string, error) {
	var cap, used int
	err := tx.QueryRow(ctx, `SELECT daily_cap,(SELECT count(*) FROM delivery_jobs WHERE asset_id=$1 AND created_at>=date_trunc('day',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' AND state<>'suppressed') FROM sending_limits WHERE asset_id=$1`, asset).Scan(&cap, &used)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if used >= cap {
		return "ASSET_DAILY_CAP", nil
	}
	return "", nil
}
func (s *Store) SaveCampaign(ctx context.Context, u User, id string, in CampaignInput) (Result, error) {
	if u.Role != "operator" {
		return Result{403, Problem{Code: "FORBIDDEN", Message: "An operator is required."}}, nil
	}
	if err := validateCampaign(in); err != nil {
		return Result{422, Problem{Code: "INVALID_CAMPAIGN", Message: err.Error()}}, nil
	}
	tx, err := s.Begin(ctx, u.WorkspaceID, false)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback(ctx)
	if err = campaignLock(ctx, tx, u.WorkspaceID); err != nil {
		return Result{}, err
	}
	for _, asset := range in.AssetIDs {
		a, e := assetByID(ctx, tx, asset, true)
		if e != nil {
			return deliveryMissing(e)
		}
		if a.Kind != "email" && a.Kind != "phone" {
			return Result{422, Problem{Code: "INVALID_ASSET", Message: "Choose phone or email assets."}}, nil
		}
	}
	for _, step := range in.Steps {
		supported := false
		for _, aid := range in.AssetIDs {
			a, e := assetByID(ctx, tx, aid, false)
			if e != nil {
				return Result{}, e
			}
			supported = supported || (step.Channel == "email" && a.Kind == "email") || (step.Channel == "sms" && a.Kind == "phone")
		}
		if !supported {
			return Result{422, Problem{Code: "MISSING_CHANNEL_ASSET", Message: "Each channel needs an appropriate sending asset."}}, nil
		}
	}
	raw, _ := json.Marshal(in.Steps)
	status := 201
	if id == "" {
		id = ID("campaign")
		_, err = tx.Exec(ctx, `INSERT INTO campaigns(workspace_id,id,name,steps,asset_ids,timezone,start_hour,end_hour,weekdays_only) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, u.WorkspaceID, id, strings.TrimSpace(in.Name), raw, in.AssetIDs, in.Timezone, in.StartHour, in.EndHour, in.WeekdaysOnly)
	} else {
		c, e := scanCampaign(tx.QueryRow(ctx, "SELECT "+campaignColumns+" FROM campaigns WHERE id=$1 FOR UPDATE", id))
		if e != nil {
			return deliveryMissing(e)
		}
		if c.State != "draft" || c.Version != in.ExpectedVersion {
			return Result{409, Problem{Code: "CAMPAIGN_CONFLICT", Message: "Only the current draft can be edited. Activated steps are immutable."}}, nil
		}
		_, err = tx.Exec(ctx, `UPDATE campaigns SET name=$2,steps=$3,asset_ids=$4,timezone=$5,start_hour=$6,end_hour=$7,weekdays_only=$8,version=version+1,updated_at=clock_timestamp() WHERE id=$1`, id, strings.TrimSpace(in.Name), raw, in.AssetIDs, in.Timezone, in.StartHour, in.EndHour, in.WeekdaysOnly)
		status = 200
	}
	if err != nil {
		return Result{}, err
	}
	if err = audit(ctx, tx, u, "campaign.saved", nil, id, "Draft saved; no outreach submitted."); err != nil {
		return Result{}, err
	}
	c, err := scanCampaign(tx.QueryRow(ctx, "SELECT "+campaignColumns+" FROM campaigns WHERE id=$1", id))
	if err != nil {
		return Result{}, err
	}
	return Result{status, map[string]any{"campaign": c}}, tx.Commit(ctx)
}
func (s *Store) CampaignAction(ctx context.Context, u User, id, action string, version int64) (Result, error) {
	if u.Role != "operator" {
		return Result{403, Problem{Code: "FORBIDDEN", Message: "An operator is required."}}, nil
	}
	tx, err := s.Begin(ctx, u.WorkspaceID, false)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback(ctx)
	if err = campaignLock(ctx, tx, u.WorkspaceID); err != nil {
		return Result{}, err
	}
	c, err := scanCampaign(tx.QueryRow(ctx, "SELECT "+campaignColumns+" FROM campaigns WHERE id=$1 FOR UPDATE", id))
	if err != nil {
		return deliveryMissing(err)
	}
	if c.Version != version {
		return Result{409, Problem{Code: "CAMPAIGN_CONFLICT", Message: "This campaign changed. Refresh before acting."}}, nil
	}
	target := ""
	switch action {
	case "activate":
		if c.State == "draft" {
			target = "active"
		}
	case "pause":
		if c.State == "active" {
			target = "paused"
		}
	case "resume":
		if c.State == "paused" {
			target = "active"
		}
	case "cancel":
		if c.State != "cancelled" {
			target = "cancelled"
		}
	}
	if target == "" {
		return Result{409, Problem{Code: "INVALID_TRANSITION", Message: "This action is unavailable in the recorded campaign state."}}, nil
	}
	if target == "active" {
		for _, aid := range c.AssetIDs {
			if _, err = tx.Exec(ctx, "INSERT INTO sending_limits(workspace_id,asset_id) VALUES($1,$2) ON CONFLICT DO NOTHING", u.WorkspaceID, aid); err != nil {
				return Result{}, err
			}
			a, e := assetByID(ctx, tx, aid, false)
			if e != nil {
				return Result{}, e
			}
			if a.Status != "active" {
				return Result{422, Problem{Code: "ASSET_UNAVAILABLE", Message: "Restore the selected assets before activation."}}, nil
			}
			if a.Kind == "email" {
				ok, e := emailCampaignReady(ctx, tx, aid)
				if e != nil {
					return Result{}, e
				}
				if !ok {
					return Result{422, Problem{Code: "EMAIL_DIAGNOSTICS_REQUIRED", Message: "Run email diagnostics and resolve configuration failures before activation."}}, nil
				}
			}
		}
	}
	if _, err = tx.Exec(ctx, "UPDATE campaigns SET state=$2,version=version+1,updated_at=clock_timestamp() WHERE id=$1", id, target); err != nil {
		return Result{}, err
	}
	if target == "active" {
		if _, err = tx.Exec(ctx, `UPDATE delivery_jobs SET next_attempt_at=now(),last_error=NULL WHERE state='queued' AND last_error='CAMPAIGN_PAUSED' AND id IN (SELECT x.job_id FROM campaign_executions x JOIN campaign_enrollments e ON e.workspace_id=x.workspace_id AND e.id=x.enrollment_id WHERE e.campaign_id=$1)`, id); err != nil {
			return Result{}, err
		}
	}
	if target == "cancelled" {
		if _, err = tx.Exec(ctx, `UPDATE delivery_jobs SET state='suppressed',last_error='CAMPAIGN_CANCELLED',updated_at=now() WHERE state='queued' AND id IN (SELECT x.job_id FROM campaign_executions x JOIN campaign_enrollments e ON e.workspace_id=x.workspace_id AND e.id=x.enrollment_id WHERE e.campaign_id=$1)`, id); err != nil {
			return Result{}, err
		}
		if _, err = tx.Exec(ctx, "UPDATE campaign_enrollments SET state='stopped',reason='CAMPAIGN_CANCELLED',updated_at=now() WHERE campaign_id=$1 AND state NOT IN ('completed','replied','stopped')", id); err != nil {
			return Result{}, err
		}
	}
	if err = audit(ctx, tx, u, "campaign."+action, nil, id, "State changed to "+target+". Already submitted jobs cannot be recalled."); err != nil {
		return Result{}, err
	}
	c, err = scanCampaign(tx.QueryRow(ctx, "SELECT "+campaignColumns+" FROM campaigns WHERE id=$1", id))
	if err != nil {
		return Result{}, err
	}
	return Result{200, map[string]any{"campaign": c}}, tx.Commit(ctx)
}

type EnrollInput struct {
	ContactIDs []string `json:"contactIds"`
	Timezone   string   `json:"timezone"`
}

func (s *Store) EnrollCampaign(ctx context.Context, u User, id string, in EnrollInput) (Result, error) {
	if u.Role != "operator" {
		return Result{403, Problem{Code: "FORBIDDEN", Message: "An operator is required."}}, nil
	}
	if len(in.ContactIDs) < 1 || len(in.ContactIDs) > 20 {
		return Result{422, Problem{Code: "INVALID_ENROLLMENT", Message: "Select 1 to 20 synthetic contacts."}}, nil
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil {
		return Result{422, Problem{Code: "TIMEZONE_REQUIRED", Message: "Specify the recipient IANA time zone. It is not inferred from a phone number."}}, nil
	}
	tx, err := s.Begin(ctx, u.WorkspaceID, false)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback(ctx)
	if err = campaignLock(ctx, tx, u.WorkspaceID); err != nil {
		return Result{}, err
	}
	c, err := scanCampaign(tx.QueryRow(ctx, "SELECT "+campaignColumns+" FROM campaigns WHERE id=$1 FOR UPDATE", id))
	if err != nil {
		return deliveryMissing(err)
	}
	if c.State != "active" {
		return Result{409, Problem{Code: "CAMPAIGN_INACTIVE", Message: "Activate the campaign before enrollment."}}, nil
	}
	results := []map[string]any{}
	for _, cid := range in.ContactIDs {
		if len(cid) > 80 {
			return Result{422, Problem{Code: "INVALID_CONTACT", Message: "Contact identifiers must be bounded."}}, nil
		}
		contact, e := contactByID(ctx, tx, cid, true)
		if errors.Is(e, pgx.ErrNoRows) {
			results = append(results, map[string]any{"contactId": cid, "state": "rejected", "reason": "NOT_FOUND"})
			continue
		}
		if e != nil {
			return Result{}, e
		}
		reason := ""
		if contact.DNC {
			reason = "DNC_SUPPRESSED"
		}
		if contact.OptedOutAt != nil {
			reason = "OPTED_OUT"
		}
		if reason != "" {
			results = append(results, map[string]any{"contactId": cid, "state": "rejected", "reason": reason})
			continue
		}
		next := campaignWindow(time.Now().Add(time.Duration(c.Steps[0].DelaySeconds)*time.Second), in.Timezone, c.StartHour, c.EndHour, c.WeekdaysOnly)
		eid := ID("enrollment")
		cmd, e := tx.Exec(ctx, `INSERT INTO campaign_enrollments(workspace_id,id,campaign_id,contact_id,timezone,next_run_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(workspace_id,campaign_id,contact_id) DO NOTHING`, u.WorkspaceID, eid, id, cid, in.Timezone, next)
		if e != nil {
			return Result{}, e
		}
		state := "enrolled"
		if cmd.RowsAffected() == 0 {
			state = "already_enrolled"
		} else if e = audit(ctx, tx, u, "campaign.enrolled", nil, eid, "Synthetic contact "+cid+"; recipient timezone "+in.Timezone); e != nil {
			return Result{}, e
		}
		results = append(results, map[string]any{"contactId": cid, "state": state, "reason": ""})
	}
	return Result{200, map[string]any{"results": results}}, tx.Commit(ctx)
}
func stopCampaignContact(ctx context.Context, tx pgx.Tx, contact, reason string) error {
	state := "stopped"
	if reason == "INBOUND_REPLY" {
		state = "replied"
	}
	rows, err := tx.Query(ctx, `UPDATE campaign_enrollments SET state=$2,reason=$3,updated_at=clock_timestamp() WHERE contact_id=$1 AND state IN ('scheduled','deferred','awaiting_confirmation','held') RETURNING id`, contact, state, reason)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err = audit(ctx, tx, systemUser, "campaign."+state, nil, id, reason); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE delivery_jobs SET state='suppressed',last_error=$2,updated_at=clock_timestamp() WHERE state='queued' AND id IN (SELECT x.job_id FROM campaign_executions x JOIN campaign_enrollments e ON e.workspace_id=x.workspace_id AND e.id=x.enrollment_id WHERE e.contact_id=$1 AND e.state IN ('replied','stopped'))`, contact, reason)
	return err
}
func updateEnrollment(ctx context.Context, tx pgx.Tx, id, state, reason string, next time.Time) error {
	_, err := tx.Exec(ctx, "UPDATE campaign_enrollments SET state=$2,reason=$3,next_run_at=$4,updated_at=clock_timestamp() WHERE id=$1", id, state, reason, next)
	return err
}
