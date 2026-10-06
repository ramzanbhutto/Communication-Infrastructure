package ops

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func integrationStore(t *testing.T) *Store {
	t.Helper()
	runtime := os.Getenv("TEST_DATABASE_URL")
	if runtime == "" {
		t.Skip("integration tests require TEST_DATABASE_URL for covent_ops_test")
	}
	u, err := url.Parse(runtime)
	if err != nil || u.Path != "/covent_ops_test" || u.Hostname() != "127.0.0.1" {
		t.Fatal("integration tests require the isolated loopback covent_ops_test database")
	}
	admin := os.Getenv("TEST_MIGRATION_DATABASE_URL")
	au, err := url.Parse(admin)
	if err != nil || au.Host != u.Host || au.Path != u.Path {
		t.Fatal("test migration URL must target the same isolated database")
	}
	ctx := context.Background()
	if err = Migrate(ctx, admin, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	db, err := pgxpool.New(ctx, runtime)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	options, err := redis.ParseURL(os.Getenv("TEST_REDIS_URL"))
	if err != nil {
		t.Fatal(err)
	}
	if options.Addr != "127.0.0.1:56390" {
		t.Fatal("integration Redis must use isolated port 56390")
	}
	options.MaxRetries = 0
	options.DialTimeout = 100 * time.Millisecond
	options.ReadTimeout = 100 * time.Millisecond
	client := redis.NewClient(options)
	t.Cleanup(func() { client.Close() })
	s := &Store{DB: db, Redis: client}
	if err = s.EnsureDemo(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

var operator = User{ID: "demo-operator", Name: "Demo operator", WorkspaceID: DemoWorkspace, Role: "operator"}

func resetFixture(t *testing.T, s *Store) {
	t.Helper()
	result, err := s.ResetDemo(context.Background(), operator)
	if err != nil || result.Status != 200 {
		t.Fatalf("reset: %+v %v", result, err)
	}
}
func count(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	ctx := context.Background()
	tx, err := s.Begin(ctx, DemoWorkspace, false)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var n int
	if err = tx.QueryRow(ctx, query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func assetFixture(t *testing.T, s *Store, id string) Asset {
	t.Helper()
	ctx := context.Background()
	tx, err := s.Begin(ctx, DemoWorkspace, false)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	a, err := assetByID(ctx, tx, id, false)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func outreachFixture(contact, key string) OutreachInput {
	return OutreachInput{ContactID: contact, LineID: "line-cedar", Message: "Synthetic property details are ready.", RequestKey: key}
}

func TestIntegration(t *testing.T) {
	s := integrationStore(t)
	ctx := context.Background()
	t.Run("quarantine restore prerequisites and atomic audit", func(t *testing.T) {
		resetFixture(t, s)
		a := assetFixture(t, s, "line-delta")
		in := AssetAction{Reason: "Provider filter rate is deteriorating.", ExpectedVersion: a.Version}
		result, err := s.AssetAction(ctx, operator, a.ID, "quarantine", in)
		if err != nil || result.Status != 200 {
			t.Fatalf("quarantine: %v %+v", err, result)
		}
		a = assetFixture(t, s, a.ID)
		if a.Status != "quarantined" || a.QuarantinedAt == nil {
			t.Fatal("state not committed")
		}
		result, err = s.AssetAction(ctx, operator, a.ID, "quarantine", in)
		if err != nil || result.Status != 200 {
			t.Fatal("repeated quarantine failed")
		}
		if n := count(t, s, "SELECT count(*) FROM audit WHERE action='asset.quarantine'"); n != 1 {
			t.Fatalf("duplicate audit %d", n)
		}
		result, err = s.AssetAction(ctx, operator, a.ID, "restore", AssetAction{Reason: "Review restoration.", ExpectedVersion: a.Version})
		if err != nil || result.Status != 409 {
			t.Fatalf("stale health accepted: %v %+v", err, result)
		}
		if count(t, s, "SELECT count(*) FROM audit WHERE action='asset.restore_denied'") != 1 {
			t.Fatal("rejection not recorded")
		}
		result, err = s.AssetAction(ctx, operator, a.ID, "recovery", AssetAction{Reason: "New clean simulated sample.", ExpectedVersion: a.Version})
		if err != nil || result.Status != 200 {
			t.Fatalf("recovery: %v %+v", err, result)
		}
		a = assetFixture(t, s, a.ID)
		result, err = s.AssetAction(ctx, operator, a.ID, "restore", AssetAction{Reason: "Clean post-quarantine sample verified.", ExpectedVersion: a.Version})
		if err != nil || result.Status != 200 {
			t.Fatalf("restore: %v %+v", err, result)
		}
		if a = assetFixture(t, s, a.ID); a.Status != "active" || a.QuarantinedAt != nil {
			t.Fatal("restore state incorrect")
		}
		if count(t, s, "SELECT count(*) FROM observations WHERE asset_id=$1", a.ID) != 6 {
			t.Fatal("history was discarded")
		}
	})
	t.Run("stale state and audit rollback", func(t *testing.T) {
		resetFixture(t, s)
		a := assetFixture(t, s, "line-delta")
		result, err := s.AssetAction(ctx, operator, a.ID, "quarantine", AssetAction{Reason: "Review provider observation.", ExpectedVersion: a.Version - 1})
		if err != nil || result.Status != 409 {
			t.Fatal("stale version accepted")
		}
		bad := operator
		bad.ID = "" // A database constraint makes the audit insertion fail.
		admin, err := pgx.Connect(ctx, os.Getenv("TEST_MIGRATION_DATABASE_URL"))
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close(ctx)
		if _, err = admin.Exec(ctx, "ALTER TABLE audit ADD CONSTRAINT test_actor_nonempty CHECK(length(actor_id)>0)"); err != nil {
			t.Fatal(err)
		}
		defer admin.Exec(ctx, "ALTER TABLE audit DROP CONSTRAINT test_actor_nonempty")
		_, err = s.AssetAction(ctx, bad, a.ID, "quarantine", AssetAction{Reason: "Force a transaction failure.", ExpectedVersion: a.Version})
		if err == nil {
			t.Fatal("audit failure not returned")
		}
		if current := assetFixture(t, s, a.ID); current.Status != "active" || current.Version != a.Version {
			t.Fatal("state escaped rolled-back audit transaction")
		}
	})
	t.Run("SMS email-open block DNC suppression and deduplication", func(t *testing.T) {
		resetFixture(t, s)
		in := outreachFixture("c101", "sms-email-open-1")
		result, err := s.Outreach(ctx, operator, "sms", in)
		if err != nil || result.Status != 422 {
			t.Fatalf("missing consent accepted: %+v %v", result, err)
		}
		problem := result.Body.(Problem)
		if problem.Code != "NO_SMS_CONSENT" || problem.DecisionID == "" {
			t.Fatal("missing recorded reason")
		}
		if count(t, s, "SELECT count(*) FROM messages") != 0 {
			t.Fatal("message delivered despite missing consent")
		}
		if _, err = s.Outreach(ctx, operator, "sms", in); err != nil {
			t.Fatal(err)
		}
		if count(t, s, "SELECT count(*) FROM decisions WHERE contact_id='c101'") != 2 {
			t.Fatal("blocked request replay created a new decision")
		}
		in = outreachFixture("c102", "sms-consented-1")
		result, err = s.Outreach(ctx, operator, "sms", in)
		if err != nil || result.Status != 201 {
			t.Fatalf("eligible SMS: %+v %v", result, err)
		}
		if _, err = s.Outreach(ctx, operator, "sms", in); err != nil {
			t.Fatal(err)
		}
		if count(t, s, "SELECT count(*) FROM messages") != 1 {
			t.Fatal("replay duplicated delivery")
		}
		in.Message = "Changed body"
		result, err = s.Outreach(ctx, operator, "sms", in)
		if err != nil || result.Status != 409 {
			t.Fatal("conflicting key accepted")
		}
		for _, channel := range []string{"sms", "call"} {
			result, err = s.Outreach(ctx, operator, channel, outreachFixture("c103", "dnc-block-"+channel))
			if err != nil || result.Status != 422 || result.Body.(Problem).Code != "DNC_SUPPRESSED" {
				t.Fatal("DNC gate bypassed")
			}
		}
	})
	t.Run("single call survives concurrent requests", func(t *testing.T) {
		resetFixture(t, s)
		start := make(chan struct{})
		statuses := make(chan int, 2)
		failures := make(chan error, 2)
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				<-start
				result, err := s.Outreach(ctx, operator, "call", outreachFixture(fmt.Sprintf("c10%d", index+1), fmt.Sprintf("call-race-%d", index)))
				statuses <- result.Status
				failures <- err
			}(i)
		}
		close(start)
		wg.Wait()
		close(statuses)
		close(failures)
		for err := range failures {
			if err != nil {
				t.Fatal(err)
			}
		}
		counts := map[int]int{}
		for status := range statuses {
			counts[status]++
		}
		if counts[201] != 1 || counts[409] != 1 {
			t.Fatalf("concurrent statuses: %v", counts)
		}
		if count(t, s, "SELECT count(*) FROM calls WHERE status='active'") != 1 {
			t.Fatal("single-call invariant failed")
		}
		tx, err := s.Begin(ctx, DemoWorkspace, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, "UPDATE calls SET finish_at=now()-interval '1 second' WHERE status='active'"); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err = s.finishCalls(ctx); err != nil {
			t.Fatal(err)
		}
		if count(t, s, "SELECT count(*) FROM calls WHERE status='completed'") != 1 {
			t.Fatal("outcome not persisted")
		}
	})
	t.Run("touch cap cannot be bypassed", func(t *testing.T) {
		resetFixture(t, s)
		for i := 0; i < 3; i++ {
			result, err := s.Outreach(ctx, operator, "sms", outreachFixture("c102", fmt.Sprintf("touch-limit-%d", i)))
			if err != nil || result.Status != 201 {
				t.Fatalf("touch %d: %+v %v", i, result, err)
			}
		}
		result, err := s.Outreach(ctx, operator, "sms", outreachFixture("c102", "touch-limit-final"))
		if err != nil || result.Status != 422 || result.Body.(Problem).Code != "TOUCH_LIMIT" {
			t.Fatal("touch cap bypassed")
		}
	})
	t.Run("Redis persistence replay and inbound opt-out", func(t *testing.T) {
		resetFixture(t, s)
		if err := s.replyStep(ctx, "test-consumer"); err != nil {
			t.Fatal(err)
		}
		tx, err := s.Begin(ctx, DemoWorkspace, false)
		if err != nil {
			t.Fatal(err)
		}
		var generation int64
		if err = tx.QueryRow(ctx, "UPDATE demo_state SET replies_paused=false RETURNING generation").Scan(&generation); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if q := s.Queue(ctx, generation, true, 0); q.Pending == nil || *q.Pending != 3 {
			t.Fatalf("wrong real backlog %+v", q)
		}
		if err = s.replyStep(ctx, "test-consumer"); err != nil {
			t.Fatal(err)
		}
		if count(t, s, "SELECT count(*) FROM replies") != 4 {
			t.Fatal("replies not persisted")
		}
		tx, err = s.Begin(ctx, DemoWorkspace, false)
		if err != nil {
			t.Fatal(err)
		}
		c, err := contactByID(ctx, tx, "c105", false)
		tx.Rollback(ctx)
		if err != nil || c.OptedOutAt == nil {
			t.Fatal("inbound STOP did not suppress contact")
		}
		result, err := s.Outreach(ctx, operator, "call", outreachFixture("c105", "optout-call-1"))
		if err != nil || result.Body.(Problem).Code != "OPTED_OUT" {
			t.Fatal("opt-out not enforced")
		}
		// Re-publish an already committed event to model a crash before acknowledgement.
		tx, err = s.Begin(ctx, DemoWorkspace, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, "UPDATE outbox SET published_at=NULL WHERE id=$1", fmt.Sprintf("reply-g%d-0", generation)); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err = s.replyStep(ctx, "test-consumer"); err != nil {
			t.Fatal(err)
		}
		if count(t, s, "SELECT count(*) FROM replies") != 4 {
			t.Fatal("replay duplicated reply")
		}
	})
	t.Run("row security isolates workspace even without query predicates", func(t *testing.T) {
		admin, err := pgx.Connect(ctx, os.Getenv("TEST_MIGRATION_DATABASE_URL"))
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close(ctx)
		if _, err = admin.Exec(ctx, `INSERT INTO workspaces(id,name) VALUES('test-other','Other synthetic tenant') ON CONFLICT DO NOTHING;
 INSERT INTO assets(workspace_id,id,kind,name,address,status) VALUES('test-other','foreign-asset','phone','Secret other asset','+12025550199','active') ON CONFLICT DO NOTHING`); err != nil {
			t.Fatal(err)
		}
		tx, err := s.Begin(ctx, DemoWorkspace, false)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		var n int
		if err = tx.QueryRow(ctx, "SELECT count(*) FROM assets WHERE id='foreign-asset'").Scan(&n); err != nil || n != 0 {
			t.Fatal("foreign tenant data disclosed")
		}
		_, err = tx.Exec(ctx, "INSERT INTO assets(workspace_id,id,kind,name,address,status) VALUES('test-other','injected-asset','phone','Forbidden','x','active')")
		if err == nil {
			t.Fatal("foreign tenant write accepted")
		}
		var unsafe bool
		if err = s.DB.QueryRow(ctx, "SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname=current_user").Scan(&unsafe); err != nil || unsafe {
			t.Fatal("runtime role bypasses RLS")
		}
	})
	t.Run("dependency error never becomes zero queue", func(t *testing.T) {
		dead := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: 0, DialTimeout: 100 * time.Millisecond})
		defer dead.Close()
		unavailable := &Store{DB: s.DB, Redis: dead}
		q := unavailable.Queue(ctx, 1, false, 2)
		if q.State != "unavailable" || q.Pending != nil || q.Error == nil || q.Unpublished != 2 {
			t.Fatalf("dishonest queue state: %+v", q)
		}
	})
	t.Run("a new stream is initializing rather than a failed dependency", func(t *testing.T) {
		q := s.Queue(ctx, 9223372036854770000, true, 3)
		if q.State != "initializing" || q.Pending != nil || q.Unpublished != 3 || q.Error == nil {
			t.Fatalf("incorrect initialization state: %+v", q)
		}
	})
	t.Run("HTTP role origin masking export and payload boundaries", func(t *testing.T) {
		resetFixture(t, s)
		webDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte(`<!doctype html><html><head><meta name="csp-nonce" content="__OPS_NONCE__"></head><body>Demo</body></html>`), 0600); err != nil {
			t.Fatal(err)
		}
		app := httptest.NewServer((&Server{Store: s, Origin: "http://localhost:5173", WebDir: webDir}).Handler())
		defer app.Close()
		jar, _ := cookiejar.New(nil)
		client := &http.Client{Jar: jar}
		send := func(method, path, raw, origin string) (int, http.Header, []byte) {
			t.Helper()
			req, err := http.NewRequest(method, app.URL+path, strings.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			if raw != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			if origin != "" {
				req.Header.Set("Origin", origin)
			}
			res, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			body, _ := io.ReadAll(res.Body)
			return res.StatusCode, res.Header, body
		}
		if status, _, _ := send("GET", "/api/v1/desk", "", ""); status != 401 {
			t.Fatal("unauthenticated read accepted")
		}
		_, firstHeaders, firstHTML := send("GET", "/", "", "")
		_, secondHeaders, secondHTML := send("GET", "/", "", "")
		if firstHeaders.Get("Content-Security-Policy") == secondHeaders.Get("Content-Security-Policy") || bytes.Equal(firstHTML, secondHTML) || bytes.Contains(firstHTML, []byte("__OPS_NONCE__")) {
			t.Fatal("HTML style nonce not regenerated")
		}
		if strings.Contains(firstHeaders.Get("Content-Security-Policy"), "unsafe-inline") || firstHeaders.Get("Cache-Control") != "no-store" {
			t.Fatal("unsafe or cached document policy")
		}
		spoofed, _ := http.NewRequest("GET", app.URL+"/api/v1/desk", nil)
		spoofed.Host = "evil.example"
		spoofResponse, e := client.Do(spoofed)
		if e != nil {
			t.Fatal(e)
		}
		spoofResponse.Body.Close()
		if spoofResponse.StatusCode != 403 {
			t.Fatal("non-loopback host accepted")
		}
		if status, _, _ := send("POST", "/api/v1/session", `{"userId":"demo-operator"}`, "https://evil.example"); status != 403 {
			t.Fatal("cross-origin action accepted")
		}
		wrongType, _ := http.NewRequest("POST", app.URL+"/api/v1/session", strings.NewReader(`{"userId":"demo-operator"}`))
		wrongType.Header.Set("Origin", app.URL)
		wrongType.Header.Set("Content-Type", "application/jsonp")
		wrongTypeResponse, e := client.Do(wrongType)
		if e != nil {
			t.Fatal(e)
		}
		wrongTypeResponse.Body.Close()
		if wrongTypeResponse.StatusCode != 415 {
			t.Fatal("non-JSON media type accepted")
		}
		if status, _, _ := send("POST", "/api/v1/session", `{"userId":"demo-viewer","role":"operator"}`, app.URL); status != 422 {
			t.Fatal("unknown fields accepted")
		}
		status, headers, _ := send("POST", "/api/v1/session", `{"userId":"demo-viewer"}`, app.URL)
		if status != 200 || !strings.Contains(headers.Get("Set-Cookie"), "HttpOnly") || !strings.Contains(headers.Get("Set-Cookie"), "SameSite=Strict") {
			t.Fatal("session attributes incorrect")
		}
		if status, _, _ := send("POST", "/api/v1/sms", `{"contactId":"c102","lineId":"line-cedar","message":"Hello","requestKey":"role-bypass-1"}`, app.URL); status != 403 {
			t.Fatal("viewer mutation accepted")
		}
		status, _, body := send("GET", "/api/v1/contacts", "", "")
		if status != 200 || bytes.Contains(body, []byte("+12025550101")) || bytes.Contains(body, []byte("jordan@example.test")) {
			t.Fatal("contact list discloses raw values")
		}
		status, headers, body = send("GET", "/api/v1/decisions/export?outcome=blocked", "", "")
		if status != 200 || headers.Get("X-Export-Scope") != "all-filtered-last-24-hours-masked" {
			t.Fatal("export contract invalid")
		}
		records, err := csv.NewReader(bytes.NewReader(body)).ReadAll()
		if err != nil || len(records) != 4 || bytes.Contains(body, []byte("+12025550101")) {
			t.Fatalf("invalid masked export %v", err)
		}
		if status, _, _ := send("GET", "/api/v1/decisions?pageSize=100000", "", ""); status != 422 {
			t.Fatal("unbounded pagination accepted")
		}
		if status, _, _ := send("POST", "/api/v1/session", `{"userId":"demo-operator"}`, app.URL); status != 200 {
			t.Fatal("operator login failed")
		}
		if status, _, _ := send("POST", "/api/v1/sms", `{"message":"`+strings.Repeat("x", 17000)+`"}`, app.URL); status != 422 {
			t.Fatal("oversized request accepted")
		}
		// Serialized evidence is stable even when the current contact changes.
		status, _, body = send("GET", "/api/v1/decisions?search=NO_SMS_CONSENT", "", "")
		if status != 200 {
			t.Fatal("decision filter failed")
		}
		var list struct {
			Items []Decision `json:"items"`
		}
		if err = json.Unmarshal(body, &list); err != nil || len(list.Items) != 1 {
			t.Fatal("unexpected filtered decisions")
		}
		if list.Items[0].Evidence["smsConsentAt"] != nil {
			t.Fatal("missing consent invented")
		}
		tx, err := s.Begin(ctx, DemoWorkspace, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, "UPDATE contacts SET sms_consent_at=now(),consent_source='new synthetic consent' WHERE id='c101'"); err != nil {
			t.Fatal(err)
		}
		tx.Commit(ctx)
		status, _, body = send("GET", "/api/v1/decisions/"+list.Items[0].ID, "", "")
		var detail struct {
			Decision Decision `json:"decision"`
		}
		if err = json.Unmarshal(body, &detail); err != nil || status != 200 || detail.Decision.Evidence["smsConsentAt"] != nil {
			t.Fatal("historical evidence changed")
		}
	})
}
