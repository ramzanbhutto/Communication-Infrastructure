package ops

import (
	"communication-infrastructure/internal/provider"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOptionalIMessage(t *testing.T) {
	s := integrationStore(t)
	ctx := context.Background()
	resetFixture(t, s)
	providers, e := provider.NewLab("http://127.0.0.1:2063")
	if e != nil {
		t.Fatal(e)
	}
	defer providers.Close()
	s.Gateway, e = providers.Gateway()
	if e != nil {
		t.Fatal(e)
	}
	in := DeliveryInput{Channel: "imessage", ContactID: "c102", AssetID: "bridge-imessage", Body: "Requested synthetic property details.", RequestKey: "imessage-integration-1"}
	result, e := s.QueueDelivery(ctx, operator, in)
	if e != nil || result.Status != 503 {
		t.Fatal("optional disabled channel was usable")
	}
	lab, e := provider.NewIMessageLab()
	if e != nil {
		t.Fatal(e)
	}
	defer lab.Close()
	s.IMessageLab = lab
	s.IMessageBridge, e = lab.Client()
	if e != nil {
		t.Fatal(e)
	}
	s.Gateway = provider.WithIMessage{Default: s.Gateway, Bridge: s.IMessageBridge}
	if e = s.EnsureIMessage(ctx); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		tx, e := s.Begin(ctx, DemoWorkspace, true)
		if e == nil {
			tx.Exec(ctx, "DELETE FROM delivery_events;DELETE FROM delivery_jobs")
			tx.Commit(ctx)
		}
		s.ResetDemo(ctx, operator)
	})
	t.Run("roles and channel-specific permission", func(t *testing.T) {
		viewer := operator
		viewer.Role = "viewer"
		result, e := s.QueueDelivery(ctx, viewer, in)
		if e != nil || result.Status != 403 {
			t.Fatal("reviewer sent iMessage")
		}
		noPermission := in
		noPermission.ContactID = "c101"
		noPermission.RequestKey = "imessage-no-permission"
		result, e = s.QueueDelivery(ctx, operator, noPermission)
		if e != nil || result.Status != 422 || result.Body.(Problem).Code != "NO_IMESSAGE_PERMISSION" {
			t.Fatalf("permission bypass: %+v %v", result, e)
		}
		if count(t, s, "SELECT count(*) FROM delivery_jobs") != 0 {
			t.Fatal("blocked request reached provider queue")
		}
		tx, err := s.Begin(ctx, DemoWorkspace, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, "DELETE FROM channel_permissions WHERE channel='imessage'"); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		result, e = s.QueueDelivery(ctx, operator, in)
		if e != nil || result.Status != 422 || result.Body.(Problem).Code != "NO_IMESSAGE_PERMISSION" {
			t.Fatal("existing SMS and email permission granted iMessage access")
		}
		if e = s.EnsureIMessage(ctx); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("durable delivery, read receipts and duplicate sync", func(t *testing.T) {
		result, e := s.QueueDelivery(ctx, operator, in)
		if e != nil || result.Status != 202 {
			t.Fatalf("queue: %+v %v", result, e)
		}
		if _, e = s.QueueDelivery(ctx, operator, in); e != nil {
			t.Fatal(e)
		}
		if e = s.deliveryStep(ctx); e != nil {
			t.Fatal(e)
		}
		if count(t, s, "SELECT count(*) FROM delivery_jobs WHERE channel='imessage' AND state='delivered' AND attempts=1") != 1 {
			t.Fatal("delivery not verified")
		}
		if count(t, s, "SELECT count(*) FROM delivery_events WHERE source='authenticated_local_bridge'") != 1 {
			t.Fatal("wrong receipt provenance")
		}
		lab.MarkRead()
		if e = s.syncIMessage(ctx, DemoWorkspace, "c102"); e != nil {
			t.Fatal(e)
		}
		if e = s.syncIMessage(ctx, DemoWorkspace, "c102"); e != nil {
			t.Fatal(e)
		}
		if count(t, s, "SELECT count(*) FROM delivery_events WHERE kind='read'") != 1 {
			t.Fatal("missing or duplicated read receipt")
		}
		if e = s.syncIMessage(ctx, "foreign-workspace", "c102"); e == nil {
			t.Fatal("cross-tenant bridge sync accepted")
		}
	})
	t.Run("STOP suppresses queued iMessage and other channels", func(t *testing.T) {
		queued := in
		queued.RequestKey = "imessage-stopped-1"
		result, e := s.QueueDelivery(ctx, operator, queued)
		if e != nil || result.Status != 202 {
			t.Fatalf("queue: %+v %v", result, e)
		}
		if e = lab.Inbound("STOP"); e != nil {
			t.Fatal(e)
		}
		if e = s.syncIMessage(ctx, DemoWorkspace, "c102"); e != nil {
			t.Fatal(e)
		}
		if e = s.deliveryStep(ctx); e != nil {
			t.Fatal(e)
		}
		if count(t, s, "SELECT count(*) FROM delivery_jobs WHERE state='suppressed' AND attempts=0") != 1 {
			t.Fatal("queued iMessage escaped STOP")
		}
		result, e = s.QueueDelivery(ctx, operator, deliveryFixture("imessage-stop-email", "email", "success"))
		if e != nil || result.Status != 422 || result.Body.(Problem).Code != "OPTED_OUT" {
			t.Fatal("STOP did not suppress email")
		}
		if count(t, s, "SELECT count(*) FROM infrastructure_inbox WHERE channel='imessage'") != 1 {
			t.Fatal("inbound reply not deduplicated")
		}
	})
	t.Run("reset isolates the optional provider history", func(t *testing.T) {
		result, e := s.ResetDemo(ctx, operator)
		if e != nil || result.Status != 200 {
			t.Fatalf("reset: %+v %v", result, e)
		}
		if e = s.syncIMessage(ctx, DemoWorkspace, "c102"); e != nil {
			t.Fatal(e)
		}
		if count(t, s, "SELECT count(*) FROM infrastructure_inbox WHERE channel='imessage'") != 0 {
			t.Fatal("old provider STOP reimported after reset")
		}
		if count(t, s, "SELECT count(*) FROM channel_permissions WHERE channel='imessage'") != 1 {
			t.Fatal("reset lost optional permission fixture")
		}
		stale := context.WithValue(ctx, imessageGenerationKey{}, int64(-1))
		if e = s.persistInboundSource(stale, "imessage:stale-reset", "imessage", "+12025550102", "demo-mac@example.test", "", "STOP", "authenticated_local_bridge"); e == nil {
			t.Fatal("stale bridge synchronization crossed reset boundary")
		}
		if count(t, s, "SELECT count(*) FROM contacts WHERE id='c102' AND opted_out_at IS NOT NULL") != 0 {
			t.Fatal("old STOP changed the new scenario")
		}
	})
	t.Run("compatibility explains existing and absent chats", func(t *testing.T) {
		handler := (&Server{Store: s}).imessageCompatibility
		for _, item := range []struct{ id, state string }{{"c102", "compatible"}, {"c101", "no_chat"}} {
			request := httptest.NewRequest("GET", "/api/v1/imessage/compatibility?contactId="+item.id, nil)
			request = request.WithContext(context.WithValue(request.Context(), userKey{}, operator))
			recorder := httptest.NewRecorder()
			handler(recorder, request)
			var body map[string]any
			json.Unmarshal(recorder.Body.Bytes(), &body)
			if recorder.Code != http.StatusOK || body["state"] != item.state {
				t.Fatalf("compatibility: %d %s", recorder.Code, recorder.Body)
			}
		}
	})
	t.Run("disabling the option preserves queued messages", func(t *testing.T) {
		queued := in
		queued.RequestKey = "imessage-disabled-pending-1"
		result, err := s.QueueDelivery(ctx, operator, queued)
		if err != nil || result.Status != 202 {
			t.Fatalf("queue: %+v %v", result, err)
		}
		bridge := s.IMessageBridge
		s.IMessageBridge = nil
		if err = s.deliveryStep(ctx); err != nil {
			t.Fatal(err)
		}
		if count(t, s, "SELECT count(*) FROM delivery_jobs WHERE channel='imessage' AND state='queued' AND attempts=0") != 1 {
			t.Fatal("disabled channel submitted or discarded pending work")
		}
		s.IMessageBridge = bridge
		if err = s.deliveryStep(ctx); err != nil {
			t.Fatal(err)
		}
		if count(t, s, "SELECT count(*) FROM delivery_jobs WHERE channel='imessage' AND state='delivered'") != 1 {
			t.Fatal("reenabled channel did not resume pending work")
		}
	})
}
