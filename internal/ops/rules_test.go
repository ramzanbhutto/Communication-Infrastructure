package ops

import (
	"strings"
	"testing"
	"time"
)

func pointer[T any](v T) *T { return &v }
func TestGate(t *testing.T) {
	now := time.Now()
	a := Asset{Kind: "phone", Status: "active"}
	eligible := Contact{SMSConsentAt: &now, ConsentSource: pointer("synthetic opt-in"), WarmSignal: pointer("inbound_reply"), WarmAt: &now}
	tests := []struct {
		name          string
		c             Contact
		a             Asset
		channel, want string
	}{
		{"email open never grants SMS consent", Contact{EmailOpenedAt: &now}, a, "sms", "NO_SMS_CONSENT"},
		{"consent without reply is insufficient", Contact{SMSConsentAt: &now, ConsentSource: pointer("opt-in")}, a, "sms", "NO_WARM_REPLY"},
		{"explicit consent and inbound reply", eligible, a, "sms", "ELIGIBLE"},
		{"DNC is cross channel", Contact{DNC: true, SMSConsentAt: &now, ConsentSource: pointer("opt-in"), WarmSignal: pointer("inbound_reply"), WarmAt: &now}, a, "sms", "DNC_SUPPRESSED"},
		{"DNC also blocks calls", Contact{DNC: true}, a, "call", "DNC_SUPPRESSED"},
		{"opt out precedes permission", Contact{OptedOutAt: &now, SMSConsentAt: &now, ConsentSource: pointer("opt-in"), WarmSignal: pointer("inbound_reply"), WarmAt: &now}, a, "sms", "OPTED_OUT"},
		{"quarantined line", eligible, Asset{Kind: "phone", Status: "quarantined"}, "call", "LINE_QUARANTINED"},
		{"email is not a source line", eligible, Asset{Kind: "email", Status: "active"}, "call", "PHONE_LINE_REQUIRED"},
		{"calls do not infer SMS permission", Contact{}, a, "call", "ELIGIBLE"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Gate(test.c, test.a, test.channel); got != test.want {
				t.Fatalf("got %s, want %s", got, test.want)
			}
		})
	}
}
func TestRestoreProblems(t *testing.T) {
	now := time.Now()
	a := Asset{Status: "quarantined", QuarantinedAt: &now}
	tests := []struct {
		name string
		s    *Sample
		want int
	}{
		{"missing", nil, 1}, {"stale", &Sample{Attempts: 100, Filtered: 2, ObservedAt: now}, 1},
		{"too small", &Sample{Attempts: 19, Filtered: 0, ObservedAt: now.Add(time.Second)}, 1},
		{"above threshold", &Sample{Attempts: 100, Filtered: 6, ObservedAt: now.Add(time.Second)}, 1},
		{"at threshold", &Sample{Attempts: 100, Filtered: 5, ObservedAt: now.Add(time.Second)}, 0},
		{"spam label", &Sample{Attempts: 100, Filtered: 2, SpamLabel: true, ObservedAt: now.Add(time.Second)}, 1},
		{"clean", &Sample{Attempts: 100, Filtered: 2, ObservedAt: now.Add(time.Second)}, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := len(RestoreProblems(a, test.s)); got != test.want {
				t.Fatalf("got %d reasons, want %d", got, test.want)
			}
		})
	}
}
func TestValidationAndExport(t *testing.T) {
	for _, s := range []string{"", "  ", strings.Repeat("x", 481), "hello\x00world"} {
		if ValidateMessage(s) == nil {
			t.Errorf("accepted invalid message %q", s)
		}
	}
	for _, s := range []string{"hello", strings.Repeat("é", 480), "hello\nworld"} {
		if err := ValidateMessage(s); err != nil {
			t.Error(err)
		}
	}
	for _, s := range []string{"=1+1", " +12025550101", "\t@SUM(1,2)", "-2"} {
		if !strings.HasPrefix(CSVSafe(s), "'") {
			t.Errorf("formula not neutralized: %q", s)
		}
	}
	if CSVSafe("Delta 02") != "Delta 02" {
		t.Fatal("modified ordinary value")
	}
	if strings.Contains(MaskPhone("+12025550101"), "202555") {
		t.Fatal("raw phone disclosed")
	}
	if MaskEmail("jordan@example.test") != "j•••@example.test" {
		t.Fatal("unexpected email masking")
	}
}
