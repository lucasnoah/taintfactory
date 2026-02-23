package web

import (
	"net/http"
)

// handleTriageDetail renders the triage detail page for a single issue.
// slug is the repo slug (e.g. "owner-repo"), issueStr is the issue number as a string.
func (s *Server) handleTriageDetail(w http.ResponseWriter, r *http.Request, slug, issueStr string) {
	http.Error(w, "triage detail not yet implemented", http.StatusNotImplemented)
}

// handleTriageStream streams the tmux session output for a triage issue via SSE.
func (s *Server) handleTriageStream(w http.ResponseWriter, r *http.Request, slug, issueStr string) {
	http.Error(w, "triage stream not yet implemented", http.StatusNotImplemented)
}
