package publicorigin

import (
	"errors"
	"testing"
)

func TestNew(t *testing.T) {
	for _, value := range []string{"https://core.example", "https://core.example:8443", "http://localhost:8091", "http://127.0.0.2:8091", "http://[::1]:8091", "https://[2001:db8::1]", "http://core.example", "http://core:8091", "http://10.0.0.5:8080"} {
		if origin, err := New(value); err != nil || origin.String() != value {
			t.Errorf("valid Core URL %q rejected: %v", value, err)
		}
	}
	for value, socket := range map[string]string{"https://core.example": "wss://core.example/api/v1/agent-daemon/ws", "http://[::1]:8091": "ws://[::1]:8091/api/v1/agent-daemon/ws"} {
		origin, err := New(value)
		if err != nil || origin.DaemonWebSocket() != socket || origin.API() != value+"/v1" || origin.RuntimeAPI() != value+"/api/v1" || origin.InstallerBase() != value+"/api/v1/agent-daemon/install/" {
			t.Errorf("addresses derived from %q: %+v %v", value, origin, err)
		}
	}
	for _, value := range []string{
		"", "ftp://core.example", "ws://core.example", "https://core.example/", "https://user:secret@core.example",
		"https://core.example/path", "https://core.example?", "https://core.example?q=x", "https://core.example#x", "https://core.example#",
		"https://CORE.example", "https://core.example:", "https://core.example:0", "https://core.example:65536", "https://core.example:0080",
		"https://core.example\\evil", "https://[not-an-ip]", "https://-core.example", "https://core..example", "https://core_example",
		"https://core.example.", "https://bücher.example", "https://core.example:0443",
	} {
		if _, err := New(value); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid Core URL %q accepted: %v", value, err)
		}
	}
}
