package ops

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type User struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspaceId"`
	Name        string `json:"name"`
	Role        string `json:"role"`
}
type Sample struct {
	ID         int64     `json:"id"`
	Attempts   int       `json:"attempts"`
	Filtered   int       `json:"filtered"`
	SpamLabel  bool      `json:"spamLabel"`
	ObservedAt time.Time `json:"observedAt"`
	Source     string    `json:"source"`
}
type EmailConfig struct {
	SPF       bool   `json:"spf"`
	DKIM      bool   `json:"dkim"`
	DMARC     string `json:"dmarc"`
	WarmupDay int    `json:"warmupDay"`
	Source    string `json:"source"`
}
type Asset struct {
	ID               string       `json:"id"`
	Kind             string       `json:"kind"`
	Name             string       `json:"name"`
	Address          string       `json:"address"`
	Status           string       `json:"status"`
	Version          int64        `json:"version"`
	QuarantinedAt    *time.Time   `json:"quarantinedAt"`
	QuarantineReason *string      `json:"quarantineReason"`
	EmailConfig      *EmailConfig `json:"emailConfig"`
	Sample           *Sample      `json:"sample"`
}
type Contact struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Phone         string     `json:"phone"`
	Email         string     `json:"email"`
	DNC           bool       `json:"dnc"`
	OptedOutAt    *time.Time `json:"optedOutAt"`
	SMSConsentAt  *time.Time `json:"smsConsentAt"`
	ConsentSource *string    `json:"consentSource"`
	WarmSignal    *string    `json:"warmSignal"`
	WarmAt        *time.Time `json:"warmAt"`
	EmailOpenedAt *time.Time `json:"emailOpenedAt"`
}
type Decision struct {
	ID           string         `json:"id"`
	ContactID    string         `json:"contactId"`
	ContactLabel string         `json:"contactLabel"`
	AssetID      *string        `json:"assetId"`
	AssetName    *string        `json:"assetName"`
	Channel      string         `json:"channel"`
	Outcome      string         `json:"outcome"`
	Reason       string         `json:"reason"`
	Evidence     map[string]any `json:"evidence"`
	RecordedAt   time.Time      `json:"recordedAt"`
}
type Audit struct {
	ID         int64     `json:"id"`
	ActorName  string    `json:"actorName"`
	Action     string    `json:"action"`
	AssetID    *string   `json:"assetId"`
	RecordID   *string   `json:"recordId"`
	Reason     *string   `json:"reason"`
	RecordedAt time.Time `json:"recordedAt"`
}
type Call struct {
	ID          string     `json:"id"`
	ContactID   string     `json:"contactId"`
	AssetID     string     `json:"assetId"`
	DecisionID  string     `json:"decisionId"`
	Status      string     `json:"status"`
	Outcome     *string    `json:"outcome"`
	StartedAt   time.Time  `json:"startedAt"`
	CompletedAt *time.Time `json:"completedAt"`
}
type Reply struct {
	ID           string    `json:"id"`
	ContactID    string    `json:"contactId"`
	ContactLabel string    `json:"contactLabel"`
	AssetID      string    `json:"assetId"`
	Body         string    `json:"body"`
	Tag          string    `json:"tag"`
	ReceivedAt   time.Time `json:"receivedAt"`
	PersistedAt  time.Time `json:"persistedAt"`
}
type Queue struct {
	State       string    `json:"state"`
	Pending     *int64    `json:"pending"`
	Unpublished int64     `json:"unpublished"`
	Paused      bool      `json:"paused"`
	Error       *string   `json:"error"`
	CheckedAt   time.Time `json:"checkedAt"`
}
type Problem struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	DecisionID string `json:"decisionId,omitempty"`
	Details    any    `json:"details,omitempty"`
}

func (p Problem) Error() string { return p.Message }

type Result struct {
	Status int
	Body   any
}

