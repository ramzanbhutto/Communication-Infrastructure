package ops

import (
	"communication-infrastructure/internal/provider"
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestCampaignWindows(t *testing.T) {
	for _, test := range []struct{ at, zone, want string }{{"2026-03-06T23:00:00Z", "America/New_York", "2026-03-09T12:00:00Z"}, {"2026-07-06T11:30:00Z", "America/Phoenix", "2026-07-06T15:00:00Z"}, {"2026-10-07T13:00:00Z", "UTC", "2026-10-07T13:00:00Z"}} {
		at, _ := time.Parse(time.RFC3339, test.at)
		got := campaignWindow(at, test.zone, 8, 18, true)
		if got.Format(time.RFC3339) != test.want {
			t.Fatalf("%s: %v", test.zone, got)
		}
	}
}
func TestCampaignWorkflow(t *testing.T) {
	s := integrationStore(t)
	ctx := context.Background()
	if _, err := s.resetDemo(ctx, operator, true); err != nil {
		t.Fatal(err)
	}
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
	t.Cleanup(func() { s.resetDemo(ctx, operator, true) })
	if err = s.EnsureCampaigns(ctx); err != nil {
		t.Fatal(err)
	}
	save := func(asset string) Campaign {
		t.Helper()
		r, e := s.SaveCampaign(ctx, operator, "", CampaignInput{Name: "Requested property updates", AssetIDs: []string{asset}, Timezone: "UTC", StartHour: 0, EndHour: 24, Steps: []CampaignStep{{Channel: "email", Subject: "Requested details", Body: "Hello {name}. Your synthetic property details are ready."}, {Channel: "email", Subject: "Following up", Body: "Would you like additional synthetic details?", DelaySeconds: 600}}})
		if e != nil || r.Status != 201 {
			t.Fatalf("save: %+v %v", r, e)
		}
		return r.Body.(map[string]any)["campaign"].(Campaign)
	}
	activate := func(c Campaign) Campaign {
		t.Helper()
		r, e := s.CampaignAction(ctx, operator, c.ID, "activate", c.Version)
		if e != nil || r.Status != 200 {
			t.Fatalf("activate: %+v %v", r, e)
		}
		return r.Body.(map[string]any)["campaign"].(Campaign)
	}
	run := func() {
		t.Helper()
		if e := s.campaignStep(ctx); e != nil {
			t.Fatal(e)
		}
	}
	mutate := func(query string, args ...any) {
		t.Helper()
		tx, e := s.Begin(ctx, DemoWorkspace, false)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback(ctx)
		if _, e = tx.Exec(ctx, query, args...); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(ctx); e != nil {
			t.Fatal(e)
		}
	}
	c := activate(save("email-north"))
	r, e := s.EnrollCampaign(ctx, operator, c.ID, EnrollInput{ContactIDs: []string{"c102", "c103", "c104"}, Timezone: "UTC"})
	if e != nil || r.Status != 200 {
		t.Fatalf("enroll: %+v %v", r, e)
	}
	if count(t, s, "SELECT count(*) FROM campaign_enrollments") != 1 {
		t.Fatal("suppressed contacts enrolled")
	}
	// Concurrent schedulers must reserve only one step/job.
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.campaignStep(ctx) }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if count(t, s, "SELECT count(*) FROM campaign_executions") != 1 || count(t, s, "SELECT count(*) FROM delivery_jobs") != 1 {
		t.Fatal("concurrent scheduling duplicated jobs")
	}
	// A transaction rollback must not leave an orphan delivery job.
	tx, e := s.Begin(ctx, DemoWorkspace, false)
	if e != nil {
		t.Fatal(e)
	}
	q, e := s.queueDeliveryInTx(ctx, operator, deliveryFixture("rollback-campaign", "email", "success"), tx)
	if e != nil || q.Status != 202 {
		t.Fatalf("transactional queue: %+v %v", q, e)
	}
	tx.Rollback(ctx)
	if count(t, s, "SELECT count(*) FROM delivery_jobs") != 1 {
		t.Fatal("job escaped rollback")
	}
	if e = s.deliveryStep(ctx); e != nil {
		t.Fatal(e)
	}
	mutate("UPDATE campaign_enrollments SET next_run_at=now()")
	run()
	if count(t, s, "SELECT count(*) FROM campaign_enrollments WHERE step=1 AND state='scheduled'") != 1 {
		t.Fatal("confirmed delivery did not advance")
	}
	if e = s.persistInbound(ctx, "campaign-reply", "email", "avery@example.test", "operator@north.example.test", "Re: details", "Please send the address."); e != nil {
		t.Fatal(e)
	}
	mutate("UPDATE campaign_enrollments SET next_run_at=now()")
	run()
	if count(t, s, "SELECT count(*) FROM campaign_enrollments WHERE state='replied'") != 1 || count(t, s, "SELECT count(*) FROM delivery_jobs") != 1 {
		t.Fatal("reply failed to stop follow-up")
	}
	// Activated sequences cannot be edited, including by a stale operator.
	r, e = s.SaveCampaign(ctx, operator, c.ID, CampaignInput{Name: "Edited campaign", AssetIDs: c.AssetIDs, Steps: c.Steps, Timezone: "UTC", StartHour: 0, EndHour: 24, ExpectedVersion: c.Version})
	if e != nil || r.Status != 409 {
		t.Fatal("active sequence edited")
	}
	viewer := operator
	viewer.Role = "viewer"
	r, e = s.CampaignAction(ctx, viewer, c.ID, "pause", c.Version)
	if e != nil || r.Status != 403 {
		t.Fatal("reviewer mutation permitted")
	}
	other := operator
	other.WorkspaceID = "inaccessible-workspace"
	r, e = s.CampaignAction(ctx, other, c.ID, "pause", c.Version)
	if e != nil || r.Status != 404 {
		t.Fatal("cross-tenant campaign visible")
	}
	// Broken diagnostics block activation; a timeout is retained as unavailable.
	east := save("email-east")
	r, e = s.CampaignAction(ctx, operator, east.ID, "activate", east.Version)
	if e != nil || r.Status != 422 {
		t.Fatal("broken domain activated")
	}
	mutate("UPDATE email_fixture_state SET scenario='healthy' WHERE asset_id='email-east'")
	if _, e = s.assessEmail(ctx, operator, "email-east", "mail", "fixture"); e != nil {
		t.Fatal(e)
	}
	east = activate(east)
	if _, e = s.EnrollCampaign(ctx, operator, east.ID, EnrollInput{ContactIDs: []string{"c102"}, Timezone: "UTC"}); e != nil {
		t.Fatal(e)
	}
	mutate("UPDATE email_fixture_state SET scenario='timeout' WHERE asset_id='email-east'")
	a, e := s.assessEmail(ctx, operator, "email-east", "mail", "fixture")
	if e != nil || a.State != "unavailable" {
		t.Fatal("timeout not recorded", a, e)
	}
	run()
	if count(t, s, "SELECT count(*) FROM campaign_enrollments WHERE campaign_id=$1 AND reason='EMAIL_DIAGNOSTICS_REQUIRED'", east.ID) != 1 {
		t.Fatal("domain failure did not defer")
	}
	mutate("UPDATE email_fixture_state SET scenario='healthy' WHERE asset_id='email-east'")
	if _, e = s.assessEmail(ctx, operator, "email-east", "mail", "fixture"); e != nil {
		t.Fatal(e)
	}
	run()
	if count(t, s, "SELECT count(*) FROM campaign_executions") != 2 {
		t.Fatal("recheck did not resume deferred enrollment")
	}
	// Opt-out after queuing must suppress the campaign job in the same transaction.
	if e = s.persistInbound(ctx, "campaign-stop", "sms", "+12025550102", "+12025550121", "", "STOP"); e != nil {
		t.Fatal(e)
	}
	if count(t, s, "SELECT count(*) FROM delivery_jobs WHERE state='queued'") != 0 {
		t.Fatal("queued job survived STOP")
	}
}

