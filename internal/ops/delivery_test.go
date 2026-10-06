package ops

import (
	"communication-infrastructure/internal/provider"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func deliveryFixture(key, channel, scenario string) DeliveryInput {
	asset := "line-cedar"
	if channel == "email" {
		asset = "email-north"
	}
	return DeliveryInput{Channel: channel, ContactID: "c102", AssetID: asset, RequestKey: key, Subject: "Property details", Body: "Synthetic property details for your requested review.", Scenario: scenario}
}
func TestDeliveryInfrastructure(t *testing.T) {
	s := integrationStore(t)
	ctx := context.Background()
	server := httptest.NewServer((&Server{Store: s, Origin: "http://localhost:5174"}).Handler())
	defer server.Close()
	lab, err := provider.NewLab(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer lab.Close()
	s.ProviderLab = lab
	s.CallbackOrigin = lab.CallbackOrigin
	s.Gateway, err = lab.Gateway()
	if err != nil {
		t.Fatal(err)
	}
	reset := func() {
		tx, e := s.Begin(ctx, DemoWorkspace, true)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(ctx, "DELETE FROM delivery_events; DELETE FROM delivery_jobs"); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(ctx); e != nil {
			t.Fatal(e)
		}
		resetFixture(t, s)
		if e = s.EnsureInfrastructure(ctx); e != nil {
			t.Fatal(e)
		}
	}
	t.Cleanup(func() {
		tx, e := s.Begin(ctx, DemoWorkspace, true)
		if e == nil {
			tx.Exec(ctx, "DELETE FROM delivery_events;DELETE FROM delivery_jobs")
			tx.Commit(ctx)
		}
	})
	queue := func(in DeliveryInput) DeliveryJob {
		t.Helper()
		r, e := s.QueueDelivery(ctx, operator, in)
		if e != nil || r.Status != 202 {
			t.Fatalf("queue: %+v %v", r, e)
		}
		return r.Body.(map[string]any)["job"].(DeliveryJob)
	}
	load := func(id string) DeliveryJob {
		t.Helper()
		tx, e := s.BeginRead(ctx, DemoWorkspace)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback(ctx)
		j, e := scanJob(tx.QueryRow(ctx, "SELECT "+jobColumns+" FROM delivery_jobs WHERE id=$1", id))
		if e != nil {
			t.Fatal(e)
		}
		return j
	}
	step := func() {
		t.Helper()
		if e := s.deliveryStep(ctx); e != nil {
			t.Fatal(e)
		}
	}
	t.Run("signed SMS delivery and duplicate event", func(t *testing.T) {
		reset()
		in := deliveryFixture("delivery-success-1", "sms", "success")
		j := queue(in)
		r, e := s.QueueDelivery(ctx, operator, in)
		if e != nil || r.Status != 200 {
			t.Fatal("retry did not replay")
		}
		in.Body = "different"
		r, e = s.QueueDelivery(ctx, operator, in)
		if e != nil || r.Status != 409 {
			t.Fatal("conflicting replay accepted")
		}
		step()
		j = load(j.ID)
		if j.State != "delivered" || j.Attempts != 1 || j.ProviderID == nil {
			t.Fatalf("not confirmed: %+v", j)
		}
		if e = lab.Emit(ctx, j.ID); e != nil {
			t.Fatal(e)
		}
		if count(t, s, "SELECT count(*) FROM delivery_events") != 1 {
			t.Fatal("callback replay duplicated event")
		}
		if e = s.applyDeliveryEvent(ctx, "late-status", j.ID, *j.ProviderID, "sent", "", time.Now()); e != nil {
			t.Fatal(e)
		}
		if load(j.ID).State != "delivered" {
			t.Fatal("late callback regressed terminal state")
		}
	})
	t.Run("definite rejection retries once", func(t *testing.T) {
		reset()
		j := queue(deliveryFixture("delivery-throttle-1", "sms", "throttle"))
		step()
		j = load(j.ID)
		if j.State != "queued" || j.Attempts != 1 || j.LastError == nil {
			t.Fatalf("wrong retry state: %+v", j)
		}
		tx, _ := s.Begin(ctx, DemoWorkspace, false)
		tx.Exec(ctx, "UPDATE delivery_jobs SET next_attempt_at=now() WHERE id=$1", j.ID)
		tx.Commit(ctx)
		step()
		j = load(j.ID)
		if j.State != "delivered" || j.Attempts != 2 {
			t.Fatalf("retry failed: %+v", j)
		}
	})
	t.Run("ambiguous call blocks both dialers until verified", func(t *testing.T) {
		reset()
		j := queue(deliveryFixture("delivery-ambiguous-1", "call", "ambiguous"))
		step()
		if load(j.ID).State != "unknown" {
			t.Fatal("ambiguity hidden")
		}
		step()
		if load(j.ID).Attempts != 1 {
			t.Fatal("ambiguous submission retried")
		}
		r, e := s.Outreach(ctx, operator, "call", outreachFixture("c101", "old-dialer-busy-1"))
		if e != nil || r.Status != 409 {
			t.Fatal("old dialer bypassed reservation")
		}
		r, e = s.QueueDelivery(ctx, operator, deliveryFixture("new-dialer-busy-1", "call", "success"))
		if e != nil || r.Status != 409 {
			t.Fatal("new dialer bypassed reservation")
		}
		r, e = s.ResetDemo(ctx, operator)
		if e != nil || r.Status != 409 {
			t.Fatal("reset erased ambiguous state")
		}
		if e = lab.Emit(ctx, j.ID); e != nil {
			t.Fatal(e)
		}
		if load(j.ID).State != "completed" {
			t.Fatal("verified callback did not release reservation")
		}
	})
	t.Run("concurrent call requests reserve one slot", func(t *testing.T) {
		reset()
		var wg sync.WaitGroup
		results := make(chan int, 2)
		failures := make(chan error, 2)
		for _, key := range []string{"concurrent-call-1", "concurrent-call-2"} {
			wg.Add(1)
			go func(key string) {
				defer wg.Done()
				r, e := s.QueueDelivery(ctx, operator, deliveryFixture(key, "call", "success"))
				results <- r.Status
				failures <- e
			}(key)
		}
		wg.Wait()
		close(results)
		close(failures)
		allowed, blocked := 0, 0
		for status := range results {
			if status == 202 {
				allowed++
			} else if status == 409 {
				blocked++
			}
		}
		for e := range failures {
			if e != nil {
				t.Fatal(e)
			}
		}
		if allowed != 1 || blocked != 1 {
			t.Fatalf("concurrency: %d %d", allowed, blocked)
		}
	})
	t.Run("STOP suppresses queued SMS and applies once", func(t *testing.T) {
		reset()
		j := queue(deliveryFixture("delivery-stop-1", "sms", "success"))
		if e := lab.Inbound(ctx, "sms", "+12025550102", "+12025550121", "STOP"); e != nil {
			t.Fatal(e)
		}
		step()
		j = load(j.ID)
		if j.State != "suppressed" || j.Attempts != 0 {
			t.Fatalf("queued outreach escaped opt-out: %+v", j)
		}
		if count(t, s, "SELECT count(*) FROM infrastructure_inbox") != 1 {
			t.Fatal("reply not persisted")
		}
	})
	t.Run("bounce suppresses future outreach", func(t *testing.T) {
		reset()
		j := queue(deliveryFixture("delivery-bounce-1", "email", "bounce"))
		step()
		if load(j.ID).State != "failed" {
			t.Fatal("bounce not recorded")
		}
		r, e := s.QueueDelivery(ctx, operator, deliveryFixture("after-bounce-1", "sms", "success"))
		if e != nil || r.Status != 422 {
			t.Fatal("bounce suppression bypassed")
		}
	})
	t.Run("provision and trunk persist provider identifiers", func(t *testing.T) {
		reset()
		j := queue(DeliveryInput{Channel: "provision", Subject: "Lab line", Destination: "+12025550188", RequestKey: "provision-line-1"})
		step()
		if load(j.ID).State != "completed" || count(t, s, "SELECT count(*) FROM assets WHERE address='+12025550188'") != 1 {
			t.Fatal("provisioning did not persist asset")
		}
		j = queue(DeliveryInput{Channel: "trunk", Subject: "Lab secure trunk", RequestKey: "provision-trunk-1"})
		step()
		if load(j.ID).State != "completed" || load(j.ID).ProviderID == nil {
			t.Fatal("trunk record missing")
		}
	})
	t.Run("HTTP signatures roles and masked overview", func(t *testing.T) {
		reset()
		form := url.Values{"AccountSid": {lab.Account}, "Body": {"STOP"}, "MessageSid": {"SM-fake"}, "From": {"+12025550102"}, "To": {"+12025550121"}}
		req, _ := http.NewRequest("POST", server.URL+"/hooks/twilio/inbound", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Twilio-Signature", "forged")
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		res.Body.Close()
		if res.StatusCode != 403 || count(t, s, "SELECT count(*) FROM infrastructure_inbox") != 0 {
			t.Fatal("forged callback changed state")
		}
		viewer := operator
		viewer.Role = "viewer"
		r, e := s.QueueDelivery(ctx, viewer, deliveryFixture("viewer-job-1", "sms", "success"))
		if e != nil || r.Status != 403 {
			t.Fatal("viewer submitted outreach")
		}
		j := queue(deliveryFixture("cross-tenant-job-1", "sms", "success"))
		tx, e := s.BeginRead(ctx, "other-workspace")
		if e != nil {
			t.Fatal(e)
		}
		var n int
		e = tx.QueryRow(ctx, "SELECT count(*) FROM delivery_jobs WHERE id=$1", j.ID).Scan(&n)
		tx.Rollback(ctx)
		if e != nil || n != 0 {
			t.Fatal("delivery job crossed tenant boundary")
		}
		encoded, _ := json.Marshal(publicJob(load(j.ID)))
		if strings.Contains(string(encoded), "+12025550102") {
			t.Fatal("overview leaks contact address")
		}
	})
	t.Run("SMS permission still requires consent and reply", func(t *testing.T) {
		reset()
		in := deliveryFixture("email-open-only-1", "sms", "success")
		in.ContactID = "c101"
		r, e := s.QueueDelivery(ctx, operator, in)
		if e != nil || r.Status != 422 || r.Body.(Problem).Code != "NO_SMS_CONSENT" {
			t.Fatal("email-open inferred SMS permission")
		}
	})
}
