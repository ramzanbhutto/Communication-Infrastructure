package provider

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"
)

type EmailResolver interface {
	TXTResolver
	LookupMX(context.Context, string) ([]*net.MX, error)
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}
type EmailCheck struct {
	State       string      `json:"state"`
	Explanation string      `json:"explanation"`
	Records     []DNSRecord `json:"records"`
	Lookups     int         `json:"lookups"`
}
type EmailAssessment struct {
	FixtureScenario string     `json:"fixtureScenario,omitempty"`
	Domain          string     `json:"domain"`
	Selector        string     `json:"selector"`
	Source          string     `json:"source"`
	State           string     `json:"state"`
	CheckedAt       time.Time  `json:"checkedAt"`
	MX              EmailCheck `json:"mx"`
	SPF             EmailCheck `json:"spf"`
	DKIM            EmailCheck `json:"dkim"`
	DMARC           EmailCheck `json:"dmarc"`
	Limitations     []string   `json:"limitations"`
}
type NativeDNS struct{ Resolver *net.Resolver }

func (n NativeDNS) LookupTXT(ctx context.Context, name string) ([]string, error) {
	return n.Resolver.LookupTXT(ctx, name)
}
func (n NativeDNS) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	return n.Resolver.LookupMX(ctx, name)
}
func (n NativeDNS) LookupNetIP(ctx context.Context, network, name string) ([]netip.Addr, error) {
	return n.Resolver.LookupNetIP(ctx, network, name)
}
func ValidDomain(domain string) bool {
	if len(domain) < 3 || len(domain) > 253 || !strings.Contains(domain, ".") || strings.HasSuffix(domain, ".") {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}
func validSelector(s string) bool {
	return len(s) > 0 && len(s) <= 63 && !strings.Contains(s, ".") && ValidDomain(s+".test")
}
func recordError(err error) string {
	var dns *net.DNSError
	if errors.As(err, &dns) && dns.IsNotFound {
		return "missing"
	}
	return "unavailable"
}
func txtRecord(ctx context.Context, r TXTResolver, name, prefix string) DNSRecord {
	out := DNSRecord{Name: name, State: "missing", Values: []string{}}
	values, err := r.LookupTXT(ctx, name)
	if err != nil {
		out.State = recordError(err)
		if out.State == "unavailable" {
			message := "The resolver did not establish record presence."
			out.Error = &message
		}
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
	if len(out.Values) > 1 {
		out.State = "conflicting"
	}
	return out
}
func tagMap(text string) (map[string]string, error) {
	out := map[string]string{}
	for _, part := range strings.Split(text, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		key = strings.ToLower(strings.TrimSpace(key))
		if !ok || key == "" {
			return nil, errors.New("Malformed tag.")
		}
		if _, exists := out[key]; exists {
			return nil, errors.New("Duplicate tag: " + key)
		}
		out[key] = strings.TrimSpace(value)
	}
	return out, nil
}
func assessDKIM(record DNSRecord) EmailCheck {
	out := EmailCheck{State: record.State, Records: []DNSRecord{record}, Explanation: "Publish a DKIM key at the configured selector."}
	if record.State != "present" {
		return out
	}
	// A DKIM key need not contain the optional v= tag.
	tags, err := tagMap(record.Values[0])
	if err != nil {
		out.State = "invalid"
		out.Explanation = err.Error()
		return out
	}
	if v, ok := tags["v"]; ok && v != "DKIM1" {
		out.State = "invalid"
		out.Explanation = "Unsupported DKIM version."
		return out
	}
	p, exists := tags["p"]
	if !exists {
		out.State = "invalid"
		out.Explanation = "The key is missing its p= tag."
		return out
	}
	if p == "" {
		out.State = "revoked"
		out.Explanation = "The published key is revoked."
		return out
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(p), ""))
	if err != nil {
		out.State = "invalid"
		out.Explanation = "The public key is not valid base64."
		return out
	}
	kind := tags["k"]
	if kind == "" {
		kind = "rsa"
	}
	if kind == "rsa" {
		key, e := x509.ParsePKIXPublicKey(decoded)
		if e != nil {
			out.State = "invalid"
			out.Explanation = "The RSA public key cannot be parsed."
			return out
		}
		rsaKey, ok := key.(*rsa.PublicKey)
		if !ok || rsaKey.Size() < 128 {
			out.State = "invalid"
			out.Explanation = "The RSA key type or size is unsupported."
			return out
		}
		out.State = "valid"
		out.Explanation = fmt.Sprintf("Parsed RSA key (%d bits). This does not verify a message signature.", rsaKey.Size()*8)
	} else if kind == "ed25519" && len(decoded) == 32 {
		out.State = "valid"
		out.Explanation = "Parsed Ed25519 key. This does not verify a message signature."
	} else {
		out.State = "invalid"
		out.Explanation = "Unsupported key type or invalid key length."
	}
	return out
}
func assessDMARC(record DNSRecord) EmailCheck {
	out := EmailCheck{State: record.State, Records: []DNSRecord{record}, Explanation: "Publish one DMARC policy for this domain."}
	if record.State != "present" {
		return out
	}
	tags, err := tagMap(record.Values[0])
	if err != nil {
		out.State = "invalid"
		out.Explanation = err.Error()
		return out
	}
	if tags["v"] != "DMARC1" || (tags["p"] != "none" && tags["p"] != "quarantine" && tags["p"] != "reject") {
		out.State = "invalid"
		out.Explanation = "The DMARC version or p= policy is invalid."
		return out
	}
	for _, key := range []string{"adkim", "aspf"} {
		v, ok := tags[key]
		if ok && v != "s" && v != "r" {
			out.State = "invalid"
			out.Explanation = "Invalid alignment mode: " + key
			return out
		}
	}
	for _, key := range []string{"sp", "np"} {
		if v, ok := tags[key]; ok && v != "none" && v != "quarantine" && v != "reject" {
			out.State = "invalid"
			out.Explanation = "Invalid subdomain policy: " + key
			return out
		}
	}
	out.State = "valid"
	out.Explanation = "Policy p=" + tags["p"] + " is syntactically valid. Sender alignment requires authenticated message evidence."
	return out
}

// This is a bounded static SPF configuration analysis, not check_host(). Paths
// depend on the sending IP and envelope identity; a lower bound is labeled as such.
func assessSPF(ctx context.Context, resolver TXTResolver, domain string) EmailCheck {
	out := EmailCheck{State: "valid", Records: []DNSRecord{}, Explanation: "SPF policy parsed. Static lookup count is a lower bound, not a sender authorization result."}
	path := map[string]bool{}
	cache := map[string]DNSRecord{}
	queries := 0
	var walk func(string, int) bool
	walk = func(name string, depth int) bool {
		if depth > 10 || path[name] {
			out.State = "invalid"
			out.Explanation = "SPF references form a cycle or exceed the inspection depth."
			return false
		}
		record, ok := cache[name]
		if !ok {
			if queries >= 30 {
				out.State = "indeterminate"
				out.Explanation = "The bounded DNS inspection budget was exhausted."
				return false
			}
			queries++
			record = txtRecord(ctx, resolver, name, "v=spf1")
			cache[name] = record
			out.Records = append(out.Records, record)
		}
		if record.State != "present" {
			out.State = record.State
			out.Explanation = "SPF record " + name + " is " + record.State + "."
			return false
		}
		terms := strings.Fields(record.Values[0])
		if len(terms) == 0 || terms[0] != "v=spf1" {
			out.State = "invalid"
			out.Explanation = "Invalid SPF version."
			return false
		}
		path[name] = true
		defer delete(path, name)
		redirectSeen := false
		allSeen := false
		for i, term := range terms[1:] {
			qualifier := byte('+')
			if strings.ContainsAny(term[:1], "+-~?") {
				qualifier = term[0]
				term = term[1:]
			}
			if term == "" {
				out.State = "invalid"
				out.Explanation = "Empty SPF mechanism."
				return false
			}
			if strings.Contains(term, "%{") {
				out.State = "indeterminate"
				out.Explanation = "SPF macros need a sending identity and are not evaluated by static diagnostics."
				return false
			}
			namePart, target, _ := strings.Cut(term, ":")
			switch {
			case term == "all":
				allSeen = true
				if qualifier == '+' {
					out.State = "invalid"
					out.Explanation = "+all authorizes every sending host."
					return false
				}
				if i < len(terms)-2 {
					out.Explanation = "Terms after all are unreachable. Static lookup count is a lower bound."
				}
				return true
			case strings.HasPrefix(term, "redirect="):
				if redirectSeen {
					out.State = "invalid"
					out.Explanation = "Duplicate redirect modifier."
					return false
				}
				redirectSeen = true
				target = strings.TrimPrefix(term, "redirect=")
				if !ValidDomain(target) {
					out.State = "invalid"
					out.Explanation = "Invalid SPF redirect domain."
					return false
				}
				out.Lookups++
				if !walk(target, depth+1) {
					return false
				}
			case strings.Contains(term, "="):
				// Unknown modifiers are permitted by SPF. They are not mechanisms.
				continue
			case namePart == "include":
				if !ValidDomain(target) {
					out.State = "invalid"
					out.Explanation = "Invalid SPF include domain."
					return false
				}
				out.Lookups++
				if !walk(target, depth+1) {
					return false
				}
			case namePart == "ip4" || namePart == "ip6":
				value := target
				if !strings.Contains(value, "/") {
					if namePart == "ip4" {
						value += "/32"
					} else {
						value += "/128"
					}
				}
				p, e := netip.ParsePrefix(value)
				if e != nil || (namePart == "ip4") != p.Addr().Is4() {
					out.State = "invalid"
					out.Explanation = "Invalid SPF IP mechanism."
					return false
				}
			case namePart == "a" || namePart == "mx" || namePart == "exists" || namePart == "ptr" || strings.HasPrefix(namePart, "a/") || strings.HasPrefix(namePart, "mx/"):
				out.Lookups++
				out.Explanation = "Static lookup count is a lower bound. Address, MX, exists and ptr mechanisms require contextual evaluation."
			default:
				out.State = "invalid"
				out.Explanation = "Unknown SPF mechanism: " + namePart
				return false
			}
			if out.Lookups > 10 {
				out.State = "indeterminate"
				out.Explanation = "Referenced paths contain more than 10 lookup terms. Which paths execute depends on the sender; evaluate before sending."
				return false
			}
		}
		if !allSeen && !redirectSeen {
			out.Explanation = "No all or redirect fallback; unmatched senders receive neutral. Authorization was not evaluated."
		}
		return true
	}
	walk(domain, 0)
	return out
}
func InspectEmail(ctx context.Context, resolver EmailResolver, domain, selector, source string) (EmailAssessment, error) {
	if !ValidDomain(domain) || !validSelector(selector) {
		return EmailAssessment{}, errors.New("invalid domain or selector")
	}
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	a := EmailAssessment{Domain: domain, Selector: selector, Source: source, CheckedAt: time.Now().UTC(), State: "configuration_ready", Limitations: []string{"DNS configuration is separate from message authentication and inbox placement.", "SPF lookup counts are static lower bounds; sending IP and envelope identity are not evaluated.", "DKIM signatures, DMARC message alignment and reputation are not verified.", "DMARC inspection validates explicit domain policy only; organizational policy discovery is not implemented."}}
	a.MX = EmailCheck{State: "missing", Records: []DNSRecord{{Name: domain, State: "missing", Values: []string{}}}, Explanation: "No MX route was established."}
	mx, err := resolver.LookupMX(bounded, domain)
	if err != nil {
		a.MX.State = recordError(err)
		a.MX.Records[0].State = a.MX.State
		if a.MX.State == "unavailable" {
			a.MX.Explanation = "MX lookup failed. Routing is unconfirmed."
		}
	} else if len(mx) > 0 {
		a.MX.State = "valid"
		a.MX.Explanation = "MX routing records are present. This does not prove a recipient mailbox exists."
		a.MX.Records[0].State = "present"
		for _, m := range mx {
			a.MX.Records[0].Values = append(a.MX.Records[0].Values, fmt.Sprintf("%d %s", m.Pref, m.Host))
			if m.Host == "." {
				a.MX.State = "invalid"
				a.MX.Explanation = "Null MX declares that this domain does not accept email."
			}
		}
	}
	a.SPF = assessSPF(bounded, resolver, domain)
	// Version is optional in DKIM key records, so fetch all TXT records here.
	dkim := DNSRecord{Name: selector + "._domainkey." + domain, State: "missing", Values: []string{}}
	values, err := resolver.LookupTXT(bounded, dkim.Name)
	if err != nil {
		dkim.State = recordError(err)
	} else {
		for _, v := range values {
			if strings.Contains(v, "p=") {
				dkim.Values = append(dkim.Values, v)
			}
		}
		if len(dkim.Values) > 0 {
			dkim.State = "present"
		}
		if len(dkim.Values) > 1 {
			dkim.State = "conflicting"
		}
	}
	a.DKIM = assessDKIM(dkim)
	a.DMARC = assessDMARC(txtRecord(bounded, resolver, "_dmarc."+domain, "v=DMARC1"))
	for _, c := range []EmailCheck{a.MX, a.SPF, a.DKIM, a.DMARC} {
		if c.State != "valid" {
			a.State = "configuration_issue"
		}
		if c.State == "unavailable" {
			a.State = "unavailable"
			break
		}
	}
	return a, nil
}

// Named local fixtures exercise the same parser and resolver interface as native DNS.
type EmailFixtureDNS struct {
	Domain   string
	Scenario string
}

const fixtureKey = "MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA0O7OBZ1CDIlZtVxPtInJnSWhZSGby0x2salgZTyOEahJCRffUF7PDt+2AVuqKYk4o317MdPyDskuZB4vMts12yxjtJ/VXoPqZ/dTwJ+ltthofaKRCMM3E0T8HMqEcIxU5ItK3xJeVuKihjXPyvDJ668zsG+sRZNXuT5VG9vUXcWzzUx1qZuYTNLtHUasMxB+K3Gfgc9+hyhzXPfzd2KSRweRzEJIeajwZXP3Z+ixSfux5NFrq2frCWPkj6Bg1MHU1YXmWR7xF3TM8pk/eMsizIv1eF0Pc/ICuUWId3T59VNlLqfQGToZDEj43L6/2FXVj8z8GgE6CiE/pmsdAYt7KQIDAQAB"

func (f EmailFixtureDNS) LookupTXT(_ context.Context, name string) ([]string, error) {
	if f.Scenario == "timeout" {
		return nil, &net.DNSError{Err: "synthetic timeout", IsTimeout: true}
	}
	if strings.HasPrefix(name, "_dmarc.") {
		if f.Scenario == "broken" {
			return []string{"v=DMARC1; p=invalid"}, nil
		}
		return []string{"v=DMARC1; p=quarantine; adkim=s; aspf=s"}, nil
	}
	if strings.Contains(name, "._domainkey.") {
		if !strings.HasPrefix(name, "mail.") {
			return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
		}
		if f.Scenario == "broken" {
			return []string{"v=DKIM1; k=rsa; p="}, nil
		}
		return []string{"v=DKIM1; k=rsa; p=" + fixtureKey}, nil
	}
	if name == f.Domain {
		if f.Scenario == "conflicting" {
			return []string{"v=spf1 -all", "v=spf1 +all"}, nil
		}
		return []string{"v=spf1 ip4:192.0.2.10 -all"}, nil
	}
	return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
}
func (f EmailFixtureDNS) LookupMX(_ context.Context, _ string) ([]*net.MX, error) {
	if f.Scenario == "timeout" {
		return nil, &net.DNSError{Err: "synthetic timeout", IsTimeout: true}
	}
	return []*net.MX{{Host: "mx.example.test.", Pref: 10}}, nil
}
func (f EmailFixtureDNS) LookupNetIP(_ context.Context, _, _ string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("192.0.2.10")}, nil
}
