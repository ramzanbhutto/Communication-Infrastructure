package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBlueBubblesContract(t *testing.T) {
	ctx := context.Background()
	lab, e := NewIMessageLab()
	if e != nil {
		t.Fatal(e)
	}
	defer lab.Close()
	bridge, _ := lab.Client()
	in := Request{ID: "imessage-contract-1", Channel: "imessage", To: ChatGUID("+12025550102"), Body: "Synthetic property details"}
	receipt, e := bridge.Submit(ctx, in)
	if e != nil || receipt.ID == "" {
		t.Fatalf("submit: %+v %v", receipt, e)
	}
	m, e := bridge.Message(ctx, receipt.ID)
	if e != nil || m.Delivered == nil || m.Read != nil || m.Text != in.Body {
		t.Fatalf("receipt: %+v %v", m, e)
	}
	if e = lab.Inbound("Please send the details."); e != nil {
		t.Fatal(e)
	}
	history, e := bridge.History(ctx, in.To)
	if e != nil || len(history) != 2 {
		t.Fatalf("history: %+v %v", history, e)
	}
	lab.MarkRead()
	m, e = bridge.Message(ctx, receipt.ID)
	if e != nil || m.Read == nil {
		t.Fatal("read evidence absent")
	}
	bad, _ := NewBlueBubbles(lab.Origin, "wrong-password")
	if _, e = bad.Chat(ctx, in.To); e == nil {
		t.Fatal("unauthenticated bridge read accepted")
	}
	if e = lab.ResetAfter(func(context.Context) error { return errors.New("commit failed") }, ctx); e == nil {
		t.Fatal("reset failure hidden")
	}
	if _, e = bridge.Message(ctx, receipt.ID); e != nil {
		t.Fatal("failed database reset discarded provider record")
	}
	if e = lab.ResetAfter(func(context.Context) error { return nil }, ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = bridge.Message(ctx, receipt.ID); e == nil {
		t.Fatal("successful lab reset retained old resource")
	}
}
func TestBlueBubblesNoFallbackAndConservativeErrors(t *testing.T) {
	for _, test := range []struct {
		name, service string
		status        int
		ambiguous     bool
	}{
		{"SMS chat rejected", "SMS", 200, false},
		{"ambiguous server failure", "iMessage", 503, true},
		{"missing resource identity", "iMessage", 200, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			sends := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("password") != "synthetic-secret" {
					t.Error("missing authentication")
				}
				if r.Method == "GET" {
					json.NewEncoder(w).Encode(map[string]any{"status": 200, "data": BridgeChat{GUID: ChatGUID("+12025550102"), Service: test.service}})
					return
				}
				sends++
				var body map[string]string
				json.NewDecoder(r.Body).Decode(&body)
				if body["method"] != "apple-script" || body["tempGuid"] != "job-1" || body["chatGuid"] != ChatGUID("+12025550102") {
					t.Error("wire contract mismatch")
				}
				w.WriteHeader(test.status)
				json.NewEncoder(w).Encode(map[string]any{"status": test.status, "data": map[string]any{}})
			}))
			defer server.Close()
			bridge, _ := NewBlueBubbles(server.URL, "synthetic-secret")
			_, e := bridge.Submit(context.Background(), Request{ID: "job-1", Channel: "imessage", To: ChatGUID("+12025550102"), Body: "test"})
			var failure *Failure
			if !errors.As(e, &failure) || failure.Ambiguous != test.ambiguous || strings.Contains(e.Error(), "synthetic-secret") {
				t.Fatalf("wrong classification: %v", e)
			}
			if test.service == "SMS" && sends != 0 || test.service == "iMessage" && sends != 1 {
				t.Fatal("fallback or unexpected retry")
			}
		})
	}
	for _, origin := range []string{"http://example.com", "https://secret@example.com", "https://example.com/path", "https://example.com?password=x"} {
		if _, e := NewBlueBubbles(origin, "test"); e == nil {
			t.Fatalf("unsafe origin accepted: %s", origin)
		}
	}
}
