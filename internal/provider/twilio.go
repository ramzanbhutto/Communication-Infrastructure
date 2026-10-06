package provider

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

type Twilio struct {
	account, token, base, trunkBase string
	client                          *http.Client
	operator                        string
}

func NewTwilio(account, token, operator, override string) (*Twilio, error) {
	if account == "" || token == "" {
		return nil, errors.New("Twilio credentials are required")
	}
	base, err := endpoint(override, "https://api.twilio.com")
	if err != nil {
		return nil, err
	}
	trunkBase := "https://trunking.twilio.com"
	if override != "" {
		trunkBase = base
	}
	return &Twilio{account: account, token: token, operator: operator, base: base, trunkBase: trunkBase, client: transport()}, nil
}
func (t *Twilio) Submit(ctx context.Context, in Request) (Receipt, error) {
	path := t.base + "/2010-04-01/Accounts/" + url.PathEscape(t.account) + "/"
	form := url.Values{}
	switch in.Channel {
	case "sms":
		path += "Messages.json"
		form.Set("From", in.From)
		form.Set("To", in.To)
		form.Set("Body", in.Body)
		form.Set("StatusCallback", in.Callback)
	case "call":
		if t.operator == "" {
			return Receipt{}, &Failure{Code: "OPERATOR_NUMBER_REQUIRED"}
		}
		// Call the operator first, then bridge the intended recipient. Never
		// mark a PSTN connection answered from an elapsed timer.
		path += "Calls.json"
		form.Set("From", in.From)
		form.Set("To", t.operator)
		form.Set("Twiml", "<Response><Dial callerId=\""+xmlText(in.From)+"\"><Number>"+xmlText(in.To)+"</Number></Dial></Response>")
		form.Set("StatusCallback", in.Callback)
		form.Set("StatusCallbackEvent", "completed")
	case "provision":
		path += "IncomingPhoneNumbers.json"
		form.Set("PhoneNumber", in.To)
		form.Set("FriendlyName", in.Subject)
	case "trunk":
		path = t.trunkBase + "/v1/Trunks"
		form.Set("FriendlyName", in.Subject)
		form.Set("Secure", "true")
	default:
		return Receipt{}, &Failure{Code: "UNSUPPORTED_CHANNEL"}
	}
	req, _ := http.NewRequest("POST", path, strings.NewReader(form.Encode()))
	req.SetBasicAuth(t.account, t.token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if strings.HasPrefix(t.base, "http://") {
		req.Header.Set("X-Lab-Job", in.ID)
		req.Header.Set("X-Lab-Scenario", in.Scenario)
	}
	var out struct {
		SID    string `json:"sid"`
		Status string `json:"status"`
	}
	if err := exchange(ctx, t.client, req, &out); err != nil {
		return Receipt{}, err
	}
	if out.SID == "" {
		return Receipt{}, &Failure{Code: "PROVIDER_RESPONSE_INVALID", Ambiguous: true}
	}
	return Receipt{ID: out.SID, Status: "accepted"}, nil
}
func xmlText(value string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(value))
	return b.String()
}

// Twilio signs the externally configured URL followed by sorted form fields.
// The caller supplies a trusted canonical URL, never a forwarded Host header.
func TwilioSignature(token, canonical string, form url.Values) string {
	keys := make([]string, 0, len(form))
	for k := range form {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(canonical)
	for _, k := range keys {
		values := append([]string(nil), form[k]...)
		sort.Strings(values)
		for _, v := range values {
			b.WriteString(k)
			b.WriteString(v)
		}
	}
	h := hmac.New(sha1.New, []byte(token))
	h.Write([]byte(b.String()))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
func VerifyTwilio(token, canonical, signature string, form url.Values) bool {
	return signature != "" && hmac.Equal([]byte(signature), []byte(TwilioSignature(token, canonical, form)))
}
