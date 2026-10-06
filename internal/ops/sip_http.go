package ops

import (
	"communication-infrastructure/internal/provider"
	"encoding/json"
	"net/http"
)

func (s *Server) sipProbe(w http.ResponseWriter, r *http.Request) {
	var in struct{}
	if !decode(w, r, &in) {
		return
	}
	if s.Store.SIPPeer == nil {
		writeJSON(w, 503, Problem{Code: "SIP_LAB_DISABLED", Message: "No local SIP peer is configured."})
		return
	}
	observation, err := provider.ProbeSIP(r.Context(), s.Store.SIPPeer.Address)
	if err != nil {
		writeJSON(w, 503, Problem{Code: "SIP_UNCONFIRMED", Message: "No matching SIP response was confirmed. The last stored observation is historical."})
		return
	}
	tx, err := s.Store.Begin(r.Context(), user(r).WorkspaceID, false)
	if err != nil {
		fail(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	raw, _ := json.Marshal(observation)
	if _, err = tx.Exec(r.Context(), "INSERT INTO infrastructure_sip(workspace_id,observation) VALUES($1,$2) ON CONFLICT(workspace_id) DO UPDATE SET observation=excluded.observation,checked_at=clock_timestamp()", user(r).WorkspaceID, raw); err != nil {
		fail(w, err)
		return
	}
	if err = audit(r.Context(), tx, user(r), "sip.options_checked", nil, nil, "Local SIP OPTIONS matched a 200 OK response. Audio and carrier routing were not tested."); err != nil {
		fail(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"observation": observation})
}
