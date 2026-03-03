package discord

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPost_SendsJSON(t *testing.T) {
	var received WebhookPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &received)
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected application/json, got %s", r.Header.Get("Content-Type"))
		}
		w.WriteHeader(http.StatusNoContent) // Discord returns 204
	}))
	defer srv.Close()

	payload := WebhookPayload{
		Embeds: []Embed{{Title: "test embed", Color: 3066993}},
	}

	if err := Post(srv.URL, payload, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if received.Embeds[0].Title != "test embed" {
		t.Errorf("expected 'test embed', got %q", received.Embeds[0].Title)
	}
}

func TestPost_WithThreadID(t *testing.T) {
	var capturedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	payload := WebhookPayload{Embeds: []Embed{{Title: "t"}}}
	Post(srv.URL, payload, "thread-123")

	if capturedURL != "/?thread_id=thread-123" {
		t.Errorf("expected thread_id in URL, got %s", capturedURL)
	}
}

func TestPost_ErrorOnBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	payload := WebhookPayload{Embeds: []Embed{{Title: "t"}}}
	if err := Post(srv.URL, payload, ""); err == nil {
		t.Error("expected error on non-2xx status")
	}
}
