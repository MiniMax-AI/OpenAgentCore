//go:build linux

package node

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func TestHostHealthReconnectStartsFreshInterval(t *testing.T) {
	hellos := make(chan Health, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		hello, err := readFrame(conn)
		if err != nil || hello.Type != "hello" || hello.Health == nil {
			t.Errorf("invalid hello: %+v, %v", hello, err)
			return
		}
		hellos <- *hello.Health
		if err := writeFrame(conn, frame{Type: "welcome", ConnectionID: uuid.NewString(), OwnerEpoch: 1}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	a := agent{config: AgentConfig{CoreURL: server.URL, StateDirectory: t.TempDir(), Identity: identity(), Probe: probe}, stored: StoredIdentity{OwnerEpoch: 1}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i := 0; i < 2; i++ {
		if err := a.connect(ctx); err == nil {
			t.Fatal("server disconnect was not observed")
		}
		select {
		case h := <-hellos:
			if h.CPUUtilization != nil || h.TotalMemoryBytes == nil || h.ObservedAt.IsZero() {
				t.Fatalf("connection %d inherited a baseline or omitted its observation: %+v", i, h)
			}
		case <-ctx.Done():
			t.Fatal("missing hello")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
