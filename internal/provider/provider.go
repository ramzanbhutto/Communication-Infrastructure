// Package provider contains transport contracts, not eligibility policy.
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Request struct {
	ID, Channel, From, To, Body, Subject, Callback, Scenario string
}
type Receipt struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}
type Failure struct {
	Code      string
	Status    int
	Retryable bool
	Ambiguous bool
}

func (f *Failure) Error() string { return f.Code }

type Gateway interface {
	Submit(context.Context, Request) (Receipt, error)
}

// Only an explicit HTTP 429 is treated as a definite retryable rejection.
// A timeout, 5xx or unreadable successful response may have created a resource.
func exchange(ctx context.Context, client *http.Client, req *http.Request, out any) error {
	req = req.WithContext(ctx)
	res, err := client.Do(req)
	if err != nil {
		return &Failure{Code: "PROVIDER_UNCONFIRMED", Ambiguous: true}
	}
	defer res.Body.Close()
	if res.StatusCode == 429 {
		return &Failure{Code: "PROVIDER_THROTTLED", Retryable: true}
	}
	if res.StatusCode >= 500 {
		return &Failure{Code: "PROVIDER_UNCONFIRMED", Ambiguous: true}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return &Failure{Code: "PROVIDER_REJECTED", Status: res.StatusCode}
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(out); err != nil {
		return &Failure{Code: "PROVIDER_RESPONSE_INVALID", Ambiguous: true}
	}
	return nil
}

func transport() *http.Client {
	return &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("provider redirects are disabled") }}
}

// Endpoint overrides are restricted to loopback for deterministic contract tests.
func endpoint(raw, production string) (string, error) {
	if raw == "" {
		return production, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "::1") {
		return "", errors.New("endpoint override must be a loopback HTTP origin")
	}
	return strings.TrimRight(raw, "/"), nil
}
