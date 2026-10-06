package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// IMessageLab is a separate authenticated loopback Mac bridge simulator.
// It never connects to Messages or an Apple account.
type IMessageLab struct {
	Origin, Password string
	mu               sync.Mutex
	messages         map[string]BridgeMessage
	chat             map[string]string
	server           *http.Server
}

func NewIMessageLab() (*IMessageLab, error) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return nil, e
	}
	l := &IMessageLab{Origin: "http://" + listener.Addr().String(), Password: random(""), messages: map[string]BridgeMessage{}, chat: map[string]string{}}
	l.server = &http.Server{Handler: http.HandlerFunc(l.serve), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}
	go l.server.Serve(listener)
	return l, nil
}
func (l *IMessageLab) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return l.server.Shutdown(ctx)
}
func (l *IMessageLab) Client() (*BlueBubbles, error) { return NewBlueBubbles(l.Origin, l.Password) }
func (l *IMessageLab) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Query().Get("password") != l.Password {
		http.Error(w, "unauthorized", 401)
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/")
	write := func(data any) { json.NewEncoder(w).Encode(map[string]any{"status": 200, "data": data}) }
	if r.Method == "POST" && path == "message/text" {
		var in struct {
			Chat   string `json:"chatGuid"`
			ID     string `json:"tempGuid"`
			Body   string `json:"message"`
			Method string `json:"method"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16384)
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.Chat != ChatGUID("+12025550102") || in.Method != "apple-script" || in.ID == "" || in.Body == "" {
			http.Error(w, "invalid lab request", 422)
			return
		}
		m, ok := l.messages[in.ID]
		if !ok {
			now := time.Now().UnixMilli()
			m = BridgeMessage{GUID: random("imsg-"), Text: in.Body, FromMe: true, Created: now, Delivered: &now}
			m.Handle.Address = "+12025550102"
			m.Handle.Service = "iMessage"
			l.messages[in.ID] = m
			l.chat[in.ID] = in.Chat
		}
		write(m)
		return
	}
	if r.Method == "GET" && strings.HasPrefix(path, "chat/") {
		guid := strings.TrimPrefix(path, "chat/")
		history := strings.HasSuffix(guid, "/message")
		guid = strings.TrimSuffix(guid, "/message")
		if guid != ChatGUID("+12025550102") {
			http.Error(w, "chat not found", 404)
			return
		}
		if !history {
			write(BridgeChat{GUID: guid, Service: "iMessage"})
			return
		}
		items := []BridgeMessage{}
		for key, m := range l.messages {
			if l.chat[key] == guid {
				items = append(items, m)
			}
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Created > items[j].Created })
		if len(items) > 100 {
			items = items[:100]
		}
		write(items)
		return
	}
	if r.Method == "GET" && strings.HasPrefix(path, "message/") {
		guid := strings.TrimPrefix(path, "message/")
		for _, m := range l.messages {
			if m.GUID == guid {
				write(m)
				return
			}
		}
		http.Error(w, "message not found", 404)
		return
	}
	http.Error(w, "unsupported bridge request", 405)
}
func (l *IMessageLab) Inbound(body string) error {
	if strings.TrimSpace(body) == "" || len(body) > 16384 {
		return errors.New("bounded reply text is required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	id := random("inbound-")
	m := BridgeMessage{GUID: id, Text: body, Created: time.Now().UnixMilli()}
	m.Handle.Address = "+12025550102"
	m.Handle.Service = "iMessage"
	l.messages[id] = m
	l.chat[id] = ChatGUID("+12025550102")
	return nil
}
func (l *IMessageLab) MarkRead() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now().UnixMilli()
	for id, m := range l.messages {
		if m.FromMe {
			m.Read = &now
			l.messages[id] = m
		}
	}
}

// Hold the provider lock through the database reset commit. A new submission
// cannot be erased between commit and clearing this isolated lab's resources.
func (l *IMessageLab) ResetAfter(commit func(context.Context) error, ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := commit(ctx); err != nil {
		return err
	}
	l.messages = map[string]BridgeMessage{}
	l.chat = map[string]string{}
	return nil
}
