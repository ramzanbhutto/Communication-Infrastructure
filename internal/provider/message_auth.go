package provider

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/mail"
	"strings"
)

type MessageAuthentication struct {
	Source        string   `json:"source"`
	Scenario      string   `json:"scenario"`
	DKIM          string   `json:"dkim"`
	DMARC         string   `json:"dmarc"`
	FromDomain    string   `json:"fromDomain"`
	SigningDomain string   `json:"signingDomain"`
	RawMessage    string   `json:"rawMessage"`
	PublicKey     string   `json:"publicKey"`
	Explanation   string   `json:"explanation"`
	Limitations   []string `json:"limitations"`
}

// This deliberately narrow verifier accepts our controlled simple/simple,
// ed25519-sha256 fixtures. Unsupported canonicalization is never labeled pass.
func VerifySimpleDKIM(raw, keyRecord string) (string, string, error) {
	if len(raw) > 16384 || strings.Contains(strings.ReplaceAll(raw, "\r\n", ""), "\n") {
		return "", "", errors.New("bounded CRLF message required")
	}
	header, body, ok := strings.Cut(raw, "\r\n\r\n")
	if !ok {
		return "", "", errors.New("message headers required")
	}
	headers := strings.Split(header, "\r\n")
	values := map[string][]string{}
	signature := ""
	for _, line := range headers {
		name, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(name) != name || name == "" {
			return "", "", errors.New("folded or malformed headers are unsupported")
		}
		name = strings.ToLower(name)
		values[name] = append(values[name], line)
		if name == "dkim-signature" {
			if signature != "" {
				return "", "", errors.New("multiple signatures are unsupported")
			}
			signature = strings.TrimSpace(value)
		}
	}
	tags, err := tagMap(signature)
	if err != nil {
		return "", "", err
	}
	if tags["v"] != "1" || tags["a"] != "ed25519-sha256" || tags["c"] != "simple/simple" || tags["l"] != "" {
		return "", "", errors.New("unsupported DKIM fixture profile")
	}
	if !ValidDomain(tags["d"]) || !validSelector(tags["s"]) {
		return "", "", errors.New("invalid signing identity")
	}
	keys, err := tagMap(keyRecord)
	if err != nil || keys["k"] != "ed25519" {
		return "", "", errors.New("fixture key required")
	}
	pub, err := base64.StdEncoding.DecodeString(keys["p"])
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return "", "", errors.New("invalid public key")
	}
	canonicalBody := strings.TrimRight(body, "\r\n") + "\r\n"
	bodyHash := sha256.Sum256([]byte(canonicalBody))
	if tags["bh"] != base64.StdEncoding.EncodeToString(bodyHash[:]) {
		return "fail", tags["d"], nil
	}
	signed := ""
	indices := map[string]int{}
	fromSigned := false
	for _, name := range strings.Split(tags["h"], ":") {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "dkim-signature" {
			return "", "", errors.New("nested signature signing unsupported")
		}
		if name == "from" {
			fromSigned = true
		}
		list := values[name]
		used := indices[name]
		if used < len(list) {
			signed += list[len(list)-1-used] + "\r\n"
			indices[name]++
		}
	}
	if !fromSigned || len(values["from"]) != 1 {
		return "", "", errors.New("one signed From header is required")
	}
	// Fixtures use a final, single-line b= tag, avoiding ambiguous stripping.
	sigLine := values["dkim-signature"][0]
	at := strings.LastIndex(sigLine, "; b=")
	if at < 0 || strings.Contains(sigLine[at+4:], ";") {
		return "", "", errors.New("unsupported signature layout")
	}
	signed += sigLine[:at+4]
	digest := sha256.Sum256([]byte(signed))
	sig, err := base64.StdEncoding.DecodeString(tags["b"])
	if err != nil {
		return "", "", errors.New("invalid signature encoding")
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), digest[:], sig) {
		return "fail", tags["d"], nil
	}
	return "pass", tags["d"], nil
}
func MessageAuthFixture(scenario string) (MessageAuthentication, error) {
	if scenario != "aligned" && scenario != "unaligned" && scenario != "tampered" {
		return MessageAuthentication{}, errors.New("choose aligned, unaligned or tampered")
	}
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return MessageAuthentication{}, err
	}
	domain := "north.example.test"
	signing := domain
	if scenario == "unaligned" {
		signing = "different.example.test"
	}
	body := "The synthetic property details you requested.\r\n"
	hash := sha256.Sum256([]byte(body))
	headers := "From: Operator <operator@" + domain + ">\r\nTo: Avery <avery@example.test>\r\nSubject: Requested property details\r\n"
	unsigned := "DKIM-Signature: v=1; a=ed25519-sha256; c=simple/simple; d=" + signing + "; s=mail; h=from:to:subject; bh=" + base64.StdEncoding.EncodeToString(hash[:]) + "; b="
	digest := sha256.Sum256([]byte(headers + unsigned))
	sig, err := private.Sign(rand.Reader, digest[:], crypto.Hash(0))
	if err != nil {
		return MessageAuthentication{}, err
	}
	raw := headers + unsigned + base64.StdEncoding.EncodeToString(sig) + "\r\n\r\n" + body
	if scenario == "tampered" {
		raw = strings.Replace(raw, "The synthetic property details", "Changed property details", 1)
	}
	record := "v=DKIM1; k=ed25519; p=" + base64.StdEncoding.EncodeToString(pub)
	result, signing, err := VerifySimpleDKIM(raw, record)
	if err != nil {
		return MessageAuthentication{}, err
	}
	from, _ := mail.ParseAddress("Operator <operator@" + domain + ">")
	_, fromDomain, _ := strings.Cut(from.Address, "@")
	dmarc := "fail"
	explanation := "The DKIM signature does not verify. No authenticated aligned identity was established."
	if result == "pass" {
		explanation = "The signature verifies, but the signing domain does not align with From under strict alignment."
		if signing == fromDomain {
			dmarc = "pass"
			explanation = "The signature verifies and its domain exactly matches From. This satisfies the DKIM path for strict DMARC alignment in this controlled fixture."
		}
	}
	return MessageAuthentication{Source: "locally_generated_signed_fixture", Scenario: scenario, DKIM: result, DMARC: dmarc, FromDomain: fromDomain, SigningDomain: signing, RawMessage: raw, PublicKey: record, Explanation: explanation, Limitations: []string{"No message is sent. This verifies actual local signatures, not a provider delivery claim.", "Only simple/simple Ed25519 fixtures with unfolded headers are supported.", "The message key is generated for this fixture and is separate from DNS configuration fixtures.", "SPF is not evaluated; the DMARC result exercises only strict DKIM alignment."}}, nil
}
