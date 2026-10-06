package provider

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSignatures(t *testing.T) {
	form := url.Values{"From": {"+12025550101"}, "Body": {"STOP"}}
	canonical := "https://hooks.example.test/hooks/twilio/inbound"
	sig := TwilioSignature("secret", canonical, form)
	if !VerifyTwilio("secret", canonical, sig, form) {
		t.Fatal("valid signature rejected")
	}
	form.Set("Body", "send more")
	if VerifyTwilio("secret", canonical, sig, form) {
		t.Fatal("tampered body accepted")
	}
	secret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("a sufficiently long secret value"))
	body := []byte(`{"type":"email.delivered"}`)
	stamp := "1700000000"
	now := time.Unix(1700000000, 0)
	sig = SvixSignature(secret, "event-1", stamp, body)
	if !VerifySvix(secret, "event-1", stamp, sig, body, now) {
		t.Fatal("valid Svix signature rejected")
	}
	if VerifySvix(secret, "event-2", stamp, sig, body, now) || VerifySvix(secret, "event-1", stamp, sig, body, now.Add(6*time.Minute)) || VerifySvix(secret, "event-1", stamp, sig, []byte("altered"), now) {
		t.Fatal("replayed or altered event accepted")
	}
}
func TestTwilioOfficialSignatureVector(t *testing.T) {
	// Compatibility fixture from twilio/twilio-go client/request_validator_test.go.
	form := url.Values{"CallSid": {"CA1234567890ABCDE"}, "Caller": {"+14158675309"}, "Digits": {"1234"}, "From": {"+14158675309"}, "To": {"+18005551212"}, "ReasonConferenceEnded": {"test"}, "Reason": {"Participant"}}
	got := TwilioSignature("12345", "https://mycompany.com/myapp.php?foo=1&bar=2", form)
	if got != "vOEb5UThFn24KEfnOFLQY2AE5FY=" {
		t.Fatalf("official signature fixture: %s", got)
	}
}
func TestAdapterFailureClassification(t *testing.T) {
	for _, test := range []struct {
		status         int
		body           string
		retry, unknown bool
	}{{429, `{}`, true, false}, {503, `{}`, false, true}, {401, `{}`, false, false}, {200, `invalid`, false, true}, {200, `{}`, false, true}} {
		t.Run(http.StatusText(test.status)+test.body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(test.status); w.Write([]byte(test.body)) }))
			defer server.Close()
			adapter, err := NewResend("test-key", server.URL)
			if err != nil {
				t.Fatal(err)
			}
			_, err = adapter.Submit(context.Background(), Request{ID: "job1", Channel: "email"})
			var failure *Failure
			if !errors.As(err, &failure) || failure.Retryable != test.retry || failure.Ambiguous != test.unknown {
				t.Fatalf("wrong classification: %v", err)
			}
		})
	}
	if _, err := NewTwilio("account", "token", "operator", "https://untrusted.example.test"); err == nil {
		t.Fatal("external override permitted")
	}
}
func TestAdapterWireContracts(t *testing.T) {
	for _, channel := range []string{"sms", "call", "provision", "trunk", "email"} {
		t.Run(channel, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" {
					t.Error("wrong method")
				}
				if channel == "email" {
					if r.URL.Path != "/emails" || r.Header.Get("Idempotency-Key") != "job-1" || r.Header.Get("Authorization") != "Bearer email-secret" {
						t.Error("email contract mismatch")
					}
					w.Write([]byte(`{"id":"provider-email-1"}`))
					return
				}
				a, token, ok := r.BasicAuth()
				if !ok || a != "account" || token != "phone-secret" {
					t.Error("missing authentication")
				}
				r.ParseForm()
				if channel == "call" {
					if r.Form.Get("To") != "+12025550199" || !strings.Contains(r.Form.Get("Twiml"), "<Number>+12025550102</Number>") {
						t.Error("operator bridge contract mismatch")
					}
				}
				if channel == "trunk" && r.Form.Get("Secure") != "true" {
					t.Error("secure trunk flag absent")
				}
				w.Write([]byte(`{"sid":"provider-resource-1"}`))
			}))
			defer server.Close()
			var gateway Gateway
			var err error
			if channel == "email" {
				gateway, err = NewResend("email-secret", server.URL)
			} else {
				gateway, err = NewTwilio("account", "phone-secret", "+12025550199", server.URL)
			}
			if err != nil {
				t.Fatal(err)
			}
			out, err := gateway.Submit(context.Background(), Request{ID: "job-1", Channel: channel, From: "+12025550121", To: "+12025550102", Subject: "Test", Body: "Test body", Callback: "https://hooks.example.test/status"})
			if err != nil || out.ID == "" || out.Status != "accepted" {
				t.Fatalf("submission: %+v %v", out, err)
			}
		})
	}
}

type failedDNS struct{}

func (failedDNS) LookupTXT(context.Context, string) ([]string, error) {
	return nil, errors.New("offline")
}
func TestDNSUnknownIsNotMissing(t *testing.T) {
	out, err := InspectDNS(context.Background(), failedDNS{}, "example.test", "mail", "test")
	if err != nil || out.SPF.State != "unavailable" || out.DKIM.State != "unavailable" {
		t.Fatalf("failure disguised as missing: %+v %v", out, err)
	}
	out, err = InspectDNS(context.Background(), LabDNS{}, "north.example.test", "mail", "local_dns_fixture")
	if err != nil || out.DKIM.State != "present" {
		t.Fatal("lab inspection failed")
	}
	if _, err = InspectDNS(context.Background(), LabDNS{}, "example.test", "../secret", "test"); err == nil {
		t.Fatal("invalid selector accepted")
	}
}
