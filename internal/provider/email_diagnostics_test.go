package provider

import (
	"context"
	"net"
	"testing"
)

func TestEmailAssessment(t *testing.T) {
	for _, test := range []struct{ scenario, state, dkim string }{{"healthy", "configuration_ready", "valid"}, {"broken", "configuration_issue", "revoked"}, {"timeout", "unavailable", "unavailable"}, {"conflicting", "configuration_issue", "valid"}} {
		t.Run(test.scenario, func(t *testing.T) {
			a, err := InspectEmail(context.Background(), EmailFixtureDNS{Domain: "north.example.test", Scenario: test.scenario}, "north.example.test", "mail", "fixture")
			if err != nil || a.State != test.state || a.DKIM.State != test.dkim {
				t.Fatalf("unexpected assessment: %+v %v", a, err)
			}
		})
	}
	for _, domain := range []string{"-bad.example", "bad..example", "example.test/route", "localhost", ".example", "a."} {
		if _, err := InspectEmail(context.Background(), EmailFixtureDNS{}, domain, "mail", "fixture"); err == nil {
			t.Fatal("invalid domain accepted", domain)
		}
	}
	if _, err := InspectEmail(context.Background(), EmailFixtureDNS{}, "example.test", "../../mail", "fixture"); err == nil {
		t.Fatal("invalid selector accepted")
	}
}

type txtMap map[string][]string

func (m txtMap) LookupTXT(_ context.Context, name string) ([]string, error) {
	v, ok := m[name]
	if !ok {
		return nil, &net.DNSError{IsNotFound: true}
	}
	return v, nil
}
func TestSPFDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name  string
		dns   txtMap
		state string
	}{{"cycle", txtMap{"a.test": {"v=spf1 include:b.test -all"}, "b.test": {"v=spf1 include:a.test -all"}}, "invalid"}, {"public-all", txtMap{"a.test": {"v=spf1 +all"}}, "invalid"}, {"conflict", txtMap{"a.test": {"v=spf1 -all", "v=spf1 +all"}}, "conflicting"}, {"macro", txtMap{"a.test": {"v=spf1 exists:%{i}.test -all"}}, "indeterminate"}, {"ip", txtMap{"a.test": {"v=spf1 ip4:192.0.2.0/24 ip6:2001:db8::/32 -all"}}, "valid"}, {"invalid-ip", txtMap{"a.test": {"v=spf1 ip4:999.1.1.1 -all"}}, "invalid"}, {"unreachable", txtMap{"a.test": {"v=spf1 -all include:missing.test"}}, "valid"}, {"empty", txtMap{"a.test": {"v=spf1 -"}}, "invalid"}} {
		t.Run(test.name, func(t *testing.T) {
			a := assessSPF(context.Background(), test.dns, "a.test")
			if a.State != test.state {
				t.Fatalf("%+v", a)
			}
		})
	}
}
func TestDMARCAndDKIMConfiguration(t *testing.T) {
	for _, record := range []string{"v=DMARC1; p=none; p=reject", "v=DMARC1; p=reject; adkim=x", "v=DMARC1; p=invalid"} {
		a := assessDMARC(DNSRecord{State: "present", Values: []string{record}})
		if a.State != "invalid" {
			t.Fatal("invalid policy accepted", a)
		}
	}
	a := assessDKIM(DNSRecord{State: "present", Values: []string{"k=rsa; p=" + fixtureKey}})
	if a.State != "valid" {
		t.Fatal("optional version rejected", a)
	}
}
