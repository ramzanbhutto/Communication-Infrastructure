package provider

import (
	"context"
	"testing"
)

func TestSIPProtocolRoundTrip(t *testing.T) {
	peer, err := NewSIPPeer()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	out, err := ProbeSIP(context.Background(), peer.Address)
	if err != nil || out.Status != "200 OK" || out.RoundTripMs < 0 {
		t.Fatalf("probe: %+v %v", out, err)
	}
	if _, err = ProbeSIP(context.Background(), "203.0.113.1:5060"); err == nil {
		t.Fatal("non-local SIP probe permitted")
	}
}
