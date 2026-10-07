package ops

import (
	"communication-infrastructure/internal/provider"
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"net"
	"net/http"
	"time"
)

func emailCampaignReady(ctx context.Context, tx pgx.Tx, asset string) (bool, error) {
	var ready bool
	err := tx.QueryRow(ctx, `SELECT coalesce((SELECT assessment->>'state'='configuration_ready' AND (source<>'synthetic_dns_fixture' OR assessment->>'fixtureScenario'=(SELECT scenario FROM email_fixture_state WHERE asset_id=$1)) AND checked_at>now()-interval '24 hours' FROM email_assessments WHERE asset_id=$1 ORDER BY checked_at DESC,id DESC LIMIT 1),false)`, asset).Scan(&ready)
	return ready, err
}
func (s *Store) assessEmail(ctx context.Context, u User, asset, selector, mode string) (provider.EmailAssessment, error) {
	tx, err := s.Begin(ctx, u.WorkspaceID, false)
	if err != nil {
		return provider.EmailAssessment{}, err
	}
	defer tx.Rollback(ctx)
	a, err := assetByID(ctx, tx, asset, false)
	if err != nil {
		return provider.EmailAssessment{}, err
	}
	if a.Kind != "email" {
		return provider.EmailAssessment{}, Problem{Code: "EMAIL_REQUIRED", Message: "Choose an email sending asset."}
	}
	scenario := "healthy"
	err = tx.QueryRow(ctx, "SELECT scenario FROM email_fixture_state WHERE asset_id=$1", asset).Scan(&scenario)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return provider.EmailAssessment{}, err
	}
	domain, source := a.Address, "synthetic_dns_fixture"
	var resolver provider.EmailResolver = provider.EmailFixtureDNS{Domain: domain, Scenario: scenario}
	if mode == "native" {
		target, ok := s.DNSTargets[asset]
		if !ok {
			return provider.EmailAssessment{}, Problem{Code: "DNS_NOT_CONFIGURED", Message: "Native DNS requires a server-configured diagnostic target for an owned domain."}
		}
		domain = target
		source = "native_dns"
		resolver = provider.NativeDNS{Resolver: net.DefaultResolver}
	} else if mode != "fixture" {
		return provider.EmailAssessment{}, Problem{Code: "INVALID_MODE", Message: "Choose fixture or native DNS explicitly."}
	}
	result, err := provider.InspectEmail(ctx, resolver, domain, selector, source)
	if mode == "fixture" {
		result.FixtureScenario = scenario
	}
	if err != nil {
		return result, Problem{Code: "INVALID_DNS_INPUT", Message: err.Error()}
	}
	// Configuration may change while the network lookup runs. Recheck it before
	// publishing an assessment; all changes and scheduling share the same lock.
	if err = campaignLock(ctx, tx, u.WorkspaceID); err != nil {
		return result, err
	}
	var current string
	e := tx.QueryRow(ctx, "SELECT scenario FROM email_fixture_state WHERE asset_id=$1", asset).Scan(&current)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return result, e
	}
	if mode == "fixture" && e == nil && current != scenario {
		return result, Problem{Code: "DNS_CONFIG_CHANGED", Message: "The fixture changed during inspection. Run the check again."}
	}
	raw, _ := json.Marshal(result)
	if _, err = tx.Exec(ctx, "INSERT INTO email_assessments(workspace_id,id,asset_id,assessment,source) VALUES($1,$2,$3,$4,$5)", u.WorkspaceID, ID("assessment"), asset, raw, source); err != nil {
		return result, err
	}
	// Clearing a configuration defer makes it eligible for reevaluation, not a send.
	if result.State == "configuration_ready" {
		if _, err = tx.Exec(ctx, `UPDATE campaign_enrollments SET next_run_at=now() WHERE state='deferred' AND reason='EMAIL_DIAGNOSTICS_REQUIRED' AND campaign_id IN (SELECT id FROM campaigns WHERE $1=ANY(asset_ids))`, asset); err != nil {
			return result, err
		}
		if _, err = tx.Exec(ctx, `UPDATE delivery_jobs SET next_attempt_at=now() WHERE state='queued' AND last_error='EMAIL_DIAGNOSTICS_REQUIRED' AND asset_id=$1`, asset); err != nil {
			return result, err
		}
	}
	if err = audit(ctx, tx, u, "email.assessed", asset, nil, result.State+"; source "+source); err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}
