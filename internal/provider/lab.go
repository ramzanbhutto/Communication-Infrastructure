package provider

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Lab is a separate loopback HTTP provider. It implements wire contracts and
// signed callbacks. Its outcomes are intentionally simulated, not carrier data.
type Lab struct {
	Origin, CallbackOrigin, Account, Token, EmailKey, WebhookSecret string
	mu                                                              sync.Mutex
	records                                                         map[string]labRecord
	throttled                                                       map[string]bool
	server                                                          *http.Server
	client                                                          *http.Client
}
type labRecord struct{ ID, Channel, Callback, Scenario, From, To, Subject, Text string }

func random(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b[:])
}
func NewLab(callback string) (*Lab, error) {
	u, err := url.Parse(callback)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Path != "" || u.User != nil || u.RawQuery != "" {
		return nil, errors.New("lab callback must be a loopback HTTP origin")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	l := &Lab{Origin: "http://" + listener.Addr().String(), CallbackOrigin: callback, Account: "AC" + strings.Repeat("0", 32), Token: random(""), EmailKey: random("re_"), WebhookSecret: "whsec_" + base64.StdEncoding.EncodeToString([]byte(random(""))), records: map[string]labRecord{}, throttled: map[string]bool{}, client: transport()}
	l.server = &http.Server{Handler: http.HandlerFunc(l.serve), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, MaxHeaderBytes: 16384}
	go l.server.Serve(listener)
	return l, nil
}
func (l *Lab) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return l.server.Shutdown(ctx)
}
func (l *Lab) Gateway() (Gateway, error) {
	phone, err := NewTwilio(l.Account, l.Token, "+12025550199", l.Origin)
	if err != nil {
		return nil, err
	}
	email, err := NewResend(l.EmailKey, l.Origin)
	return Router{Phone: phone, Email: email}, err
}
func (l *Lab) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/lab/resources/") {
		account, token, ok := r.BasicAuth()
		if !ok || account != l.Account || token != l.Token {
			http.Error(w, "unauthorized", 401)
			return
		}
		job := strings.TrimPrefix(r.URL.Path, "/lab/resources/")
		l.mu.Lock()
		record, ok := l.records[job]
		l.mu.Unlock()
		if !ok {
			http.Error(w, "not found", 404)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"id": record.ID, "channel": record.Channel, "status": "accepted"})
		return
	}
	if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/emails/receiving/") {
		if r.Header.Get("Authorization") != "Bearer "+l.EmailKey {
			http.Error(w, "unauthorized", 401)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/emails/receiving/")
		l.mu.Lock()
		record, ok := l.records[id]
		l.mu.Unlock()
		if !ok {
			http.Error(w, "not found", 404)
			return
		}
		json.NewEncoder(w).Encode(ReceivedEmail{ID: id, From: record.From, To: []string{record.To}, Subject: record.Subject, Text: record.Text})
		return
	}
	if r.Method != "POST" {
		http.Error(w, "unsupported lab request", 405)
		return
	}
	email := r.URL.Path == "/emails"
	if email {
		if r.Header.Get("Authorization") != "Bearer "+l.EmailKey {
			http.Error(w, "unauthorized", 401)
			return
		}
	} else {
		account, token, ok := r.BasicAuth()
		if !ok || account != l.Account || token != l.Token {
			http.Error(w, "unauthorized", 401)
			return
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	job := r.Header.Get("X-Lab-Job")
	if job == "" {
		http.Error(w, "missing lab job", 422)
		return
	}
	scenario := r.Header.Get("X-Lab-Scenario")
	l.mu.Lock()
	if scenario == "throttle" && !l.throttled[job] {
		l.throttled[job] = true
		l.mu.Unlock()
		w.WriteHeader(429)
		w.Write([]byte(`{"code":"rate_limit"}`))
		return
	}
	l.mu.Unlock()
	channel, callback := "", ""
	if email {
		var data struct {
			From    string   `json:"from"`
			To      []string `json:"to"`
			Subject string   `json:"subject"`
			Text    string   `json:"text"`
		}
		if json.NewDecoder(r.Body).Decode(&data) != nil || !strings.HasSuffix(data.From, ".test") || len(data.To) != 1 || !strings.HasSuffix(data.To[0], ".test") {
			http.Error(w, "synthetic email required", 422)
			return
		}
		channel = "email"
		callback = l.CallbackOrigin + "/hooks/resend"
	} else {
		if r.ParseForm() != nil {
			http.Error(w, "invalid form", 422)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/Messages.json"):
			channel = "sms"
		case strings.HasSuffix(r.URL.Path, "/Calls.json"):
			channel = "call"
		case strings.HasSuffix(r.URL.Path, "/IncomingPhoneNumbers.json"):
			channel = "provision"
		case r.URL.Path == "/v1/Trunks":
			channel = "trunk"
		default:
			http.Error(w, "unknown resource", 404)
			return
		}
		callback = r.PostForm.Get("StatusCallback")
		if channel == "sms" || channel == "call" {
			if !strings.HasPrefix(r.PostForm.Get("From"), "+120255501") || !strings.HasPrefix(r.PostForm.Get("To"), "+120255501") || !strings.HasPrefix(callback, l.CallbackOrigin+"/hooks/twilio/status?job=") {
				http.Error(w, "synthetic addresses required", 422)
				return
			}
		}
	}
	l.mu.Lock()
	record, exists := l.records[job]
	if !exists {
		prefix := map[string]string{"sms": "SM", "call": "CA", "provision": "PN", "trunk": "TK", "email": "email-"}[channel]
		record = labRecord{ID: random(prefix), Channel: channel, Callback: callback, Scenario: scenario}
		l.records[job] = record
	}
	l.mu.Unlock()
	if scenario == "ambiguous" {
		w.WriteHeader(503)
		w.Write([]byte(`{"error":"accepted but response unavailable"}`))
		return
	}
	if email {
		json.NewEncoder(w).Encode(map[string]string{"id": record.ID})
	} else {
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]string{"sid": record.ID, "status": "queued"})
	}
	// Events are requested explicitly by the worker after acceptance is persisted.
}
func (l *Lab) Inspect(ctx context.Context, job string) (Receipt, string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", l.Origin+"/lab/resources/"+url.PathEscape(job), nil)
	req.SetBasicAuth(l.Account, l.Token)
	var out struct {
		ID      string `json:"id"`
		Channel string `json:"channel"`
		Status  string `json:"status"`
	}
	if err := exchange(ctx, l.client, req, &out); err != nil {
		return Receipt{}, "", err
	}
	if out.ID == "" || out.Status != "accepted" {
		return Receipt{}, "", errors.New("lab resource identity unavailable")
	}
	return Receipt{ID: out.ID, Status: out.Status}, out.Channel, nil
}
func (l *Lab) Emit(ctx context.Context, job string) error {
	l.mu.Lock()
	record, ok := l.records[job]
	l.mu.Unlock()
	if !ok {
		return errors.New("provider lab has no record for this job")
	}
	if record.Channel == "provision" || record.Channel == "trunk" {
		return nil
	}
	if record.Channel == "email" {
		kind := "email.delivered"
		if record.Scenario == "bounce" {
			kind = "email.bounced"
		}
		body, _ := json.Marshal(map[string]any{"type": kind, "created_at": time.Now().UTC(), "data": map[string]any{"email_id": record.ID}})
		return l.postEmail(ctx, random("evt_"), body)
	}
	form := url.Values{"AccountSid": {l.Account}}
	if record.Channel == "call" {
		form.Set("CallSid", record.ID)
		form.Set("CallStatus", "completed")
	} else {
		form.Set("MessageSid", record.ID)
		form.Set("MessageStatus", "delivered")
		if record.Scenario == "filtered" {
			form.Set("MessageStatus", "undelivered")
			form.Set("ErrorCode", "30007")
		}
	}
	return l.postTwilio(ctx, record.Callback, form)
}
func (l *Lab) Inbound(ctx context.Context, channel, from, to, body string) error {
	if channel == "sms" {
		return l.postTwilio(ctx, l.CallbackOrigin+"/hooks/twilio/inbound", url.Values{"AccountSid": {l.Account}, "MessageSid": {random("SM")}, "From": {from}, "To": {to}, "Body": {body}})
	}
	// Resend's received event contains metadata. Content is retrieved separately.
	id := random("inbound-")
	l.mu.Lock()
	l.records[id] = labRecord{ID: id, From: from, To: to, Subject: "Property details", Text: body}
	l.mu.Unlock()
	data, _ := json.Marshal(map[string]any{"type": "email.received", "created_at": time.Now().UTC(), "data": map[string]any{"email_id": id, "from": from, "to": []string{to}, "subject": "Property details"}})
	return l.postEmail(ctx, random("evt_"), data)
}
func (l *Lab) postTwilio(ctx context.Context, target string, form url.Values) error {
	req, _ := http.NewRequestWithContext(ctx, "POST", target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Twilio-Signature", TwilioSignature(l.Token, target, form))
	return l.send(req)
}
func (l *Lab) postEmail(ctx context.Context, id string, body []byte) error {
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	req, _ := http.NewRequestWithContext(ctx, "POST", l.CallbackOrigin+"/hooks/resend", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("svix-id", id)
	req.Header.Set("svix-timestamp", stamp)
	req.Header.Set("svix-signature", SvixSignature(l.WebhookSecret, id, stamp, body))
	return l.send(req)
}
func (l *Lab) send(req *http.Request) error {
	res, err := l.client.Do(req)
	if err != nil {
		return errors.New("lab callback unavailable")
	}
	defer res.Body.Close()
	io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return errors.New("lab callback rejected")
	}
	return nil
}
