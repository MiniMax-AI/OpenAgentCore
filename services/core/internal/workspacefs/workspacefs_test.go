package workspacefs

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func testBinding() Binding {
	reference := Reference{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"}
	configuration := Configuration{"44444444-4444-4444-8444-444444444444", "directory", json.RawMessage(`{"root":"/owned"}`)}
	return Binding{configuration, Attachment{reference, configuration.ID, AttachmentHostDirectory, json.RawMessage(`{"object":"receipt"}`)}}
}

func TestBindingOwnership(t *testing.T) {
	binding := testBinding()
	if err := binding.Validate(); err != nil {
		t.Fatal(err)
	}
	expected := binding.Attachment.Reference
	other := "55555555-5555-4555-8555-555555555555"
	for _, field := range []string{"tenant", "environment", "object", "configuration"} {
		t.Run(field, func(t *testing.T) {
			changed := binding
			switch field {
			case "tenant":
				changed.Attachment.Reference.TenantID = other
			case "environment":
				changed.Attachment.Reference.EnvironmentID = other
			case "object":
				changed.Attachment.Reference.ObjectID = other
			case "configuration":
				changed.Attachment.ConfigurationID = other
			}
			if err := ValidateAttachment(expected, binding.Configuration, changed.Attachment); !errors.Is(err, ErrOwnership) {
				t.Fatalf("expected ownership error, got %v", err)
			}
		})
	}
}

func TestRejectInvalidEnvelopes(t *testing.T) {
	for _, value := range []string{"", "00000000-0000-0000-0000-000000000000", "11111111111141118111111111111111", "../workspace", "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA"} {
		binding := testBinding()
		binding.Attachment.Reference.ObjectID = value
		if err := binding.Validate(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted object ID %q: %v", value, err)
		}
	}
	for _, raw := range []string{"", "null", "[]", `"/host/path"`, `{"root":`, "{} {}"} {
		t.Run(raw, func(t *testing.T) {
			binding := testBinding()
			binding.Configuration.Parameters = json.RawMessage(raw)
			if err := binding.Validate(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted parameters: %v", err)
			}
			binding = testBinding()
			binding.Attachment.Native = json.RawMessage(raw)
			if err := binding.Validate(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted receipt: %v", err)
			}
		})
	}
	binding := testBinding()
	binding.Configuration.Adapter = "directory/../../other"
	if !errors.Is(binding.Validate(), ErrInvalid) {
		t.Fatal("accepted adapter path")
	}
	binding = testBinding()
	binding.Attachment.Kind = "public_host_path"
	if !errors.Is(binding.Validate(), ErrInvalid) {
		t.Fatal("accepted unknown attachment kind")
	}
}

func TestValidateCombination(t *testing.T) {
	required := Requirements{AttachmentHostDirectory, true}
	supported := Declaration{AttachmentHostDirectory, true, true}
	for _, tc := range []struct {
		name        string
		declaration Declaration
		capacity    uint32
		want        error
	}{
		{"supported", supported, 512, nil},
		{"no quota requested", Declaration{AttachmentHostDirectory, true, false}, 0, nil},
		{"unenforced quota", Declaration{AttachmentHostDirectory, true, false}, 512, ErrUnsupported},
		{"missing xattrs", Declaration{AttachmentHostDirectory, false, true}, 0, ErrUnsupported},
		{"missing attachment", Declaration{}, 0, ErrUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateCombination(required, tc.declaration, tc.capacity); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
	if err := ValidateCombination(Requirements{Attachment: AttachmentHostDirectory}, Declaration{Attachment: AttachmentHostDirectory}, 0); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCombination(Requirements{Attachment: "unknown"}, supported, 0); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unknown requirement: %v", err)
	}
}

func TestDirectoryValidation(t *testing.T) {
	for _, path := range []string{"", "relative", "/owned/../other", "/owned\x00/path"} {
		if !errors.Is((Directory{path}).Validate(), ErrInvalid) {
			t.Fatalf("accepted path %q", path)
		}
	}
	if err := (Directory{"/owned/workspace"}).Validate(); err != nil {
		t.Fatal(err)
	}
	// Noncanonical UUID encodings must not create alternate ownership keys.
	binding := testBinding()
	binding.Configuration.ID = strings.ReplaceAll(binding.Configuration.ID, "-", "")
	if !errors.Is(binding.Validate(), ErrInvalid) {
		t.Fatal("accepted compact configuration ID")
	}
}
