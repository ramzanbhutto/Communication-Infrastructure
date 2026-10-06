package ops

import (
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Server struct {
	Store  *Store
	Origin string
	WebDir string
}
type userKey struct{}
type nonceKey struct{}

func user(r *http.Request) User { return r.Context().Value(userKey{}).(User) }
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, 404, Problem{Code: "NOT_FOUND", Message: "The record was not found."})
		return
	}
	writeJSON(w, 503, Problem{Code: "DEPENDENCY_UNAVAILABLE", Message: "The operation could not be confirmed. Refresh to verify its recorded state before trying again."})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	mediaType, _, mediaError := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaError != nil || mediaType != "application/json" {
		writeJSON(w, 415, Problem{Code: "JSON_REQUIRED", Message: "Use an application/json request body."})
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeJSON(w, 422, Problem{Code: "INVALID_INPUT", Message: "The request body is invalid or contains unsupported fields."})
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeJSON(w, 422, Problem{Code: "INVALID_INPUT", Message: "Send one JSON object."})
		return false
	}
	return true
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("POST /hooks/twilio/status", s.providerHook)
	mux.HandleFunc("POST /hooks/twilio/inbound", s.providerHook)
	mux.HandleFunc("POST /hooks/resend", s.providerHook)
	mux.HandleFunc("GET /api/v1/infrastructure", s.require(s.infrastructure, false))
	mux.HandleFunc("GET /api/v1/imessage/compatibility", s.require(s.imessageCompatibility, false))
	mux.HandleFunc("POST /api/v1/imessage/sync", s.require(s.imessageSync, true))
	mux.HandleFunc("POST /api/v1/imessage/scenarios", s.require(s.imessageScenario, true))
	mux.HandleFunc("POST /api/v1/infrastructure/sip/probe", s.require(s.sipProbe, true))
	mux.HandleFunc("POST /api/v1/infrastructure/jobs", s.require(s.deliveryCreate, true))
	mux.HandleFunc("GET /api/v1/infrastructure/jobs/{id}", s.require(s.deliveryDetail, false))
	mux.HandleFunc("POST /api/v1/infrastructure/jobs/{id}/verify", s.require(s.deliveryVerify, true))
	mux.HandleFunc("POST /api/v1/infrastructure/scenarios", s.require(s.infrastructureScenario, true))
	mux.HandleFunc("GET /api/v1/infrastructure/inbox/{id}", s.require(s.inboxDetail, false))
	mux.HandleFunc("POST /api/v1/infrastructure/inbox/{id}/resolve", s.require(s.inboxResolve, true))
	mux.HandleFunc("POST /api/v1/infrastructure/dns/{id}", s.require(s.dnsInspect, true))
	mux.HandleFunc("POST /api/v1/infrastructure/ramps/{id}", s.require(s.rampUpdate, true))
	mux.HandleFunc("POST /api/v1/session", s.login)
	mux.HandleFunc("GET /api/v1/session", s.require(s.session, false))
	mux.HandleFunc("DELETE /api/v1/session", s.require(s.logout, false))
	mux.HandleFunc("GET /api/v1/desk", s.require(s.desk, false))
	mux.HandleFunc("GET /api/v1/assets", s.require(s.assets, false))
	mux.HandleFunc("GET /api/v1/assets/{id}", s.require(s.asset, false))
	mux.HandleFunc("POST /api/v1/assets/{id}/{action}", s.require(s.assetAction, true))
	mux.HandleFunc("GET /api/v1/contacts", s.require(s.contacts, false))
	mux.HandleFunc("GET /api/v1/contacts/{id}", s.require(s.contact, false))
	mux.HandleFunc("GET /api/v1/decisions", s.require(s.decisions, false))
	mux.HandleFunc("GET /api/v1/decisions/export", s.require(s.export, false))
	mux.HandleFunc("GET /api/v1/decisions/{id}", s.require(s.decision, false))
	mux.HandleFunc("POST /api/v1/scrub", s.require(s.scrub, true))
	mux.HandleFunc("POST /api/v1/dial", s.require(s.dial, true))
	mux.HandleFunc("POST /api/v1/sms", s.require(s.sms, true))
	mux.HandleFunc("GET /api/v1/conversations", s.require(s.conversations, false))
	mux.HandleFunc("POST /api/v1/demo/reset", s.require(s.reset, true))
	mux.HandleFunc("POST /api/v1/demo/replies", s.require(s.repliesControl, true))
	mux.HandleFunc("GET /api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 404, Problem{Code: "NOT_FOUND", Message: "Unknown API route."})
	})
	mux.HandleFunc("POST /api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 404, Problem{Code: "NOT_FOUND", Message: "Unknown API route."})
	})
	mux.HandleFunc("GET /", s.static)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, e := net.SplitHostPort(r.Host); e == nil {
			host = h
		}
		if host != "localhost" && host != "127.0.0.1" && host != "::1" {
			writeJSON(w, 403, Problem{Code: "HOST_REJECTED", Message: "Open this isolated demo through localhost."})
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		var randomNonce [16]byte
		if _, err := rand.Read(randomNonce[:]); err != nil {
			fail(w, err)
			return
		}
		nonce := hex.EncodeToString(randomNonce[:])
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'nonce-"+nonce+"'; style-src-attr 'none'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("Permissions-Policy", "microphone=(), camera=(), geolocation=()")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		isProviderHook := r.URL.Path == "/hooks/twilio/status" || r.URL.Path == "/hooks/twilio/inbound" || r.URL.Path == "/hooks/resend"
		if r.Method != "GET" && r.Method != "HEAD" && !isProviderHook {
			origin := r.Header.Get("Origin")
			if origin == "" || (origin != s.Origin && origin != "http://"+r.Host) {
				writeJSON(w, 403, Problem{Code: "ORIGIN_REJECTED", Message: "This action must come from the local application."})
				return
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		mux.ServeHTTP(w, r.WithContext(context.WithValue(ctx, nonceKey{}, nonce)))
	})
}
func (s *Server) require(next http.HandlerFunc, operator bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("ops_session")
		if err != nil {
			writeJSON(w, 401, Problem{Code: "UNAUTHENTICATED", Message: "Choose a demo account to continue."})
			return
		}
		var u User
		err = s.Store.DB.QueryRow(r.Context(), "SELECT id,workspace_id,name,role FROM resolve_session($1)", Hash(c.Value)).Scan(&u.ID, &u.WorkspaceID, &u.Name, &u.Role)
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, 401, Problem{Code: "UNAUTHENTICATED", Message: "Your demo session expired. Choose an account again."})
			return
		}
		if err != nil {
			fail(w, err)
			return
		}
		if operator && u.Role != "operator" {
			writeJSON(w, 403, Problem{Code: "FORBIDDEN", Message: "The reviewer can investigate and export. An operator is required to change records."})
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userKey{}, u)))
	}
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		UserID string `json:"userId"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.UserID != "demo-operator" && in.UserID != "demo-viewer" {
		writeJSON(w, 422, Problem{Code: "INVALID_ACCOUNT", Message: "Choose a published synthetic demo account."})
		return
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		fail(w, err)
		return
	}
	token := hex.EncodeToString(b[:])
	var ok bool
	if err := s.Store.DB.QueryRow(r.Context(), "SELECT demo_login($1,$2)", in.UserID, Hash(token)).Scan(&ok); err != nil {
		fail(w, err)
		return
	}
	if !ok {
		writeJSON(w, 403, Problem{Code: "FORBIDDEN", Message: "This demo account is unavailable."})
		return
	}
	if previous, err := r.Cookie("ops_session"); err == nil {
		if _, err = s.Store.DB.Exec(r.Context(), "SELECT revoke_session($1)", Hash(previous.Value)); err != nil {
			fail(w, err)
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: "ops_session", Value: token, Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: 28800})
	writeJSON(w, 200, map[string]any{"demo": true})
}
func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"user": user(r), "demo": true})
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	c, _ := r.Cookie("ops_session")
	if _, err := s.Store.DB.Exec(r.Context(), "SELECT revoke_session($1)", Hash(c.Value)); err != nil {
		fail(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "ops_session", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, 200, map[string]bool{"signedOut": true})
}
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := deadline(r.Context())
	defer cancel()
	pg := s.Store.DB.Ping(ctx) == nil
	redis := s.Store.Redis.Ping(ctx).Err() == nil
	status := 200
	if !pg || !redis {
		status = 503
	}
	writeJSON(w, status, map[string]any{"api": "available", "postgres": pg, "redis": redis, "demo": true, "checkedAt": time.Now().UTC()})
}
func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(s.WebDir, filepath.Clean("/"+r.URL.Path))
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		path = filepath.Join(s.WebDir, "index.html")
	}
	if _, err = os.Stat(path); err != nil {
		http.Error(w, "Build web/ or start the Vite development server.", 503)
		return
	}
	if filepath.Ext(path) == ".html" {
		content, err := os.ReadFile(path)
		if err != nil {
			fail(w, err)
			return
		}
		nonce, _ := r.Context().Value(nonceKey{}).(string)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, strings.ReplaceAll(string(content), "__OPS_NONCE__", nonce))
		return
	}
	http.ServeFile(w, r, path)
}
func (s *Server) assets(w http.ResponseWriter, r *http.Request) {
	tx, err := s.Store.BeginRead(r.Context(), user(r).WorkspaceID)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	items, err := listAssets(r.Context(), tx)
	if err != nil {
		fail(w, err)
		return
	}
	kind, status, search := r.URL.Query().Get("kind"), r.URL.Query().Get("status"), strings.ToLower(r.URL.Query().Get("search"))
	if kind != "" && kind != "phone" && kind != "email" || status != "" && status != "active" && status != "quarantined" {
		writeJSON(w, 422, Problem{Code: "INVALID_FILTER", Message: "Use a supported asset kind and status."})
		return
	}
	out := []Asset{}
	for _, a := range items {
		if (kind == "" || a.Kind == kind) && (status == "" || a.Status == status) && (strings.Contains(strings.ToLower(a.Name+" "+a.Address), search)) {
			out = append(out, a)
		}
	}
	writeJSON(w, 200, map[string]any{"items": out, "total": len(out), "observedAt": time.Now().UTC()})
}
func (s *Server) asset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tx, err := s.Store.BeginRead(ctx, user(r).WorkspaceID)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback(ctx)
	id := r.PathValue("id")
	a, err := assetByID(ctx, tx, id, false)
	if err != nil {
		fail(w, err)
		return
	}
	rows, err := tx.Query(ctx, "SELECT id,attempts,filtered,spam_label,observed_at,source FROM observations WHERE asset_id=$1 ORDER BY observed_at,id LIMIT 100", id)
	if err != nil {
		fail(w, err)
		return
	}
	samples := []Sample{}
	for rows.Next() {
		var sample Sample
		if err = rows.Scan(&sample.ID, &sample.Attempts, &sample.Filtered, &sample.SpamLabel, &sample.ObservedAt, &sample.Source); err != nil {
			rows.Close()
			fail(w, err)
			return
		}
		samples = append(samples, sample)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		fail(w, err)
		return
	}
	events, err := auditList(ctx, tx, id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"asset": a, "samples": samples, "audit": events, "restoreProblems": RestoreProblems(a, a.Sample), "observedAt": time.Now().UTC()})
}
func (s *Server) assetAction(w http.ResponseWriter, r *http.Request) {
	var in AssetAction
	if !decode(w, r, &in) {
		return
	}
	action := r.PathValue("action")
	if action != "quarantine" && action != "restore" && action != "recovery" {
		writeJSON(w, 404, Problem{Code: "NOT_FOUND", Message: "Unknown asset action."})
		return
	}
	result, err := s.Store.AssetAction(r.Context(), user(r), r.PathValue("id"), action, in)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, result.Status, result.Body)
}
func (s *Server) contacts(w http.ResponseWriter, r *http.Request) {
	tx, err := s.Store.BeginRead(r.Context(), user(r).WorkspaceID)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	rows, err := tx.Query(r.Context(), "SELECT id,phone,email FROM contacts ORDER BY id LIMIT 100")
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]string{}
	for rows.Next() {
		var id, phone, email string
		if err = rows.Scan(&id, &phone, &email); err != nil {
			fail(w, err)
			return
		}
		out = append(out, map[string]string{"id": id, "label": "Contact · " + id, "phone": MaskPhone(phone), "email": MaskEmail(email)})
	}
	if err = rows.Err(); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": out})
}
func (s *Server) contact(w http.ResponseWriter, r *http.Request) {
	tx, err := s.Store.BeginRead(r.Context(), user(r).WorkspaceID)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	c, err := contactByID(r.Context(), tx, r.PathValue("id"), false)
	if err != nil {
		fail(w, err)
		return
	}
	a, err := listAssets(r.Context(), tx)
	if err != nil {
		fail(w, err)
		return
	}
	eligibility := map[string]any{}
	for _, asset := range a {
		if asset.Kind == "phone" {
			eligibility[asset.ID] = map[string]string{"sms": Gate(c, asset, "sms"), "call": Gate(c, asset, "call")}
		}
	}
	writeJSON(w, 200, map[string]any{"contact": c, "eligibility": eligibility, "source": "Current records. Preview is not a recorded outreach decision."})
}
func parseDecisionQuery(r *http.Request) (channel, outcome, search string, page, size int, err error) {
	q := r.URL.Query()
	channel, outcome, search = q.Get("channel"), q.Get("outcome"), strings.TrimSpace(q.Get("search"))
	page, size = 1, 20
	if channel != "" && channel != "sms" && channel != "call" && channel != "email" && channel != "imessage" || outcome != "" && outcome != "allowed" && outcome != "blocked" && outcome != "deferred" || len(search) > 100 {
		err = Problem{Code: "INVALID_FILTER", Message: "Choose a supported decision filter."}
		return
	}
	if p := q.Get("page"); p != "" {
		page, err = strconv.Atoi(p)
		if err != nil {
			return
		}
	}
	if p := q.Get("pageSize"); p != "" {
		size, err = strconv.Atoi(p)
		if err != nil {
			return
		}
	}
	if page < 1 || page > 10000 || size < 1 || size > 100 {
		err = Problem{Code: "INVALID_PAGE", Message: "Use page >= 1 and a page size between 1 and 100."}
	}
	return
}
func (s *Server) decisions(w http.ResponseWriter, r *http.Request) {
	channel, outcome, search, page, size, err := parseDecisionQuery(r)
	if err != nil {
		writeJSON(w, 422, Problem{Code: "INVALID_FILTER", Message: "Check the channel, outcome, search and pagination filters."})
		return
	}
	tx, err := s.Store.BeginRead(r.Context(), user(r).WorkspaceID)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	items, total, err := decisions(r.Context(), tx, channel, outcome, search, size, (page-1)*size)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total, "page": page, "pageSize": size, "window": "last_24_hours", "observedAt": time.Now().UTC()})
}
func (s *Server) decision(w http.ResponseWriter, r *http.Request) {
	tx, err := s.Store.BeginRead(r.Context(), user(r).WorkspaceID)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var d Decision
	var raw []byte
	err = tx.QueryRow(r.Context(), `SELECT d.id,d.contact_id,d.asset_id,a.name,d.channel,d.outcome,d.reason,d.evidence,d.recorded_at FROM decisions d LEFT JOIN assets a ON a.workspace_id=d.workspace_id AND a.id=d.asset_id WHERE d.id=$1`, r.PathValue("id")).Scan(&d.ID, &d.ContactID, &d.AssetID, &d.AssetName, &d.Channel, &d.Outcome, &d.Reason, &raw, &d.RecordedAt)
	if err != nil {
		fail(w, err)
		return
	}
	if err = json.Unmarshal(raw, &d.Evidence); err != nil {
		fail(w, err)
		return
	}
	d.ContactLabel = "Contact · " + d.ContactID
	writeJSON(w, 200, map[string]any{"decision": d, "explanation": reasonText[d.Reason], "explanationSource": "Derived from the recorded reason code, not a new policy check."})
}
func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	channel, outcome, search, _, _, err := parseDecisionQuery(r)
	if err != nil {
		writeJSON(w, 422, Problem{Code: "INVALID_FILTER", Message: "Check the export filters."})
		return
	}
	tx, err := s.Store.BeginRead(r.Context(), user(r).WorkspaceID)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	items, total, err := decisions(r.Context(), tx, channel, outcome, search, 501, 0)
	if err != nil {
		fail(w, err)
		return
	}
	if total > 500 {
		writeJSON(w, 422, Problem{Code: "EXPORT_TOO_LARGE", Message: "Narrow the filters to 500 decisions or fewer. No partial export was created."})
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="decisions-last-24h-masked.csv"`)
	w.Header().Set("X-Export-Scope", "all-filtered-last-24-hours-masked")
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"id", "recorded_at_utc", "channel", "outcome", "reason", "contact_id", "masked_phone", "masked_email", "asset", "scope"})
	for _, d := range items {
		asset := ""
		if d.AssetName != nil {
			asset = *d.AssetName
		}
		row := []string{d.ID, d.RecordedAt.UTC().Format(time.RFC3339Nano), d.Channel, d.Outcome, d.Reason, d.ContactID, stringEvidence(d.Evidence, "maskedPhone"), stringEvidence(d.Evidence, "maskedEmail"), asset, "all filtered decisions in last 24 hours; masked contacts"}
		for i := range row {
			row[i] = CSVSafe(row[i])
		}
		_ = writer.Write(row)
	}
	writer.Flush()
}
func stringEvidence(v map[string]any, key string) string      { s, _ := v[key].(string); return s }
func (s *Server) dial(w http.ResponseWriter, r *http.Request) { s.outreach(w, r, "call") }
func (s *Server) sms(w http.ResponseWriter, r *http.Request)  { s.outreach(w, r, "sms") }
func (s *Server) outreach(w http.ResponseWriter, r *http.Request, channel string) {
	var in OutreachInput
	if !decode(w, r, &in) {
		return
	}
	result, err := s.Store.Outreach(r.Context(), user(r), channel, in)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, result.Status, result.Body)
}
func (s *Server) reset(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Confirmation       string `json:"confirmation"`
		DiscardUnconfirmed bool   `json:"discardUnconfirmed"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Confirmation != "RESET DEMO" {
		writeJSON(w, 422, Problem{Code: "CONFIRMATION_REQUIRED", Message: "Type RESET DEMO to reset only the synthetic workspace."})
		return
	}
	result, err := s.Store.resetDemo(r.Context(), user(r), in.DiscardUnconfirmed)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, result.Status, result.Body)
}
func (s *Server) repliesControl(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Paused bool `json:"paused"`
	}
	if !decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	u := user(r)
	tx, err := s.Store.Begin(ctx, u.WorkspaceID, false)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback(ctx)
	var old bool
	if err = tx.QueryRow(ctx, "SELECT replies_paused FROM demo_state FOR UPDATE").Scan(&old); err != nil {
		fail(w, err)
		return
	}
	if old != in.Paused {
		if _, err = tx.Exec(ctx, "UPDATE demo_state SET replies_paused=$1", in.Paused); err != nil {
			fail(w, err)
			return
		}
		if err = audit(ctx, tx, u, "reply.consumer_setting", nil, nil, map[bool]string{true: "Paused the synthetic reply consumer.", false: "Resumed the synthetic reply consumer."}[in.Paused]); err != nil {
			fail(w, err)
			return
		}
	}
	if err = tx.Commit(ctx); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"paused": in.Paused})
}
func (s *Server) scrub(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ContactIDs []string `json:"contactIds"`
		LineID     string   `json:"lineId"`
		Channel    string   `json:"channel"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.ContactIDs) < 1 || len(in.ContactIDs) > 50 || (in.Channel != "call" && in.Channel != "sms") {
		writeJSON(w, 422, Problem{Code: "INVALID_INPUT", Message: "Choose 1 to 50 contacts and either call or SMS."})
		return
	}
	ctx := r.Context()
	u := user(r)
	tx, err := s.Store.Begin(ctx, u.WorkspaceID, false)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback(ctx)
	a, err := assetByID(ctx, tx, in.LineID, false)
	if err != nil {
		fail(w, err)
		return
	}
	out := []map[string]string{}
	for _, id := range in.ContactIDs {
		c, e := contactByID(ctx, tx, id, false)
		if e != nil {
			fail(w, e)
			return
		}
		reason := Gate(c, a, in.Channel)
		did, e := recordDecision(ctx, tx, u, c, a, in.Channel, reason)
		if e != nil {
			fail(w, e)
			return
		}
		out = append(out, map[string]string{"contactId": id, "reason": reason, "decisionId": did})
	}
	if err = tx.Commit(ctx); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": out, "sent": false})
}
