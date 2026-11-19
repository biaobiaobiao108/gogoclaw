package gateway

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/biaobiaobiao108/gogoclaw/pkg/agent"
	"github.com/biaobiaobiao108/gogoclaw/pkg/evolution"
)

func evolutionHandler(loop *agent.AgentLoop) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		var result any
		var err error
		status := http.StatusOK
		switch r.Method {
		case http.MethodGet:
			if id := r.URL.Query().Get("draft"); id != "" {
				result, err = loop.ReviewEvolutionDraft(id)
			} else {
				result, err = loop.EvolutionOverview()
			}
		case http.MethodPost:
			var input struct {
				Action  string `json:"action"`
				DraftID string `json:"draft_id"`
				Token   string `json:"review_token"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
			decoder.DisallowUnknownFields()
			if decodeErr := decoder.Decode(&input); decodeErr != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid evolution request"})
				return
			}
			if input.Action == "run" {
				err = loop.RunEvolution()
				status = http.StatusAccepted
			} else {
				err = loop.ActOnEvolutionDraft(r.Context(), input.DraftID, input.Action, input.Token)
			}
			result = map[string]bool{"ok": err == nil}
		default:
			w.Header().Set("Allow", "GET, POST")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err != nil {
			switch {
			case errors.Is(err, evolution.ErrDraftNotFound):
				status = http.StatusNotFound
			case errors.Is(err, evolution.ErrDraftConflict), errors.Is(err, evolution.ErrDraftAction),
				errors.Is(err, evolution.ErrEvolutionBusy):
				status = http.StatusConflict
			default:
				status = http.StatusInternalServerError
			}
			result = map[string]string{"error": err.Error()}
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(result)
	}
}