func TestCampaignControlsAndAmbiguity(t *testing.T) {
	s := integrationStore(t)
	ctx := context.Background()
	if _, e := s.resetDemo(ctx, operator, true); e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer((&Server{Store: s, Origin: "http://localhost:5174"}).Handler())
	defer server.Close()
	lab, e := provider.NewLab(server.URL)
	if e != nil {
		t.Fatal(e)
	}
	defer lab.Close()
	s.ProviderLab = lab
	s.CallbackOrigin = lab.CallbackOrigin
	s.Gateway, e = lab.Gateway()
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.resetDemo(ctx, operator, true) })
	change := func(q string, args ...any) {
		t.Helper()
		tx, e := s.Begin(ctx, DemoWorkspace, false)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback(ctx)
		if _, e = tx.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(ctx); e != nil {
			t.Fatal(e)
		}
	}
	create := func() Campaign {
		t.Helper()
		if _, e = s.resetDemo(ctx, operator, true); e != nil {
			t.Fatal(e)
		}
		if e = s.EnsureCampaigns(ctx); e != nil {
			t.Fatal(e)
		}
		r, e := s.SaveCampaign(ctx, operator, "", CampaignInput{Name: "Recovery campaign", AssetIDs: []string{"email-north"}, Timezone: "UTC", EndHour: 24, Steps: []CampaignStep{{Channel: "email", Subject: "Requested details", Body: "Synthetic property details."}, {Channel: "email", Subject: "Follow-up", Body: "Synthetic follow-up.", DelaySeconds: 600}}})
		if e != nil {
			t.Fatal(e)
		}
		c := r.Body.(map[string]any)["campaign"].(Campaign)
		r, e = s.CampaignAction(ctx, operator, c.ID, "activate", c.Version)
		if e != nil || r.Status != 200 {
			t.Fatalf("activate %+v %v", r, e)
		}
		c = r.Body.(map[string]any)["campaign"].(Campaign)
		if _, e = s.EnrollCampaign(ctx, operator, c.ID, EnrollInput{ContactIDs: []string{"c102"}, Timezone: "UTC"}); e != nil {
			t.Fatal(e)
		}
		if e = s.campaignStep(ctx); e != nil {
			t.Fatal(e)
		}
		return c
	}
	t.Run("pause and resume queued work", func(t *testing.T) {
		c := create()
		r, e := s.CampaignAction(ctx, operator, c.ID, "pause", c.Version)
		if e != nil || r.Status != 200 {
			t.Fatal(r, e)
		}
		c = r.Body.(map[string]any)["campaign"].(Campaign)
		if e = s.deliveryStep(ctx); e != nil {
			t.Fatal(e)
		}
		if count(t, s, "SELECT count(*) FROM delivery_jobs WHERE state='queued' AND attempts=0") != 1 {
			t.Fatal("pause submitted queued job")
		}
		r, e = s.CampaignAction(ctx, operator, c.ID, "resume", c.Version)
		if e != nil || r.Status != 200 {
			t.Fatal(r, e)
		}
		if e = s.deliveryStep(ctx); e != nil {
			t.Fatal(e)
		}
		if count(t, s, "SELECT count(*) FROM delivery_jobs WHERE state='delivered'") != 1 {
			t.Fatal("resume failed")
		}
	})
	t.Run("uncertain send stays held without resend", func(t *testing.T) {
		create()
		change("UPDATE delivery_jobs SET scenario='ambiguous'")
		if e = s.deliveryStep(ctx); e != nil {
			t.Fatal(e)
		}
		for i := 0; i < 4; i++ {
			change("UPDATE campaign_enrollments SET next_run_at=now()")
			if e = s.campaignStep(ctx); e != nil {
				t.Fatal(e)
			}
			if e = s.deliveryStep(ctx); e != nil {
				t.Fatal(e)
			}
		}
		if count(t, s, "SELECT count(*) FROM campaign_enrollments WHERE state='held' AND reason='AMBIGUOUS_DELIVERY'") != 1 || count(t, s, "SELECT count(*) FROM delivery_jobs WHERE attempts=1") != 1 {
			t.Fatal("ambiguity triggered resend or progression")
		}
	})
	t.Run("cancel atomically suppresses unsent work", func(t *testing.T) {
		c := create()
		r, e := s.CampaignAction(ctx, operator, c.ID, "cancel", c.Version)
		if e != nil || r.Status != 200 {
			t.Fatal(r, e)
		}
		if e = s.deliveryStep(ctx); e != nil {
			t.Fatal(e)
		}
		if count(t, s, "SELECT count(*) FROM delivery_jobs WHERE state='suppressed' AND attempts=0") != 1 || count(t, s, "SELECT count(*) FROM campaign_enrollments WHERE state='stopped'") != 1 || count(t, s, "SELECT count(*) FROM audit WHERE action='campaign.cancel'") != 1 {
			t.Fatal("cancellation was inconsistent")
		}
	})
	t.Run("shared quotas cover manual jobs too", func(t *testing.T) {
		create()
		change("UPDATE sending_limits SET daily_cap=1 WHERE asset_id='email-north'")
		r, e := s.QueueDelivery(ctx, operator, deliveryFixture("shared-cap-check", "email", "success"))
		if e != nil || r.Status != 422 || r.Body.(Problem).Code != "ASSET_DAILY_CAP" {
			t.Fatal("manual send bypassed campaign capacity", r, e)
		}
	})
}
