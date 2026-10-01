package sandboxbootstrap

import (
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
)

const valid = `{
  "version": 1,
  "link_url": "wss://core.example.com/api/v1/sandbox-link",
  "credential": "serve-credential",
  "resource": {
    "tenant_id": "11111111-1111-4111-8111-111111111111",
    "environment_id": "22222222-2222-4222-8222-222222222222",
    "kind": "allocation",
    "id": "33333333-3333-4333-8333-333333333333",
    "generation": 2
  }
}`

func TestDecode(t *testing.T) {
	in, err := Decode([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	ref := in.Resource.Ref()
	if ref.Kind != sandboxlink.ResourceAllocation || ref.Generation != 2 || ref.ID.String() != "33333333333343338333333333333333" {
		t.Fatalf("decoded %+v", in)
	}
	raw, err := in.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if again, err := Decode(raw); err != nil || again.Resource != in.Resource {
		t.Fatalf("round trip: %+v %v", again, err)
	}
	if _, err := Decode([]byte(strings.Replace(valid, "wss://core.example.com", "ws://127.0.0.1:8080", 1))); err != nil {
		t.Fatalf("loopback relay URL: %v", err)
	}
}

func TestDecodeRejects(t *testing.T) {
	cases := map[string][2]string{
		"unknown field":           {`"version": 1,`, `"version": 1, "workload": {"uid": 0},`},
		"unknown nested field":    {`"generation": 2`, `"generation": 2, "extra": 1`},
		"missing field":           {`"credential": "serve-credential",`, ``},
		"missing nested field":    {`"kind": "allocation",`, ``},
		"duplicate field":         {`"version": 1,`, `"version": 1, "version": 1,`},
		"case alias":              {`"link_url"`, `"Link_URL"`},
		"other version":           {`"version": 1`, `"version": 2`},
		"plain websocket":         {`wss://`, `ws://`},
		"credential in URL":       {`wss://core`, `wss://user:secret@core`},
		"unknown kind":            {`"allocation"`, `"machine"`},
		"zero generation":         {`"generation": 2`, `"generation": 0`},
		"non-canonical id":        {`33333333-3333-4333-8333-333333333333`, `33333333333343338333333333333333`},
		"exports field":           {`"version": 1,`, `"version": 1, "exports": [{"id": "world", "root": "/"}],`},
		"trailing document":       {"  }\n}", "  }\n} {}"},
		"credential with a space": {`"serve-credential"`, `"serve credential"`},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			doc := strings.Replace(valid, edit[0], edit[1], 1)
			if doc == valid {
				t.Fatal("edit did not apply")
			}
			if _, err := Decode([]byte(doc)); err != ErrInvalid {
				t.Fatalf("decode: %v, want ErrInvalid", err)
			}
		})
	}
}