func (s *Store) EnsureCampaigns(ctx context.Context) error {
	tx, err := s.Begin(ctx, DemoWorkspace, false)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, statement := range []string{`INSERT INTO email_fixture_state(workspace_id,asset_id,scenario) SELECT workspace_id,id,CASE WHEN id='email-east' THEN 'broken' ELSE 'healthy' END FROM assets WHERE kind='email' ON CONFLICT DO NOTHING`} {
		if _, err = tx.Exec(ctx, statement); err != nil {
			return err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	// Initialization fills absent fixture evidence only; it never replaces history.
	for _, asset := range []string{"email-north", "email-east"} {
		tx, e := s.BeginRead(ctx, DemoWorkspace)
		if e != nil {
			return e
		}
		var exists bool
		e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM email_assessments WHERE asset_id=$1)", asset).Scan(&exists)
		tx.Rollback(ctx)
		if e != nil {
			return e
		}
		if !exists {
			if _, e = s.assessEmail(ctx, systemUser, asset, "mail", "fixture"); e != nil {
				return e
			}
		}
	}
	return nil
}
func (s *Server) emailList(w http.ResponseWriter, r *http.Request) {
	tx, err := s.Store.BeginRead(r.Context(), user(r).WorkspaceID)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	assets, err := listAssets(r.Context(), tx)
	if err != nil {
		fail(w, err)
		return
	}
	items := []map[string]any{}
	for _, a := range assets {
		if a.Kind != "email" {
			continue
		}
		rows, e := tx.Query(r.Context(), "SELECT id,assessment,checked_at FROM email_assessments WHERE asset_id=$1 ORDER BY checked_at DESC,id DESC LIMIT 10", a.ID)
		if e != nil {
			fail(w, e)
			return
		}
		history := []map[string]any{}
		for rows.Next() {
			var id string
			var raw []byte
			var at time.Time
			if e = rows.Scan(&id, &raw, &at); e != nil {
				rows.Close()
				fail(w, e)
				return
			}
			var assessment provider.EmailAssessment
			if e = json.Unmarshal(raw, &assessment); e != nil {
				rows.Close()
				fail(w, e)
				return
			}
			history = append(history, map[string]any{"id": id, "assessment": assessment, "checkedAt": at})
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			fail(w, e)
			return
		}
		var affected int
		var failures, delivered, total int
		e = tx.QueryRow(r.Context(), `SELECT (SELECT count(*) FROM campaigns WHERE $1=ANY(asset_ids) AND state IN ('active','paused')),(SELECT count(*) FROM delivery_jobs WHERE asset_id=$1 AND channel='email' AND created_at>now()-interval '7 days'),(SELECT count(*) FROM delivery_jobs WHERE asset_id=$1 AND channel='email' AND state='delivered' AND created_at>now()-interval '7 days'),(SELECT count(DISTINCT e.job_id) FROM delivery_events e JOIN delivery_jobs j ON j.workspace_id=e.workspace_id AND j.id=e.job_id WHERE j.asset_id=$1 AND e.provider_code IN ('email.bounced','email.complained') AND e.received_at>now()-interval '7 days')`, a.ID).Scan(&affected, &total, &delivered, &failures)
		if e != nil {
			fail(w, e)
			return
		}
		ready, e := emailCampaignReady(r.Context(), tx, a.ID)
		if e != nil {
			fail(w, e)
			return
		}
		target, enabled := s.Store.DNSTargets[a.ID]
		items = append(items, map[string]any{"asset": a, "history": history, "affectedCampaigns": affected, "readyForCampaigns": ready, "nativeEnabled": enabled, "nativeTarget": target, "metrics": map[string]any{"queued": total, "delivered": delivered, "adverseJobs": failures, "window": "last_7_days", "source": "Persisted simulated jobs and verified local provider events"}})
	}
	writeJSON(w, 200, map[string]any{"items": items, "observedAt": time.Now().UTC()})
}
func (s *Server) emailCheck(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Selector string `json:"selector"`
		Mode     string `json:"mode"`
	}
	if !decode(w, r, &in) {
		return
	}
	a, err := s.Store.assessEmail(r.Context(), user(r), r.PathValue("id"), in.Selector, in.Mode)
	if err != nil {
		var p Problem
		if errors.As(err, &p) {
			writeJSON(w, 422, p)
		} else {
			fail(w, err)
		}
		return
	}
	writeJSON(w, 200, map[string]any{"assessment": a})
}
func (s *Server) emailFixture(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Scenario string `json:"scenario"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Scenario != "healthy" && in.Scenario != "broken" && in.Scenario != "timeout" && in.Scenario != "conflicting" {
		writeJSON(w, 422, Problem{Code: "INVALID_SCENARIO", Message: "Choose a named synthetic DNS scenario."})
		return
	}
	tx, err := s.Store.Begin(r.Context(), user(r).WorkspaceID, false)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if err = campaignLock(r.Context(), tx, user(r).WorkspaceID); err != nil {
		fail(w, err)
		return
	}
	a, err := assetByID(r.Context(), tx, r.PathValue("id"), true)
	if err != nil {
		fail(w, err)
		return
	}
	if a.Kind != "email" {
		writeJSON(w, 422, Problem{Code: "EMAIL_REQUIRED", Message: "Choose an email asset."})
		return
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO email_fixture_state(workspace_id,asset_id,scenario) VALUES($1,$2,$3) ON CONFLICT(workspace_id,asset_id) DO UPDATE SET scenario=excluded.scenario`, user(r).WorkspaceID, a.ID, in.Scenario)

	if err == nil {
		err = audit(r.Context(), tx, user(r), "email.fixture_changed", a.ID, nil, "Synthetic DNS scenario: "+in.Scenario+". Run diagnostics to record new evidence.")
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"scenario": in.Scenario, "requiresCheck": true})
}

func (s *Server) messageFixture(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Scenario string `json:"scenario"`
	}
	if !decode(w, r, &in) {
		return
	}
	a, err := provider.MessageAuthFixture(in.Scenario)
	if err != nil {
		writeJSON(w, 422, Problem{Code: "INVALID_MESSAGE_FIXTURE", Message: err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"authentication": a})
}
