package writeaudit

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func issuedSource(tenant string) Source {
	return Source{KeyID: uuid.NewString(), Prefix: "pc_Ab3_-xyz", Name: "key", Kind: "issued", TenantID: tenant, RequestID: "request", TraceID: "trace"}
}

func TestValidateRecord(t *testing.T) {
	tenant := uuid.NewString()
	agent := []Resource{{Type: "agent", ID: "agent"}}
	if err := ValidateRecord(issuedSource(tenant), tenant, "create", agent); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRecord(issuedSource(tenant), tenant, "update_default_version", []Resource{{Type: "skill_version", ID: "1", ParentID: "skill"}}); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Source, *string, *[]Resource){
		"other tenant":  func(s *Source, _ *string, _ *[]Resource) { s.TenantID = uuid.NewString() },
		"nil tenant":    func(s *Source, _ *string, _ *[]Resource) { s.TenantID = uuid.Nil.String() },
		"key ID":        func(s *Source, _ *string, _ *[]Resource) { s.KeyID = "key" },
		"prefix":        func(s *Source, _ *string, _ *[]Resource) { s.Prefix = "bad" },
		"prefix chars":  func(s *Source, _ *string, _ *[]Resource) { s.Prefix = "pc_Ab3_-xy!" },
		"kind":          func(s *Source, _ *string, _ *[]Resource) { s.Kind = "static" },
		"request":       func(s *Source, _ *string, _ *[]Resource) { s.RequestID = "" },
		"trace":         func(s *Source, _ *string, _ *[]Resource) { s.TraceID = "bad\x01" },
		"name":          func(s *Source, _ *string, _ *[]Resource) { s.Name = strings.Repeat("x", 81) },
		"action":        func(_ *Source, a *string, _ *[]Resource) { *a = "copy" },
		"resource type": func(_ *Source, _ *string, r *[]Resource) { *r = []Resource{{Type: "project", ID: "p"}} },
		"resource ID":   func(_ *Source, _ *string, r *[]Resource) { *r = []Resource{{Type: "agent"}} },
		"resource parent": func(_ *Source, _ *string, r *[]Resource) {
			*r = []Resource{{Type: "agent", ID: "a", ParentID: strings.Repeat("x", 257)}}
		},
	} {
		source, action, resources := issuedSource(tenant), "create", agent
		change(&source, &action, &resources)
		if err := ValidateRecord(source, tenant, Action(action), resources); !errors.Is(err, ErrInvalidSource) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
}

func TestReadQueries(t *testing.T) {
	if f, err := (Filter{}).Validate(); err != nil || f.Limit != 20 {
		t.Fatal(f, err)
	}
	now := time.Now()
	for _, f := range []Filter{{Limit: -1}, {Limit: 101}, {ResourceType: "project"}, {KeyID: strings.Repeat("x", 129)}, {ResourceID: "bad\x00"}, {CreatedAfter: &now, CreatedBefore: &now}} {
		if _, err := f.Validate(); !errors.Is(err, ErrInvalidQuery) {
			t.Errorf("filter %+v accepted", f)
		}
	}
	if err := ValidateOwnerQuery("agent", []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []struct {
		kind string
		ids  []string
	}{{"project", []string{"a"}}, {"agent", nil}, {"agent", make([]string, 101)}, {"agent", []string{""}}} {
		if err := ValidateOwnerQuery(q.kind, q.ids); !errors.Is(err, ErrInvalidQuery) {
			t.Errorf("owner query %s %d accepted", q.kind, len(q.ids))
		}
	}
}
