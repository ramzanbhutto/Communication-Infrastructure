package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
)

var systemUser = User{ID: "simulated-worker", WorkspaceID: DemoWorkspace, Name: "Simulated provider", Role: "operator"}

func seed(ctx context.Context, tx pgx.Tx, generation int64) error {
	statements := []string{
		`INSERT INTO assets(workspace_id,id,kind,name,address,status,version,email_config) VALUES
 ($1,'line-delta','phone','Delta 02','+12025550122','active',$2,NULL),
 ($1,'line-cedar','phone','Cedar 01','+12025550121','active',$2,NULL),
 ($1,'email-north','email','North sending domain','north.example.test','active',$2,'{"spf":true,"dkim":true,"dmarc":"quarantine","warmupDay":18,"source":"synthetic_fixture"}'),
 ($1,'email-east','email','East sending domain','east.example.test','active',$2,'{"spf":true,"dkim":false,"dmarc":"none","warmupDay":4,"source":"synthetic_fixture"}')`,
		`INSERT INTO observations(workspace_id,asset_id,attempts,filtered,spam_label,observed_at) VALUES
 ($1,'line-delta',250,3,false,now()-interval '5 hours'),($1,'line-delta',250,5,false,now()-interval '4 hours'),
 ($1,'line-delta',250,8,false,now()-interval '3 hours'),($1,'line-delta',250,16,false,now()-interval '2 hours'),
 ($1,'line-delta',250,28,true,now()-interval '1 hour'),($1,'line-cedar',200,2,false,now()-interval '30 minutes')`,
		`INSERT INTO contacts(workspace_id,id,name,phone,email,dnc,opted_out_at,sms_consent_at,consent_source,warm_signal,warm_at,email_opened_at) VALUES
 ($1,'c101','Jordan Wells','+12025550101','jordan@example.test',false,NULL,NULL,NULL,NULL,NULL,now()-interval '2 hours'),
 ($1,'c102','Avery Brooks','+12025550102','avery@example.test',false,NULL,now()-interval '3 days','synthetic opt-in form','inbound_reply',now()-interval '90 minutes',NULL),
 ($1,'c103','Morgan Patel','+12025550103','morgan@example.test',true,NULL,now()-interval '3 days','synthetic opt-in form','inbound_reply',now()-interval '2 hours',NULL),
 ($1,'c104','Casey Reed','+12025550104','casey@example.test',false,now()-interval '1 day',NULL,NULL,NULL,NULL,NULL),
 ($1,'c105','Riley Chen','+12025550105','riley@example.test',false,NULL,NULL,NULL,NULL,NULL,NULL)`,
		`INSERT INTO channel_permissions(workspace_id,contact_id,channel,granted_at,evidence) VALUES($1,'c102','email',now(),'Explicit synthetic lab opt-in')`,
		`INSERT INTO email_ramps(workspace_id,asset_id) VALUES($1,'email-north')`,
	}
	for i, q := range statements {
		args := []any{DemoWorkspace}
		if i == 0 {
			args = append(args, generation*100)
		}
		if _, err := tx.Exec(ctx, q, args...); err != nil {
			return err
		}
	}
	a, err := assetByID(ctx, tx, "line-cedar", false)
	if err != nil {
		return err
	}
	for _, item := range []struct{ id, channel, reason string }{{"c101", "sms", "NO_SMS_CONSENT"}, {"c103", "call", "DNC_SUPPRESSED"}, {"c104", "sms", "OPTED_OUT"}} {
		c, e := contactByID(ctx, tx, item.id, false)
		if e != nil {
			return e
		}
		if _, e = recordDecision(ctx, tx, systemUser, c, a, item.channel, item.reason); e != nil {
			return e
		}
	}
	// One already persisted reply gives context while three durable events wait.
	_, err = tx.Exec(ctx, `INSERT INTO replies(workspace_id,id,contact_id,asset_id,body,tag,received_at) VALUES($1,'reply-initial','c102','email-north','Could you send the property details?','interested',now()-interval '90 minutes')`, DemoWorkspace)
	if err != nil {
		return err
	}
	for i, item := range []struct{ contact, body, tag string }{{"c102", "Thursday afternoon works. Please send the details.", "interested"}, {"c101", "Which neighborhood is the property in?", "question"}, {"c105", "STOP", "opt-out"}} {
		payload, _ := json.Marshal(map[string]string{"contactId": item.contact, "assetId": "email-north", "body": item.body, "tag": item.tag})
		_, err = tx.Exec(ctx, "INSERT INTO outbox(workspace_id,id,generation,payload) VALUES($1,$2,$3,$4)", DemoWorkspace, fmt.Sprintf("reply-g%d-%d", generation, i), generation, payload)
		if err != nil {
			return err
		}
	}
	return audit(ctx, tx, systemUser, "demo.seed", nil, nil, "Synthetic scenario records created. Reply consumer starts paused.")
}
func (s *Store) EnsureDemo(ctx context.Context) error {
	tx, err := s.Begin(ctx, DemoWorkspace, true)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM demo_state)").Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if _, err = tx.Exec(ctx, "INSERT INTO demo_state(workspace_id) VALUES($1)", DemoWorkspace); err != nil {
			return err
		}
		if err = seed(ctx, tx, 1); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (s *Store) ResetDemo(ctx context.Context, u User) (Result, error) {
	return s.resetDemo(ctx, u, false)
}
func (s *Store) resetDemo(ctx context.Context, u User, discardUnconfirmed bool) (Result, error) {
	if u.Role != "operator" || u.WorkspaceID != DemoWorkspace {
		return Result{403, Problem{Code: "FORBIDDEN", Message: "Only the demo operator can reset these fixtures."}}, nil
	}
	tx, err := s.Begin(ctx, DemoWorkspace, true)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback(ctx)
	var generation int64
	var infrastructureActive bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM delivery_jobs WHERE state IN ('queued','submitting','accepted','unknown'))").Scan(&infrastructureActive); err != nil {
		return Result{}, err
	}
	if infrastructureActive && !discardUnconfirmed {
		return Result{409, Problem{Code: "INFRASTRUCTURE_BUSY", Message: "Resolve pending or unconfirmed infrastructure jobs before resetting the demo."}}, nil
	}
	if err = tx.QueryRow(ctx, "UPDATE demo_state SET generation=generation+1,replies_paused=true,reset_at=clock_timestamp() RETURNING generation").Scan(&generation); err != nil {
		return Result{}, err
	}
	// RLS and a fixed table list restrict deletion to the isolated demo workspace.
	for _, t := range []string{"delivery_events", "delivery_jobs", "infrastructure_inbox", "infrastructure_dns", "infrastructure_sip", "email_ramps", "channel_permissions", "operations", "messages", "calls", "decisions", "replies", "outbox", "observations", "audit", "contacts", "assets"} {
		if _, err = tx.Exec(ctx, "DELETE FROM "+t); err != nil {
			return Result{}, err
		}
	}
	if err = seed(ctx, tx, generation); err != nil {
		return Result{}, err
	}
	if s.IMessageBridge != nil {
		if err = seedIMessage(ctx, tx); err != nil {
			return Result{}, err
		}
	}
	if err = audit(ctx, tx, u, "demo.reset", nil, nil, "Only synthetic workspace records were reset."); err != nil {
		return Result{}, err
	}
	// A new Redis namespace is selected after commit. No existing stream is deleted.
	if s.IMessageLab != nil {
		err = s.IMessageLab.ResetAfter(tx.Commit, ctx)
		return Result{200, map[string]any{"generation": generation, "simulated": true}}, err
	}
	return Result{200, map[string]any{"generation": generation, "simulated": true}}, tx.Commit(ctx)
}
