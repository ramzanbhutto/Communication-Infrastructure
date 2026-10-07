package ops

import (
	"context"
	"github.com/jackc/pgx/v5"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func resultHTTP(w http.ResponseWriter, result Result, err error) {
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, result.Status, result.Body)
}
func (s *Server) campaignSave(w http.ResponseWriter, r *http.Request) {
	var in CampaignInput
	if !decode(w, r, &in) {
		return
	}
	v, e := s.Store.SaveCampaign(r.Context(), user(r), r.PathValue("id"), in)
	resultHTTP(w, v, e)
}
func (s *Server) campaignAction(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ExpectedVersion int64 `json:"expectedVersion"`
	}
	if !decode(w, r, &in) {
		return
	}
	v, e := s.Store.CampaignAction(r.Context(), user(r), r.PathValue("id"), r.PathValue("action"), in.ExpectedVersion)
	resultHTTP(w, v, e)
}
func (s *Server) campaignEnroll(w http.ResponseWriter, r *http.Request) {
	var in EnrollInput
	if !decode(w, r, &in) {
		return
	}
	v, e := s.Store.EnrollCampaign(r.Context(), user(r), r.PathValue("id"), in)
	resultHTTP(w, v, e)
}
func readCampaigns(ctx context.Context, tx pgx.Tx, search string, limit, offset int) ([]Campaign, int, error) {
	var total int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM campaigns WHERE name ILIKE $1", "%"+search+"%").Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := tx.Query(ctx, "SELECT "+campaignColumns+" FROM campaigns WHERE name ILIKE $1 ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3", "%"+search+"%", limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Campaign{}
	for rows.Next() {
		c, e := scanCampaign(rows)
		if e != nil {
			return nil, 0, e
		}
		out = append(out, c)
	}
	return out, total, rows.Err()
}
func (s *Server) campaignList(w http.ResponseWriter, r *http.Request) {
	tx, err := s.Store.BeginRead(r.Context(), user(r).WorkspaceID)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	if len(search) > 100 {
		writeJSON(w, 422, Problem{Code: "INVALID_SEARCH", Message: "Search is limited to 100 characters."})
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	if page > 10000 {
		page = 10000
	}
	items, total, err := readCampaigns(r.Context(), tx, search, 20, (page-1)*20)
	if err != nil {
		fail(w, err)
		return
	}
	assets, err := listAssets(r.Context(), tx)
	if err != nil {
		fail(w, err)
		return
	}
	summaries := map[string]map[string]int{}
	for _, c := range items {
		rows, e := tx.Query(r.Context(), "SELECT state,count(*) FROM campaign_enrollments WHERE campaign_id=$1 GROUP BY state", c.ID)
		if e != nil {
			fail(w, e)
			return
		}
		states := map[string]int{}
		for rows.Next() {
			var state string
			var n int
			if e = rows.Scan(&state, &n); e != nil {
				rows.Close()
				fail(w, e)
				return
			}
			states[state] = n
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			fail(w, e)
			return
		}
		summaries[c.ID] = states
	}
	writeJSON(w, 200, map[string]any{"items": items, "summaries": summaries, "assets": assets, "total": total, "page": page, "pageSize": 20, "observedAt": time.Now().UTC()})
}
func (s *Server) campaignDetail(w http.ResponseWriter, r *http.Request) {
	tx, err := s.Store.BeginRead(r.Context(), user(r).WorkspaceID)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	c, err := scanCampaign(tx.QueryRow(r.Context(), "SELECT "+campaignColumns+" FROM campaigns WHERE id=$1", r.PathValue("id")))
	if err != nil {
		fail(w, err)
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	if page > 10000 {
		page = 10000
	}
	var total int
	if err = tx.QueryRow(r.Context(), "SELECT count(*) FROM campaign_enrollments WHERE campaign_id=$1", c.ID).Scan(&total); err != nil {
		fail(w, err)
		return
	}
	rows, err := tx.Query(r.Context(), "SELECT id,contact_id,timezone,step,state,reason,next_run_at,enrolled_at,updated_at FROM campaign_enrollments WHERE campaign_id=$1 ORDER BY enrolled_at DESC,id DESC LIMIT 50 OFFSET $2", c.ID, (page-1)*50)
	if err != nil {
		fail(w, err)
		return
	}
	enrollments := []Enrollment{}
	ids := []string{}
	for rows.Next() {
		var e Enrollment
		if err = rows.Scan(&e.ID, &e.ContactID, &e.Timezone, &e.Step, &e.State, &e.Reason, &e.NextRunAt, &e.EnrolledAt, &e.UpdatedAt); err != nil {
			rows.Close()
			fail(w, err)
			return
		}
		enrollments = append(enrollments, e)
		ids = append(ids, e.ID)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		fail(w, err)
		return
	}
	executions := []map[string]any{}
	rows, err = tx.Query(r.Context(), `SELECT x.enrollment_id,x.step,x.job_id,j.state,j.asset_id,j.decision_id,j.created_at FROM campaign_executions x JOIN delivery_jobs j ON j.workspace_id=x.workspace_id AND j.id=x.job_id WHERE x.enrollment_id=ANY($1) ORDER BY x.created_at`, ids)
	if err != nil {
		fail(w, err)
		return
	}
	for rows.Next() {
		var eid, jid, state string
		var step int
		var asset, decision *string
		var at time.Time
		if err = rows.Scan(&eid, &step, &jid, &state, &asset, &decision, &at); err != nil {
			rows.Close()
			fail(w, err)
			return
		}
		executions = append(executions, map[string]any{"enrollmentId": eid, "step": step, "jobId": jid, "state": state, "assetId": asset, "decisionId": decision, "createdAt": at})
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		fail(w, err)
		return
	}
	rows, err = tx.Query(r.Context(), `SELECT id,actor_name,action,asset_id,record_id,reason,recorded_at FROM audit WHERE record_id=$1 OR record_id IN (SELECT id FROM campaign_enrollments WHERE campaign_id=$1) ORDER BY recorded_at DESC,id DESC LIMIT 30`, c.ID)
	if err != nil {
		fail(w, err)
		return
	}
	events := []Audit{}
	for rows.Next() {
		var e Audit
		if err = rows.Scan(&e.ID, &e.ActorName, &e.Action, &e.AssetID, &e.RecordID, &e.Reason, &e.RecordedAt); err != nil {
			rows.Close()
			fail(w, err)
			return
		}
		events = append(events, e)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"campaign": c, "enrollments": enrollments, "executions": executions, "audit": events, "total": total, "page": page, "pageSize": 50, "observedAt": time.Now().UTC()})
}
