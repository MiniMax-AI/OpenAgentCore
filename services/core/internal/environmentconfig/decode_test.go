package environmentconfig

import (
	"errors"
	"reflect"
	"testing"
)

func TestDecode(t *testing.T) {
	for _, test := range []struct {
		name string
		data string
		want []SetupCommand
	}{
		{"valid", `[{"command":"true","cwd":"/workspace"}]`, []SetupCommand{{Command: "true", CWD: "/workspace"}}},
		{"invalid JSON", `[{"command":`, nil},
		{"removed field", `[{"command":"true","shell":"bash"}]`, nil},
		{"wrong shape", `{"command":"true"}`, nil},
		{"trailing value", `[] []`, nil},
	} {
		var got []SetupCommand
		err := Decode([]byte(test.data), &got)
		if test.want == nil {
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("%s: got %v, want ErrInvalid", test.name, err)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, test.want) {
			t.Errorf("%s: got %+v, %v", test.name, got, err)
		}
	}
}
