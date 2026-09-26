package v1_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	client "github.com/MiniMax-AI-Dev/parsar/packages/agents-client/v1"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// The official-client harness starts the actual service with a dedicated test
// database and fresh tenant keys, then supplies these explicit test variables.
func TestService(t *testing.T) {
	base, key, otherKey := os.Getenv("OAC_TEST_CLIENT_BASE_URL"), os.Getenv("OAC_TEST_CLIENT_KEY"), os.Getenv("OAC_TEST_CLIENT_OTHER_KEY")
	if base == "" && key == "" && otherKey == "" {
		t.Skip("real service test is run by services/agents-api/tests/official_client.py")
	}
	if base == "" || key == "" || otherKey == "" {
		t.Fatal("all real service test settings are required")
	}
	newClient := func(token string) openai.BetaAgentSessionService {
		t.Helper()
		c, err := client.New(client.Config{BaseURL: base, APIKey: token})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	a, b, invalid := newClient(key), newClient(otherKey), newClient("invalid-key")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	input := openai.BetaAgentSessionNewParams{
		Agent:       openai.BetaAgentSessionNewParamsAgent{Model: openai.String("go-client-test-model"), Instructions: openai.String("Keep this configuration.")},
		Environment: openai.EnvironmentParamUnion{OfParamNone: &openai.EnvironmentParamNone{}},
		Input:       openai.BetaAgentSessionNewParamsInputUnion{OfString: openai.String("Verify the Go client's queued creation and retry identity.")},
		Metadata:    map[string]string{"workspace": "not-an-identity"},
	}
	retry := option.WithHeader("Idempotency-Key", "go-client-first")
	first, err := a.New(ctx, input, retry)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "" || first.Object != "agent.session" || first.Status != "in_progress" || first.Agent.Model != "go-client-test-model" || first.Agent.Instructions != "Keep this configuration." || first.Environment.Type != "none" {
		t.Fatal("incorrect resolved Session")
	}
	read, err := a.Get(ctx, first.ID)
	if err != nil || read.RawJSON() != first.RawJSON() {
		t.Fatalf("retrieve: %v", err)
	}
	replay, err := a.New(ctx, input, retry)
	if err != nil || replay.ID != first.ID {
		t.Fatalf("retry: %v", err)
	}
	turns, err := a.Turns.List(ctx, first.ID, openai.BetaAgentSessionTurnListParams{})
	if err != nil || len(turns.Data) != 1 || turns.Data[0].Status != "queued" {
		t.Fatalf("creation retry must retain one queued Turn: %v", err)
	}
	expectStatus := func(err error, status int) {
		t.Helper()
		var apiErr *openai.Error
		if !errors.As(err, &apiErr) || apiErr.StatusCode != status || apiErr.Code == "" {
			t.Fatalf("expected SDK HTTP %d error: %v", status, err)
		}
	}
	changed := input
	changed.Agent.Instructions = openai.String("Changed")
	_, err = a.New(ctx, changed, retry)
	expectStatus(err, 409)
	for _, key := range []string{"go-client-second", "go-client-third"} {
		if _, err := a.New(ctx, input, option.WithHeader("Idempotency-Key", key)); err != nil {
			t.Fatal(err)
		}
	}
	list := func(order openai.BetaAgentSessionListParamsOrder) []string {
		t.Helper()
		page, err := a.List(ctx, openai.BetaAgentSessionListParams{Limit: openai.Int(1), Order: order})
		var ids []string
		for err == nil && page != nil {
			for _, session := range page.Data {
				ids = append(ids, session.ID)
			}
			if len(ids) > 3 {
				t.Fatal("unexpected or repeating page")
			}
			page, err = page.GetNextPage()
		}
		if err != nil || len(ids) != 3 {
			t.Fatalf("pagination: %v", err)
		}
		return ids
	}
	asc, desc := list(openai.BetaAgentSessionListParamsOrderAsc), list(openai.BetaAgentSessionListParamsOrderDesc)
	slices.Reverse(desc)
	if !slices.Equal(asc, desc) || !slices.Contains(asc, first.ID) {
		t.Fatal("incorrect creation ordering")
	}
	other, err := b.New(ctx, input, retry)
	if err != nil || other.ID == first.ID {
		t.Fatalf("tenant-scoped creation: %v", err)
	}
	_, err = b.Get(ctx, first.ID)
	expectStatus(err, 404)
	_, err = b.List(ctx, openai.BetaAgentSessionListParams{After: openai.String(first.ID)})
	expectStatus(err, 404)
	_, err = invalid.Get(ctx, first.ID)
	// Beta 401s are invalid_request_error with a null code and a request ID (HP-07/HP-23).
	var unauthorized *openai.Error
	if !errors.As(err, &unauthorized) || unauthorized.StatusCode != 401 || unauthorized.Type != "invalid_request_error" ||
		unauthorized.Code != "" || !strings.HasPrefix(unauthorized.Response.Header.Get("X-Request-Id"), "req_") {
		t.Fatalf("expected SDK HTTP 401 error: %v", err)
	}
}
