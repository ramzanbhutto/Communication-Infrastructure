package provider

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"time"
)

// SIPPeer is a local OPTIONS responder for protocol health testing, not a PBX.
type SIPPeer struct {
	Address string
	conn    *net.UDPConn
	done    chan struct{}
	close   sync.Once
}

func NewSIPPeer() (*SIPPeer, error) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		return nil, err
	}
	p := &SIPPeer{Address: conn.LocalAddr().String(), conn: conn, done: make(chan struct{})}
	go p.run()
	return p, nil
}
func (p *SIPPeer) Close() error {
	var err error
	p.close.Do(func() { err = p.conn.Close(); <-p.done })
	return err
}
func (p *SIPPeer) run() {
	defer close(p.done)
	buf := make([]byte, 8192)
	for {
		n, peer, err := p.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		lines := strings.Split(string(buf[:n]), "\r\n")
		if len(lines) == 0 || !strings.HasPrefix(lines[0], "OPTIONS ") {
			continue
		}
		var headers strings.Builder
		for _, line := range lines[1:] {
			lower := strings.ToLower(line)
			if strings.HasPrefix(lower, "via:") || strings.HasPrefix(lower, "from:") || strings.HasPrefix(lower, "to:") || strings.HasPrefix(lower, "call-id:") || strings.HasPrefix(lower, "cseq:") {
				headers.WriteString(line + "\r\n")
			}
		}
		reply := "SIP/2.0 200 OK\r\n" + headers.String() + "Allow: OPTIONS\r\nContent-Length: 0\r\n\r\n"
		p.conn.WriteToUDP([]byte(reply), peer)
	}
}

type SIPObservation struct {
	State       string    `json:"state"`
	Transport   string    `json:"transport"`
	Status      string    `json:"status"`
	RoundTripMs float64   `json:"roundTripMs"`
	CheckedAt   time.Time `json:"checkedAt"`
	Source      string    `json:"source"`
	Limitation  string    `json:"limitation"`
}

func ProbeSIP(ctx context.Context, address string) (SIPObservation, error) {
	host, _, err := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() {
		return SIPObservation{}, errors.New("SIP lab probes require a loopback literal address")
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "udp", address)
	if err != nil {
		return SIPObservation{}, errors.New("SIP peer unavailable")
	}
	defer conn.Close()
	deadline := time.Now().Add(time.Second)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	conn.SetDeadline(deadline)
	id := random("options-")
	local := conn.LocalAddr().String()
	payload := "OPTIONS sip:lab@" + address + " SIP/2.0\r\nVia: SIP/2.0/UDP " + local + ";branch=z9hG4bK" + id + "\r\nMax-Forwards: 1\r\nFrom: <sip:monitor@localhost>;tag=" + id + "\r\nTo: <sip:lab@localhost>\r\nCall-ID: " + id + "\r\nCSeq: 1 OPTIONS\r\nContent-Length: 0\r\n\r\n"
	start := time.Now()
	if _, err = conn.Write([]byte(payload)); err != nil {
		return SIPObservation{}, errors.New("SIP probe write failed")
	}
	buf := make([]byte, 8192)
	n, err := conn.Read(buf)
	if err != nil {
		return SIPObservation{}, errors.New("SIP response unconfirmed")
	}
	reply := string(buf[:n])
	if !strings.HasPrefix(reply, "SIP/2.0 200 OK\r\n") || !strings.Contains(reply, "\r\nCall-ID: "+id+"\r\n") || !strings.Contains(reply, "\r\nCSeq: 1 OPTIONS\r\n") {
		return SIPObservation{}, errors.New("SIP response did not match the probe")
	}
	return SIPObservation{State: "responsive", Transport: "UDP loopback", Status: "200 OK", RoundTripMs: float64(time.Since(start).Microseconds()) / 1000, CheckedAt: time.Now().UTC(), Source: "Actual local SIP OPTIONS exchange", Limitation: "Tests signaling response only. Does not verify registration, RTP audio, TLS or carrier routing."}, nil
}
