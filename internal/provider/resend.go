package provider

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Resend struct {
	key, base string
	client    *http.Client
}
type ReceivedEmail struct {
	ID      string   `json:"id"`
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Text    string   `json:"text"`
}

func (r *Resend) Received(ctx context.Context, id string) (ReceivedEmail, error) {
	if id == "" || len(id) > 100 || strings.ContainsAny(id, "/\\?#") {
		return ReceivedEmail{}, errors.New("invalid received email identifier")
	}
	req, _ := http.NewRequest("GET", r.base+"/emails/receiving/"+url.PathEscape(id), nil)
	req.Header.Set("Authorization", "Bearer "+r.key)
	var out ReceivedEmail
	err := exchange(ctx, r.client, req, &out)
	if err == nil && out.ID != id {
		err = errors.New("received email identity mismatch")
	}
	return out, err
}

func NewResend(key, override string) (*Resend, error) {
	if key == "" {
		return nil, errors.New("Resend credentials are required")
	}
	base, err := endpoint(override, "https://api.resend.com")
	if err != nil {
		return nil, err
	}
	return &Resend{key: key, base: base, client: transport()}, nil
}
func (r *Resend) Submit(ctx context.Context, in Request) (Receipt, error) {
	if in.Channel != "email" {
		return Receipt{}, &Failure{Code: "UNSUPPORTED_CHANNEL"}
	}
	data, _ := json.Marshal(map[string]any{"from": in.From, "to": []string{in.To}, "subject": in.Subject, "text": in.Body, "tags": []map[string]string{{"name": "job", "value": in.ID}}})
	req, _ := http.NewRequest("POST", r.base+"/emails", strings.NewReader(string(data)))
	req.Header.Set("Authorization", "Bearer "+r.key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", in.ID)
	if strings.HasPrefix(r.base, "http://") {
		req.Header.Set("X-Lab-Job", in.ID)
		req.Header.Set("X-Lab-Scenario", in.Scenario)
	}
	var out Receipt
	if err := exchange(ctx, r.client, req, &out); err != nil {
		return Receipt{}, err
	}
	if out.ID == "" {
		return Receipt{}, &Failure{Code: "PROVIDER_RESPONSE_INVALID", Ambiguous: true}
	}
	out.Status = "accepted"
	return out, nil
}
func SvixSignature(secret, id, stamp string, body []byte) string {
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil {
		return ""
	}
	h := hmac.New(sha256.New, key)
	h.Write([]byte(id + "." + stamp + "."))
	h.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(h.Sum(nil))
}
func VerifySvix(secret, id, stamp, signatures string, body []byte, now time.Time) bool {
	t, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil || id == "" || len(id) > 200 || now.Sub(time.Unix(t, 0)) > 5*time.Minute || time.Unix(t, 0).Sub(now) > 5*time.Minute {
		return false
	}
	expected := SvixSignature(secret, id, stamp, body)
	if expected == "" {
		return false
	}
	for _, s := range strings.Fields(signatures) {
		if hmac.Equal([]byte(s), []byte(expected)) {
			return true
		}
	}
	return false
}

type Router struct {
	Phone Gateway
	Email Gateway
}

func (r Router) Submit(ctx context.Context, in Request) (Receipt, error) {
	if in.Channel == "email" {
		return r.Email.Submit(ctx, in)
	}
	return r.Phone.Submit(ctx, in)
}
