package proto

import (
	"strings"
	"testing"
)

func TestPortableSourceDirectories(t *testing.T) {
	for _, value := range []string{"/", "/home/operator/skills", `C:\`, `C:\Users\operator\skills`, "D:/skills", `\\host\share`, `\\host\share\skills`, "//host/share/skills"} {
		if ValidateSourceDirectories([]string{value}) != nil {
			t.Fatal("valid source refused", value)
		}
	}
	for _, value := range []string{"", "relative", "C:relative", `\rooted`, `\\host`, `\\?\C:\skills`, `/a/../b`, `C:\a\..\b`, "/a/", "/a\tb", "/a\x7fb", string([]byte{'/', 0xff}), strings.Repeat("a", 4097)} {
		if ValidateSourceDirectories([]string{value}) == nil {
			t.Fatal("invalid source accepted", value)
		}
	}
	if ValidateSourceDirectories([]string{"/a", "/a"}) == nil || ValidateSourceDirectories(make([]string, 51)) == nil {
		t.Fatal("source bounds not enforced")
	}
}
