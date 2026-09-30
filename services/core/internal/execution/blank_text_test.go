package execution

import (
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/engine"
)

// The Claude bridge test reads the same table, so both sides must agree on
// every member and non-member.
func TestBlankTextMatchesClaudeBridgeTable(t *testing.T) {
	raw, err := os.ReadFile("../../../../packages/claude-sdk-adapter/tests/blank-text.json")
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		Members    []string `json:"members"`
		NonMembers []string `json:"non_members"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}
	parse := func(values []string) map[rune]bool {
		set := map[rune]bool{}
		for _, value := range values {
			code, err := strconv.ParseUint(strings.TrimPrefix(value, "U+"), 16, 32)
			if err != nil {
				t.Fatal(value, err)
			}
			set[rune(code)] = true
		}
		return set
	}
	members, nonMembers := parse(table.Members), parse(table.NonMembers)
	if len(members) != 26 || !members['\ufeff'] || !members['\u0085'] || !nonMembers['\u200b'] || !nonMembers['\u180e'] {
		t.Fatalf("unexpected table: %v %v", members, nonMembers)
	}
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if blankTextRune(r) != members[r] {
			t.Fatalf("U+%04X: predicate %v, table %v", r, blankTextRune(r), members[r])
		}
		if unicode.IsSpace(r) && !members[r] {
			t.Fatalf("U+%04X is Go whitespace but not in the table", r)
		}
	}
	claude, _ := (engine.Catalog{}).Lookup("claude_sdk")
	var all strings.Builder
	for r := range members {
		all.WriteRune(r)
		if !errors.Is(validateMessageTextProfile(claude, proto.TextInput(string(r))), ErrWhitespaceOnlyText) {
			t.Fatalf("U+%04X admitted", r)
		}
	}
	if !errors.Is(validateMessageTextProfile(claude, proto.TextInput(all.String())), ErrWhitespaceOnlyText) {
		t.Fatal("all members admitted")
	}
	for r := range nonMembers {
		if err := validateMessageTextProfile(claude, proto.TextInput(" "+string(r)+"\ufeff")); err != nil {
			t.Fatalf("U+%04X rejected: %v", r, err)
		}
	}
}
