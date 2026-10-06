package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// BlueBubbles implements the documented Mac bridge REST contract. Only the
// AppleScript path and existing one-to-one iMessage chats are supported.
// Authentication stays on the server; URLs containing passwords are never logged.
type BlueBubbles struct {
	base, password string
	client         *http.Client
}
type BridgeChat struct {
	GUID    string `json:"guid"`
	Service string `json:"serviceName"`
}
type BridgeMessage struct {
	GUID      string `json:"guid"`
	Text      string `json:"text"`
	FromMe    bool   `json:"isFromMe"`
	Created   int64  `json:"dateCreated"`
	Delivered *int64 `json:"dateDelivered"`
	Read      *int64 `json:"dateRead"`
	Error     int    `json:"error"`
	Handle    struct {
		Address string `json:"address"`
		Service string `json:"service"`
	} `json:"handle"`
}
type bridgeEnvelope[T any] struct {
	Status int `json:"status"`
	Data   T   `json:"data"`
}

func NewBlueBubbles(origin, password string) (*BlueBubbles, error) {
	u, e := url.Parse(origin)
	if e != nil || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || password == "" {
		return nil, errors.New("a bridge origin and server password are required")
	}
	loopback := u.Hostname() == "127.0.0.1" || u.Hostname() == "::1" || u.Hostname() == "localhost"
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return nil, errors.New("bridge transport requires HTTPS or loopback HTTP")
	}
	return &BlueBubbles{base: strings.TrimRight(origin, "/"), password: password, client: transport()}, nil
}
func (b *BlueBubbles) request(ctx context.Context, method, path string, query url.Values, data, out any) error {
	if query == nil {
		query = url.Values{}
	}
	query.Set("password", b.password)
	var raw []byte
	if data != nil {
		raw, _ = json.Marshal(data)
	}
	req, e := http.NewRequest(method, b.base+path+"?"+query.Encode(), bytes.NewReader(raw))
	if e != nil {
		return errors.New("invalid bridge request")
	}
	if data != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return exchange(ctx, b.client, req, out)
}
func ChatGUID(address string) string { return "iMessage;-;" + address }
func (b *BlueBubbles) Chat(ctx context.Context, guid string) (BridgeChat, error) {
	var out bridgeEnvelope[BridgeChat]
	e := b.request(ctx, "GET", "/api/v1/chat/"+url.PathEscape(guid), nil, nil, &out)
	if e == nil && (out.Status != 200 || out.Data.GUID != guid || out.Data.Service != "iMessage" || !strings.HasPrefix(guid, "iMessage;-;")) {
		e = &Failure{Code: "IMESSAGE_CHAT_INCOMPATIBLE"}
	}
	return out.Data, e
}
func (b *BlueBubbles) Submit(ctx context.Context, in Request) (Receipt, error) {
	if in.Channel != "imessage" {
		return Receipt{}, &Failure{Code: "UNSUPPORTED_CHANNEL"}
	}
	// Compatibility is rechecked before every submission. No SMS or "any" fallback.
	if _, e := b.Chat(ctx, in.To); e != nil {
		return Receipt{}, &Failure{Code: "IMESSAGE_CHAT_UNAVAILABLE"}
	}
	var out bridgeEnvelope[BridgeMessage]
	e := b.request(ctx, "POST", "/api/v1/message/text", nil, map[string]string{
		"chatGuid": in.To, "tempGuid": in.ID, "message": in.Body, "method": "apple-script",
	}, &out)
	if e != nil {
		return Receipt{}, e
	}
	if out.Status != 200 || out.Data.GUID == "" || !out.Data.FromMe || out.Data.Error != 0 || out.Data.Handle.Service != "iMessage" {
		return Receipt{}, &Failure{Code: "IMESSAGE_SUBMISSION_UNCONFIRMED", Ambiguous: true}
	}
	return Receipt{ID: out.Data.GUID, Status: "accepted"}, nil
}
func (b *BlueBubbles) Message(ctx context.Context, guid string) (BridgeMessage, error) {
	var out bridgeEnvelope[BridgeMessage]
	e := b.request(ctx, "GET", "/api/v1/message/"+url.PathEscape(guid), nil, nil, &out)
	if e == nil && (out.Status != 200 || out.Data.GUID != guid || out.Data.Handle.Service != "iMessage") {
		e = errors.New("bridge message identity or service mismatch")
	}
	return out.Data, e
}
func (b *BlueBubbles) History(ctx context.Context, guid string) ([]BridgeMessage, error) {
	if _, e := b.Chat(ctx, guid); e != nil {
		return nil, e
	}
	var out bridgeEnvelope[[]BridgeMessage]
	e := b.request(ctx, "GET", "/api/v1/chat/"+url.PathEscape(guid)+"/message", url.Values{"limit": {strconv.Itoa(100)}, "sort": {"DESC"}}, nil, &out)
	if e == nil && (out.Status != 200 || out.Data == nil || len(out.Data) > 100) {
		e = errors.New("bridge history response is incomplete")
	}
	return out.Data, e
}

type WithIMessage struct {
	Default Gateway
	Bridge  *BlueBubbles
}

func (g WithIMessage) Submit(ctx context.Context, in Request) (Receipt, error) {
	if in.Channel == "imessage" {
		return g.Bridge.Submit(ctx, in)
	}
	return g.Default.Submit(ctx, in)
}
