package provider

import (
	"context"
	"errors"
	"net"
	"regexp"
	"strings"
)

type TXTResolver interface {
	LookupTXT(context.Context, string) ([]string, error)
}
type DNSRecord struct {
	Name   string   `json:"name"`
	State  string   `json:"state"`
	Values []string `json:"values"`
	Error  *string  `json:"error"`
}
type DNSAssessment struct {
	Source     string    `json:"source"`
	SPF        DNSRecord `json:"spf"`
	DKIM       DNSRecord `json:"dkim"`
	DMARC      DNSRecord `json:"dmarc"`
	Limitation string    `json:"limitation"`
}

var DNSLabel = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]{0,252}$`)

func InspectDNS(ctx context.Context, resolver TXTResolver, domain, selector, source string) (DNSAssessment, error) {
	if !DNSLabel.MatchString(domain) || !DNSLabel.MatchString(selector) || strings.Contains(selector, ".") {
		return DNSAssessment{}, errors.New("invalid DNS name or selector")
	}
	check := func(name, prefix string) DNSRecord {
		out := DNSRecord{Name: name, State: "missing", Values: []string{}}
		values, err := resolver.LookupTXT(ctx, name)
		if err != nil {
			var dns *net.DNSError
			if errors.As(err, &dns) && dns.IsNotFound {
				return out
			}
			out.State = "unavailable"
			reason := "DNS query failed; record presence is unconfirmed."
			out.Error = &reason
			return out
		}
		for _, v := range values {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(v)), strings.ToLower(prefix)) {
				out.Values = append(out.Values, v)
			}
		}
		if len(out.Values) > 0 {
			out.State = "present"
		}
		if len(out.Values) > 1 && prefix != "v=DKIM1" {
			out.State = "conflicting"
		}
		return out
	}
	return DNSAssessment{Source: source, SPF: check(domain, "v=spf1"), DKIM: check(selector+"._domainkey."+domain, "v=DKIM1"), DMARC: check("_dmarc."+domain, "v=DMARC1"), Limitation: "Record presence only. Does not verify SPF authorization, DKIM signatures, DMARC alignment or inbox placement."}, nil
}

type LabDNS struct{}

func (LabDNS) LookupTXT(_ context.Context, name string) ([]string, error) {
	if strings.HasSuffix(name, "north.example.test") {
		switch {
		case strings.HasPrefix(name, "_dmarc."):
			return []string{"v=DMARC1; p=quarantine"}, nil
		case strings.Contains(name, "._domainkey."):
			return []string{"v=DKIM1; k=rsa; p=LAB_FIXTURE_NOT_A_REAL_KEY"}, nil
		default:
			return []string{"v=spf1 -all"}, nil
		}
	}
	return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}
