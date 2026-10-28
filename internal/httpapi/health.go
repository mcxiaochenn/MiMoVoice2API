package httpapi

import "net/http"

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{
		Status:  "ok",
		Version: s.build.Version,
		Commit:  s.build.Commit,
	})
}
