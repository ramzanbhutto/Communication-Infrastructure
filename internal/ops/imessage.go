package ops

import (
	"communication-infrastructure/internal/provider"
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type imessageGenerationKey struct{}

func seedIMessage(ctx context.Context, tx pgx.Tx) error {
	for _, query := range []string{
		`INSERT INTO assets(workspace_id,id,kind,name,address,status) VALUES($1,'bridge-imessage','imessage','Local Mac bridge','demo-mac@example.test','active') ON CONFLICT DO NOTHING`,
		`INSERT INTO channel_permissions(workspace_id,contact_id,channel,granted_at,evidence) VALUES($1,'c102','imessage',now(),'Explicit synthetic iMessage opt-in') ON CONFLICT DO NOTHING`,
	} {
		if _, err := tx.Exec(ctx, query, DemoWorkspace); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) EnsureIMessage(ctx context.Context) error {
	tx, err := s.Begin(ctx, DemoWorkspace, false)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = seedIMessage(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Synchronization reads authenticated bridge resources. No unsigned webhook can
// modify a message, grant consent or suppress a contact.
func (s *Store) syncIMessage(ctx context.Context, workspace, contactID string) error {
	if s.IMessageBridge == nil || workspace != DemoWorkspace {
		return errors.New("local iMessage bridge unavailable for this workspace")
	}
	tx, err := s.BeginRead(ctx, workspace)
	if err != nil {
		return err
	}
	c, err := contactByID(ctx, tx, contactID, false)
	if err != nil {
		tx.Rollback(ctx)
		return err
	}
	var generation int64
	if err = tx.QueryRow(ctx, "SELECT generation FROM demo_state").Scan(&generation); err != nil {
		tx.Rollback(ctx)
		return err
	}
	ctx = context.WithValue(ctx, imessageGenerationKey{}, generation)
	rows, err := tx.Query(ctx, "SELECT id,provider_id,body,destination FROM delivery_jobs WHERE channel='imessage' AND contact_id=$1 AND provider_id IS NOT NULL", c.ID)
	if err != nil {
		tx.Rollback(ctx)
		return err
	}
	type outgoing struct{ id, guid, body, destination string }
	items := []outgoing{}
	for rows.Next() {
		var item outgoing
		if err = rows.Scan(&item.id, &item.guid, &item.body, &item.destination); err != nil {
			rows.Close()
			tx.Rollback(ctx)
			return err
		}
		items = append(items, item)
	}
	rows.Close()
	err = rows.Err()
	tx.Rollback(ctx)
	if err != nil {
		return err
	}
	history, err := s.IMessageBridge.History(ctx, provider.ChatGUID(c.Phone))
	if err != nil {
		return err
	}
	source := "authenticated_local_bridge"
	if s.IMessageLab == nil {
		source = "authenticated_mac_bridge"
	}
	// Process replies first so STOP takes effect before later worker submissions.
	for _, m := range history {
		if m.FromMe {
			continue
		}
		if m.GUID == "" || m.Handle.Service != "iMessage" || m.Handle.Address != c.Phone || len(m.Text) > 16384 {
			return errors.New("bridge reply identity mismatch")
		}
		if strings.TrimSpace(m.Text) == "" {
			continue
		} // Attachments are intentionally unsupported.
		if err = s.persistInboundSource(ctx, "imessage:"+Hash(m.GUID), "imessage", c.Phone, "demo-mac@example.test", "", m.Text, source); err != nil {
			return err
		}
	}
	for _, item := range items {
		m, e := s.IMessageBridge.Message(ctx, item.guid)
		if e != nil {
			return e
		}
		if !m.FromMe || m.Handle.Address != c.Phone || m.Text != item.body || item.destination != provider.ChatGUID(c.Phone) {
			return errors.New("bridge receipt identity mismatch")
		}
		status := ""
		at := time.Time{}
		code := ""
		if m.Error != 0 {
			status = "failed"
			code = "IMESSAGE_ERROR_" + strconv.Itoa(m.Error)
			at = time.UnixMilli(m.Created)
		} else if m.Delivered != nil && *m.Delivered > 0 {
			status = "delivered"
			at = time.UnixMilli(*m.Delivered)
		}
		if status != "" {
			id := "imessage:" + Hash(item.guid+":"+status+":"+at.String())
			if e = s.applyDeliveryEventSource(ctx, id, item.id, item.guid, status, code, at, source); e != nil {
				return e
			}
		}
		if m.Error == 0 && m.Read != nil && *m.Read > 0 {
			at = time.UnixMilli(*m.Read)
			if e = s.applyDeliveryEventSource(ctx, "imessage:"+Hash(item.guid+":read:"+at.String()), item.id, item.guid, "read", "", at, source); e != nil {
				return e
			}
		}
	}
	return nil
}
func (s *Server) imessageCompatibility(w http.ResponseWriter, r *http.Request) {
	if s.Store.IMessageBridge == nil {
		writeJSON(w, 200, map[string]any{"state": "disabled", "compatible": false, "source": "configuration", "checkedAt": time.Now().UTC(), "message": "iMessage is optional. Enable IMESSAGE_LAB=true with INFRA_LAB=true for the isolated Mac bridge demo."})
		return
	}
	tx, err := s.Store.BeginRead(r.Context(), user(r).WorkspaceID)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	c, err := contactByID(r.Context(), tx, r.URL.Query().Get("contactId"), false)
	if err != nil {
		fail(w, err)
		return
	}
	a, err := assetByID(r.Context(), tx, "bridge-imessage", false)
	if err != nil {
		fail(w, err)
		return
	}
	reason, err := deliveryGate(r.Context(), tx, c, a, "imessage")
	if err != nil {
		fail(w, err)
		return
	}
	// Release the database snapshot before calling the external bridge.
	tx.Rollback(r.Context())
	_, err = s.Store.IMessageBridge.Chat(r.Context(), provider.ChatGUID(c.Phone))
	state, text := "compatible", "An existing one-to-one iMessage conversation is available. Recipient reachability is not established."
	if err != nil {
		state, text = "unavailable", "The bridge or this existing iMessage conversation could not be verified. No send is available."
		var failure *provider.Failure
		if errors.As(err, &failure) && failure.Status == 404 {
			state, text = "no_chat", "No existing iMessage chat is available for this contact. No chat will be created automatically."
		}
		if errors.As(err, &failure) && failure.Code == "IMESSAGE_CHAT_INCOMPATIBLE" {
			state, text = "incompatible", "The stored chat does not identify an iMessage service. SMS fallback is disabled."
		}
	}
	writeJSON(w, 200, map[string]any{"state": state, "compatible": err == nil, "eligible": reason == "ELIGIBLE", "reason": reason, "contactId": c.ID, "checkedAt": time.Now().UTC(), "source": "authenticated_local_bridge", "message": text})
}
func (s *Server) imessageSync(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ContactID string `json:"contactId"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.Store.syncIMessage(r.Context(), user(r).WorkspaceID, in.ContactID); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"synced": true, "simulated": s.Store.IMessageLab != nil, "scope": "Latest 100 bridge messages and all known outbound provider IDs for this contact"})
}
func (s *Server) imessageScenario(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind string `json:"kind"`
		Body string `json:"body"`
	}
	if !decode(w, r, &in) {
		return
	}
	if s.Store.IMessageLab == nil {
		writeJSON(w, 503, Problem{Code: "IMESSAGE_DISABLED", Message: "The optional iMessage lab is disabled."})
		return
	}
	switch in.Kind {
	case "reply":
		if ValidateMessage(in.Body) != nil {
			writeJSON(w, 422, Problem{Code: "INVALID_MESSAGE", Message: "Enter bounded reply text."})
			return
		}
		if err := s.Store.IMessageLab.Inbound(in.Body); err != nil {
			fail(w, err)
			return
		}
	case "read":
		s.Store.IMessageLab.MarkRead()
	default:
		writeJSON(w, 422, Problem{Code: "INVALID_SCENARIO", Message: "Choose a reply or read-receipt simulation."})
		return
	}
	if err := s.Store.syncIMessage(r.Context(), user(r).WorkspaceID, "c102"); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"recorded": true, "simulated": true})
}
