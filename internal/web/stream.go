package web

import (
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// handleSessionStream serves a Server-Sent Events stream of the current tmux
// pane output for the active session on the given pipeline issue.
// It polls tmux capture-pane every 2 seconds and sends the full pane content
// as a single SSE message. When the session ends it sends a "done" event.
func (s *Server) handleSessionStream(w http.ResponseWriter, r *http.Request, issueStr string) {
	issue, err := strconv.Atoi(issueStr)
	if err != nil {
		http.Error(w, "invalid issue", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering if present

	sendDone := func(reason string) {
		fmt.Fprintf(w, "event: done\ndata: %s\n\n", reason)
		flusher.Flush()
	}

	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}

		ps, err := s.store.Get(issue)
		if err != nil {
			sendDone("pipeline not found")
			return
		}
		if ps.CurrentSession == "" {
			sendDone("no active session")
			return
		}

		output, err := capturePane(ps.CurrentSession)
		if err != nil {
			// tmux session no longer exists
			sendDone("session ended")
			return
		}

		// Send full pane content as one SSE message (multiple data: lines join with \n on client)
		for _, line := range strings.Split(output, "\n") {
			fmt.Fprintf(w, "data: %s\n", line)
		}
		fmt.Fprintf(w, "\n")
		flusher.Flush()
	}
}

// capturePane runs tmux capture-pane and returns the visible pane content
// with ANSI escape sequences stripped.
func capturePane(sessionName string) (string, error) {
	out, err := exec.Command(
		"tmux", "capture-pane",
		"-t", sessionName,
		"-p",       // print to stdout
		"-S", "-500", // include last 500 lines of scrollback
	).Output()
	if err != nil {
		return "", err
	}
	return stripANSI(string(out)), nil
}
