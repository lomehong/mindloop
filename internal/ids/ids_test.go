package ids

import (
	"regexp"
	"strings"
	"testing"
)

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNewUUID(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		id := NewUUID()
		if !uuidRE.MatchString(id) {
			t.Fatalf("not a v4 uuid: %q", id)
		}
		if seen[id] {
			t.Fatalf("duplicate uuid: %q", id)
		}
		seen[id] = true
	}
}

func TestShort(t *testing.T) {
	if got := Short("abcd1234-5678-4abc-9abc-abcd1234abcd", 8); got != "abcd1234" {
		t.Fatalf("Short = %q, want %q", got, "abcd1234")
	}
	if got := Short("abc", 8); got != "abc" {
		t.Fatalf("Short of short id = %q, want %q", got, "abc")
	}
	if strings.Contains(Short("a-b-c-d-e-f", 8), "-") {
		t.Fatalf("Short must strip dashes, got %q", Short("a-b-c-d-e-f", 8))
	}
}
