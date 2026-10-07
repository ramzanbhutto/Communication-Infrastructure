package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/redis/go-redis/v9"
	"strings"
	"time"
)

const replyGroup = "reply-persistence"

func streamKey(generation int64) string {
	return fmt.Sprintf("covent-ops:demo:g%d:replies", generation)
}
func (s *Store) Queue(ctx context.Context, generation int64, paused bool, unpublished int64) Queue {
	q := Queue{State: "ready", Paused: paused, Unpublished: unpublished, CheckedAt: time.Now().UTC()}
	if paused {
		q.State = "paused"
	}
	groups, err := s.Redis.XInfoGroups(ctx, streamKey(generation)).Result()
	if err != nil {
		if strings.Contains(err.Error(), "no such key") || strings.Contains(err.Error(), "NOGROUP") {
			q.State = "initializing"
			message := "The new demo stream is being prepared. The transport count is not available yet."
			q.Error = &message
			return q
		}
		q.State = "unavailable"
		message := "Redis queue data is unavailable. No backlog count can be confirmed."
		q.Error = &message
		return q
	}
	for _, g := range groups {
		if g.Name == replyGroup && g.Lag >= 0 {
			n := g.Pending + g.Lag
			q.Pending = &n
			return q
		}
	}
	q.State = "initializing"
	message := "The consumer group has not reported a count yet."
	q.Error = &message
	return q
}
func (s *Store) RunWorker(ctx context.Context) {
	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()
	consumer := ID("consumer")
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			delivery, finishDelivery := context.WithTimeout(ctx, 10*time.Second)
			campaignErr := s.campaignStep(delivery)
			rampErr := s.rampStep(delivery)
			deliveryErr := s.deliveryStep(delivery)
			s.DeliveryFailed.Store(campaignErr != nil || rampErr != nil || deliveryErr != nil)
			if campaignErr == nil && rampErr == nil && deliveryErr == nil {
				s.DeliverySuccess.Store(time.Now().UnixMilli())
			}
			finishDelivery()
			// A tick has its own deadline. Dependency failure never stops call completion.
			step, cancel := context.WithTimeout(ctx, 3*time.Second)
			_ = s.finishCalls(step)
			err := s.replyStep(step, consumer)
			s.ReplyFailed.Store(err != nil)
			if err == nil {
				s.ReplySuccess.Store(time.Now().UnixMilli())
			}
			cancel()
		}
	}
}
func (s *Store) finishCalls(ctx context.Context) error {
	tx, err := s.Begin(ctx, DemoWorkspace, false)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, "UPDATE calls SET status='completed',outcome='answered_simulated',completed_at=clock_timestamp() WHERE status='active' AND finish_at<=now() RETURNING id,asset_id,actor_id,actor_name")
	if err != nil {
		return err
	}
	type item struct{ id, asset, actor, name string }
	items := []item{}
	for rows.Next() {
		var i item
		if err = rows.Scan(&i.id, &i.asset, &i.actor, &i.name); err != nil {
			rows.Close()
			return err
		}
		items = append(items, i)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, i := range items {
		u := User{ID: i.actor, Name: i.name, WorkspaceID: DemoWorkspace}
		if err = audit(ctx, tx, u, "call.completed", i.asset, i.id, "Simulated provider returned an answered outcome."); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (s *Store) replyStep(ctx context.Context, consumer string) error {
	// Keep the reset lock through publication and persistence so generations cannot mix.
	tx, err := s.Begin(ctx, DemoWorkspace, false)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = campaignLock(ctx, tx, DemoWorkspace); err != nil {
		return err
	}
	var generation int64
	var paused bool
	if err = tx.QueryRow(ctx, "SELECT generation,replies_paused FROM demo_state").Scan(&generation, &paused); err != nil {
		return err
	}
	key := streamKey(generation)
	err = s.Redis.XGroupCreateMkStream(ctx, key, replyGroup, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return err
	}
	rows, err := tx.Query(ctx, "SELECT id,payload,created_at FROM outbox WHERE published_at IS NULL AND generation=$1 ORDER BY created_at,id LIMIT 20 FOR UPDATE SKIP LOCKED", generation)
	if err != nil {
		return err
	}
	type event struct {
		id  string
		raw []byte
		at  time.Time
	}
	events := []event{}
	for rows.Next() {
		var e event
		if err = rows.Scan(&e.id, &e.raw, &e.at); err != nil {
			rows.Close()
			return err
		}
		events = append(events, e)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, e := range events {
		if err = s.Redis.XAdd(ctx, &redis.XAddArgs{Stream: key, Values: map[string]any{"eventId": e.id, "payload": string(e.raw), "receivedAt": e.at.Format(time.RFC3339Nano)}}).Err(); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "UPDATE outbox SET published_at=clock_timestamp() WHERE id=$1", e.id); err != nil {
			return err
		}
	}
	if paused {
		return tx.Commit(ctx)
	}
	// Recover abandoned pending events. A committed reply is deduplicated by event ID.
	messages, _, err := s.Redis.XAutoClaim(ctx, &redis.XAutoClaimArgs{Stream: key, Group: replyGroup, Consumer: consumer, MinIdle: 2 * time.Second, Start: "0-0", Count: 20}).Result()
	if err != nil {
		return err
	}
	if len(messages) == 0 {
		streams, e := s.Redis.XReadGroup(ctx, &redis.XReadGroupArgs{Group: replyGroup, Consumer: consumer, Streams: []string{key, ">"}, Count: 20, Block: -1}).Result()
		if e != nil && e != redis.Nil {
			return e
		}
		for _, st := range streams {
			messages = append(messages, st.Messages...)
		}
	}
	ack := []string{}
	for _, m := range messages {
		raw, ok := m.Values["payload"].(string)
		if !ok {
			return fmt.Errorf("invalid synthetic event payload")
		}
		eventID, ok := m.Values["eventId"].(string)
		if !ok {
			return fmt.Errorf("missing synthetic event identity")
		}
		received, ok := m.Values["receivedAt"].(string)
		if !ok {
			return fmt.Errorf("missing event timestamp")
		}
		at, e := time.Parse(time.RFC3339Nano, received)
		if e != nil {
			return e
		}
		var p struct {
			ContactID string `json:"contactId"`
			AssetID   string `json:"assetId"`
			Body      string `json:"body"`
			Tag       string `json:"tag"`
		}
		if e = json.Unmarshal([]byte(raw), &p); e != nil {
			return e
		}
		tag := p.Tag
		if strings.EqualFold(strings.TrimSpace(p.Body), "STOP") {
			tag = "opt-out"
		}
		command, e := tx.Exec(ctx, "INSERT INTO replies(workspace_id,id,contact_id,asset_id,body,tag,received_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING", DemoWorkspace, eventID, p.ContactID, p.AssetID, p.Body, tag, at)
		if e != nil {
			return e
		}
		if command.RowsAffected() > 0 {
			if tag == "opt-out" {
				if _, e = tx.Exec(ctx, "UPDATE contacts SET opted_out_at=coalesce(opted_out_at,$2) WHERE id=$1", p.ContactID, at); e != nil {
					return e
				}
			}
			if tag == "interested" {
				if _, e = tx.Exec(ctx, "UPDATE contacts SET warm_signal='inbound_reply',warm_at=$2 WHERE id=$1", p.ContactID, at); e != nil {
					return e
				}
			}
			reason := "INBOUND_REPLY"
			if tag == "opt-out" {
				reason = "OPTED_OUT"
			}
			if e = stopCampaignContact(ctx, tx, p.ContactID, reason); e != nil {
				return e
			}
			if e = audit(ctx, tx, systemUser, "reply.persisted", p.AssetID, eventID, "Synthetic reply stored and suppression rules applied."); e != nil {
				return e
			}
		}
		ack = append(ack, m.ID)
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	// Never acknowledge before the database commit. A failed acknowledgement is safe to replay.
	if len(ack) > 0 {
		return s.Redis.XAck(ctx, key, replyGroup, ack...).Err()
	}
	return nil
}