// These are demonstration policies, not a claim of regulatory compliance.
// An email-open event is never evidence of permission to send SMS.
func Gate(c Contact, a Asset, channel string) string {
	if c.DNC {
		return "DNC_SUPPRESSED"
	}
	if c.OptedOutAt != nil {
		return "OPTED_OUT"
	}
	if a.Kind != "phone" {
		return "PHONE_LINE_REQUIRED"
	}
	if a.Status != "active" {
		return "LINE_QUARANTINED"
	}
	if channel == "sms" {
		if c.SMSConsentAt == nil || c.ConsentSource == nil || strings.TrimSpace(*c.ConsentSource) == "" {
			return "NO_SMS_CONSENT"
		}
		if c.WarmAt == nil || c.WarmSignal == nil || *c.WarmSignal != "inbound_reply" {
			return "NO_WARM_REPLY"
		}
	}
	return "ELIGIBLE"
}
func RestoreProblems(a Asset, s *Sample) []string {
	reasons := []string{}
	if a.Status != "quarantined" {
		return reasons
	}
	if s == nil {
		return append(reasons, "No provider observation is available.")
	}
	if a.QuarantinedAt == nil || !s.ObservedAt.After(*a.QuarantinedAt) {
		reasons = append(reasons, "A new observation after quarantine is required.")
	}
	if s.Attempts < 20 {
		reasons = append(reasons, "At least 20 measured attempts are required.")
	}
	if s.Attempts > 0 && float64(s.Filtered)/float64(s.Attempts) > .05 {
		reasons = append(reasons, "The measured filter rate must be 5% or lower.")
	}
	if s.SpamLabel {
		reasons = append(reasons, "The provider spam label must be cleared.")
	}
	return reasons
}
func ValidateMessage(s string) error {
	n := utf8.RuneCountInString(strings.TrimSpace(s))
	if n < 1 || n > 480 {
		return fmt.Errorf("Enter a message between 1 and 480 characters.")
	}
	if !utf8.ValidString(s) {
		return fmt.Errorf("The message must be valid UTF-8.")
	}
	for _, c := range s {
		if c < 32 && c != '\n' && c != '\t' {
			return fmt.Errorf("The message contains unsupported control characters.")
		}
	}
	return nil
}
func MaskPhone(s string) string {
	if len(s) < 4 {
		return "••••"
	}
	return "••• ••• " + s[len(s)-4:]
}
func MaskEmail(s string) string {
	p := strings.SplitN(s, "@", 2)
	if len(p) != 2 || p[0] == "" {
		return "•••"
	}
	return p[0][:1] + "•••@" + p[1]
}
func CSVSafe(s string) string {
	t := strings.TrimLeft(s, " \t\r\n")
	if t != "" && strings.ContainsRune("=+-@", rune(t[0])) {
		return "'" + s
	}
	return s
}

var reasonText = map[string]string{
	"ASSET_DAILY_CAP":         "The sending asset has reached its UTC daily reservation limit.",
	"NO_IMESSAGE_PERMISSION":  "Explicit iMessage permission is required. SMS or email permission does not grant it.",
	"IMESSAGE_ASSET_REQUIRED": "Choose an active iMessage bridge asset.",
	"NO_EMAIL_PERMISSION":     "Explicit email permission is required in the local lab.",
	"EMAIL_ASSET_REQUIRED":    "Choose an active email asset.",
	"INVALID_EMAIL":           "The contact email address is invalid.",
	"DNC_SUPPRESSED":          "This contact is on the synthetic do-not-contact list.",
	"OPTED_OUT":               "The contact has opted out. Phone outreach is suppressed.",
	"PHONE_LINE_REQUIRED":     "Choose a phone line for this action.",
	"LINE_QUARANTINED":        "This line is quarantined. Choose an active line.",
	"NO_SMS_CONSENT":          "No explicit SMS consent is recorded. An email open does not grant SMS permission.",
	"NO_WARM_REPLY":           "SMS requires a recorded inbound reply as well as explicit consent.",
	"CALL_BUSY":               "Another call is active in this workspace. Wait for its recorded outcome.",
	"TOUCH_LIMIT":             "This contact has reached the demonstration limit of three outreach attempts in 24 hours.",
	"ELIGIBLE":                "The recorded demonstration eligibility checks passed.",
}
