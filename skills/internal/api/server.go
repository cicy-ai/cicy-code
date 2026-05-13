package api

import (
	"encoding/json"
	"net/http"

	"github.com/cicy-ai/cicy-skills/internal/config"
	"github.com/cicy-ai/cicy-skills/internal/skills"
	"github.com/cicy-ai/cicy-skills/internal/version"
)

func NewServer(cfg config.Config) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"service": "cicy-skillsd",
			"version": version.Version,
		})
	})

	mux.HandleFunc("/v1/config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, cfg.Public())
	})

	mux.HandleFunc("/v1/skills", func(w http.ResponseWriter, r *http.Request) {
		list, err := skills.Scan(cfg.SkillRoots)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error": err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"skills": list,
			"count":  len(list),
		})
	})

	mux.HandleFunc("/v1/nodes", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"default_node": cfg.DefaultNode,
			"nodes":        cfg.Public().Nodes,
			"count":        len(cfg.Nodes),
		})
	})

	return withAuth(cfg, mux)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
