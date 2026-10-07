package ops

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"strings"
	"time"
)

func (s *Store) campaignStep(ctx context.Context) error {
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
	var id, cid, contact, zone, state string
	var step int
	var next time.Time
	err = tx.QueryRow(ctx, `SELECT e.id,e.campaign_id,e.contact_id,e.timezone,e.state,e.step,e.next_run_at FROM campaign_enrollments e JOIN campaigns c ON c.workspace_id=e.workspace_id AND c.id=e.campaign_id WHERE c.state='active' AND e.state IN ('scheduled','deferred','awaiting_confirmation','held') AND e.next_run_at<=now() ORDER BY e.next_run_at,e.id LIMIT 1 FOR UPDATE OF e SKIP LOCKED`).Scan(&id, &cid, &contact, &zone, &state, &step, &next)
	if errors.Is(err, pgx.ErrNoRows) {
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	c, err := scanCampaign(tx.QueryRow(ctx, "SELECT "+campaignColumns+" FROM campaigns WHERE id=$1", cid))
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	ct, err := contactByID(ctx, tx, contact, true)
	if err != nil {
		return err
	}
	if ct.DNC || ct.OptedOutAt != nil {
		if err = stopCampaignContact(ctx, tx, contact, "SUPPRESSED"); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	var replied bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM infrastructure_inbox WHERE contact_id=$1 AND received_at>=(SELECT enrolled_at FROM campaign_enrollments WHERE id=$2)) OR EXISTS(SELECT 1 FROM replies WHERE contact_id=$1 AND persisted_at>=(SELECT enrolled_at FROM campaign_enrollments WHERE id=$2))`, contact, id).Scan(&replied); err != nil {
		return err
	}
	if replied {
		if err = stopCampaignContact(ctx, tx, contact, "INBOUND_REPLY"); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if state == "awaiting_confirmation" || state == "held" {
		var jobState string
		var jobAt time.Time
		err = tx.QueryRow(ctx, `SELECT j.state,j.updated_at FROM campaign_executions x JOIN delivery_jobs j ON j.workspace_id=x.workspace_id AND j.id=x.job_id WHERE x.enrollment_id=$1 AND x.step=$2`, id, step).Scan(&jobState, &jobAt)
		if err != nil {
			return err
		}
		if jobState == "delivered" {
			step++
			if step == len(c.Steps) {
				err = updateEnrollment(ctx, tx, id, "completed", "ALL_STEPS_CONFIRMED", now)
			} else {
				due := campaignWindow(jobAt.Add(time.Duration(c.Steps[step].DelaySeconds)*time.Second), zone, c.StartHour, c.EndHour, c.WeekdaysOnly)
				_, err = tx.Exec(ctx, "UPDATE campaign_enrollments SET step=$2,state='scheduled',reason='PREVIOUS_STEP_CONFIRMED',next_run_at=$3,updated_at=now() WHERE id=$1", id, step, due)
			}
			if err == nil {
				err = audit(ctx, tx, systemUser, "campaign.advanced", nil, id, "Verified delivery advanced the enrollment.")
			}
		} else if jobState == "failed" || jobState == "suppressed" {
			err = updateEnrollment(ctx, tx, id, "stopped", "DELIVERY_"+strings.ToUpper(jobState), now)
			if err == nil {
				err = audit(ctx, tx, systemUser, "campaign.stopped", nil, id, "Delivery ended as "+jobState)
			}
		} else {
			target, reason := "awaiting_confirmation", "WAITING_FOR_RECEIPT"
			if jobState == "unknown" {
				target = "held"
				reason = "AMBIGUOUS_DELIVERY"
			}
			err = updateEnrollment(ctx, tx, id, target, reason, now.Add(2*time.Second))
		}
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	window := campaignWindow(now, zone, c.StartHour, c.EndHour, c.WeekdaysOnly)
	if window.After(now) {
		if err = updateEnrollment(ctx, tx, id, "deferred", "OUTSIDE_SENDING_WINDOW", window); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	spec := c.Steps[step]
	var selected Asset
	reason := "NO_ELIGIBLE_ASSET"
	// Stable pool order keeps selection reproducible; capacity is reserved in QueueDelivery.
	for _, aid := range c.AssetIDs {
		a, e := assetByID(ctx, tx, aid, true)
		if e != nil {
			return e
		}
		if (spec.Channel == "email" && a.Kind != "email") || (spec.Channel == "sms" && a.Kind != "phone") {
			continue
		}
		r, e := deliveryGate(ctx, tx, ct, a, spec.Channel)
		if e != nil {
			return e
		}
		if r != "ELIGIBLE" {
			reason = r
			continue
		}
		if spec.Channel == "email" {
			ready, e := emailCampaignReady(ctx, tx, aid)
			if e != nil {
				return e
			}
			if !ready {
				reason = "EMAIL_DIAGNOSTICS_REQUIRED"
				continue
			}
		}
		r, e = sendingCapacity(ctx, tx, aid)
		if e != nil {
			return e
		}
		if r != "" {
			reason = r
			continue
		}
		selected = a
		break
	}
	if selected.ID == "" {
		target := "deferred"
		retry := now.Add(time.Minute)
		if reason == "NO_SMS_CONSENT" || reason == "NO_EMAIL_PERMISSION" || reason == "NO_WARM_REPLY" {
			target = "stopped"
		}
		if reason == "ASSET_DAILY_CAP" {
			retry = time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
		}
		if err = updateEnrollment(ctx, tx, id, target, reason, retry); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	render := func(v string) string {
		return strings.ReplaceAll(strings.ReplaceAll(v, "{name}", ct.Name), "{contact_id}", ct.ID)
	}
	result, err := s.queueDeliveryInTx(ctx, systemUser, DeliveryInput{Channel: spec.Channel, ContactID: contact, AssetID: selected.ID, RequestKey: fmt.Sprintf("campaign:%s:%d", id, step), Subject: render(spec.Subject), Body: render(spec.Body)}, tx)
	if err != nil {
		return err
	}
	if result.Status != 202 && result.Status != 200 {
		p, ok := result.Body.(Problem)
		if !ok {
			return errors.New("unexpected campaign delivery result")
		}
		target := "stopped"
		retry := now
		if p.Code == "TOUCH_LIMIT" || p.Code == "ASSET_DAILY_CAP" {
			target = "deferred"
			retry = now.Add(24 * time.Hour)
		}
		if err = updateEnrollment(ctx, tx, id, target, p.Code, retry); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	job := result.Body.(map[string]any)["job"].(DeliveryJob)
	if _, err = tx.Exec(ctx, "INSERT INTO campaign_executions(workspace_id,enrollment_id,step,job_id) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING", DemoWorkspace, id, step, job.ID); err != nil {
		return err
	}
	if err = updateEnrollment(ctx, tx, id, "awaiting_confirmation", "JOB_QUEUED", now.Add(time.Second)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) campaignDispatchGate(ctx context.Context, tx pgx.Tx, j DeliveryJob) (bool, error) {
	var c Campaign
	var zone, state string
	row := tx.QueryRow(ctx, `SELECT `+"c.id,c.name,c.state,c.version,c.steps,c.asset_ids,c.timezone,c.start_hour,c.end_hour,c.weekdays_only,c.created_at,c.updated_at"+`,e.timezone,e.state FROM campaign_executions x JOIN campaign_enrollments e ON e.workspace_id=x.workspace_id AND e.id=x.enrollment_id JOIN campaigns c ON c.workspace_id=e.workspace_id AND c.id=e.campaign_id WHERE x.job_id=$1`, j.ID)
	var raw []byte
	err := row.Scan(&c.ID, &c.Name, &c.State, &c.Version, &raw, &c.AssetIDs, &c.Timezone, &c.StartHour, &c.EndHour, &c.WeekdaysOnly, &c.CreatedAt, &c.UpdatedAt, &zone, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if c.State == "cancelled" || state == "stopped" || state == "replied" {
		_, err = tx.Exec(ctx, "UPDATE delivery_jobs SET state='suppressed',last_error='CAMPAIGN_STOPPED',updated_at=now() WHERE id=$1", j.ID)
		return false, err
	}
	due := campaignWindow(time.Now(), zone, c.StartHour, c.EndHour, c.WeekdaysOnly)
	reason := ""
	if c.State == "paused" {
		reason = "CAMPAIGN_PAUSED"
		due = time.Now().Add(time.Minute)
	} else if due.After(time.Now()) {
		reason = "OUTSIDE_SENDING_WINDOW"
	} else if j.Channel == "email" && j.AssetID != nil {
		ready, e := emailCampaignReady(ctx, tx, *j.AssetID)
		if e != nil {
			return false, e
		}
		if !ready {
			reason = "EMAIL_DIAGNOSTICS_REQUIRED"
			due = time.Now().Add(time.Minute)
		}
	}
	if reason != "" {
		_, err = tx.Exec(ctx, "UPDATE delivery_jobs SET next_attempt_at=$2,last_error=$3 WHERE id=$1", j.ID, due, reason)
		return false, err
	}
	return true, nil
}
