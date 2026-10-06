package ops

import (
	"context"
	"time"
)

// A volume ramp schedules one requested test message per opted-in contact per
// UTC day. It never opens messages, manufactures replies or rotates identities.
func (s *Store) rampStep(ctx context.Context) error {
	if s.Gateway == nil {
		return nil
	}
	tx, err := s.Begin(ctx, DemoWorkspace, false)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Pause on actual recorded bounces/complaints. A recorded signal is stronger
	// than an arbitrary dashboard reputation score.
	_, err = tx.Exec(ctx, `UPDATE email_ramps SET enabled=false WHERE enabled AND EXISTS(SELECT 1 FROM delivery_events e JOIN delivery_jobs j ON j.id=e.job_id AND j.workspace_id=e.workspace_id WHERE j.asset_id=email_ramps.asset_id AND e.provider_code IN ('email.bounced','email.complained') AND e.received_at>now()-interval '24 hours')`)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT r.asset_id,p.contact_id FROM email_ramps r JOIN infrastructure_dns d ON d.asset_id=r.asset_id AND d.workspace_id=r.workspace_id CROSS JOIN channel_permissions p JOIN contacts c ON c.id=p.contact_id AND c.workspace_id=p.workspace_id WHERE r.enabled AND p.channel='email' AND NOT c.dnc AND c.opted_out_at IS NULL AND d.checked_at>now()-interval '24 hours' AND d.records->'spf'->>'state'='present' AND d.records->'dkim'->>'state'='present' AND d.records->'dmarc'->>'state'='present' ORDER BY r.asset_id,p.contact_id LIMIT 20`)
	if err != nil {
		return err
	}
	type item struct{ asset, contact string }
	items := []item{}
	for rows.Next() {
		var in item
		if err = rows.Scan(&in.asset, &in.contact); err != nil {
			rows.Close()
			return err
		}
		items = append(items, in)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	u := systemUser
	u.Role = "operator"
	for _, item := range items {
		key := "ramp-" + Hash(item.asset + ":" + item.contact + ":" + time.Now().UTC().Format("2006-01-02"))[:32]
		_, err = s.QueueDelivery(ctx, u, DeliveryInput{Channel: "email", ContactID: item.contact, AssetID: item.asset, RequestKey: key, Purpose: "ramp", Subject: "Requested property update", Body: "This is the scheduled synthetic property update requested by this opted-in test recipient."})
		if err != nil {
			return err
		}
	}
	return nil
}
