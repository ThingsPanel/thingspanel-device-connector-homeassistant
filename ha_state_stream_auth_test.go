package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestReadUntilAuthOKAcceptsAuthOKMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteJSON(map[string]any{"type": "auth_required"})
		_, _, _ = conn.ReadMessage()
		_ = conn.WriteJSON(map[string]any{"type": "auth_ok", "ha_version": "2024.1.0"})
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if err := readUntilAuthRequired(conn); err != nil {
		t.Fatalf("auth_required: %v", err)
	}
	if err := conn.WriteJSON(map[string]any{"type": "auth", "access_token": "token"}); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	if err := readUntilAuthOK(conn); err != nil {
		t.Fatalf("auth_ok: %v", err)
	}
}
