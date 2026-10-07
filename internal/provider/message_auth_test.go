package provider

import (
	"strings"
	"testing"
)

func TestSignedMessageFixtures(t *testing.T) {
	for _, test := range []struct{ scenario, dkim, dmarc string }{{"aligned", "pass", "pass"}, {"unaligned", "pass", "fail"}, {"tampered", "fail", "fail"}} {
		a, e := MessageAuthFixture(test.scenario)
		if e != nil || a.DKIM != test.dkim || a.DMARC != test.dmarc {
			t.Fatalf("%+v %v", a, e)
		}
		if _, _, e = VerifySimpleDKIM(strings.Replace(a.RawMessage, "simple/simple", "relaxed/relaxed", 1), a.PublicKey); e == nil {
			t.Fatal("unsupported canonicalization accepted")
		}
		if got, _, e := VerifySimpleDKIM(strings.Replace(a.RawMessage, "Requested property details", "Altered subject", 1), a.PublicKey); e == nil && got == "pass" {
			t.Fatal("unsigned header alteration accepted")
		}
	}
}
