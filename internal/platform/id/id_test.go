package id_test

import (
	"strings"
	"testing"
	"time"

	"github.com/bmardale/stocat/internal/platform/id"
)

func TestNew(t *testing.T) {
	t.Parallel()
	value := id.New(id.User)
	if !strings.HasPrefix(value, "usr_") {
		t.Fatalf("got %q, want the prefix %q", value, "usr_")
	}
	if len(value) != len("usr_")+26 {
		t.Fatalf("got length %d, want %d", len(value), len("usr_")+26)
	}
	if !id.Valid(id.User, value) {
		t.Fatalf("new identifier %q is not valid", value)
	}
}

func TestNewIsUnique(t *testing.T) {
	t.Parallel()
	seen := make(map[string]bool, 1000)
	for range 1000 {
		value := id.New(id.User)
		if seen[value] {
			t.Fatalf("duplicate identifier %q", value)
		}
		seen[value] = true
	}
}

func TestNewSortsByTime(t *testing.T) {
	t.Parallel()
	first := id.New(id.User)
	time.Sleep(2 * time.Millisecond)
	second := id.New(id.User)
	if first >= second {
		t.Fatalf("got %q before %q, want the older identifier first", second, first)
	}
}

func TestValid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		prefix string
		value  string
		want   bool
	}{
		{"new identifier", id.User, id.New(id.User), true},
		{"lowest value", id.User, "usr_" + strings.Repeat("0", 26), true},
		{"highest value", id.User, "usr_7" + strings.Repeat("Z", 25), true},
		{"other prefix", id.User, "org_01K4W9T5V8QK3M7ZB0YHXC2FNE", false},
		{"no prefix", id.User, "01K4W9T5V8QK3M7ZB0YHXC2FNE", false},
		{"no separator", id.User, "usr01K4W9T5V8QK3M7ZB0YHXC2FNE", false},
		{"too short", id.User, "usr_01K4W9T5V8QK3M7ZB0YHXC2FN", false},
		{"too long", id.User, "usr_01K4W9T5V8QK3M7ZB0YHXC2FNEE", false},
		{"overflow", id.User, "usr_8" + strings.Repeat("0", 25), false},
		{"letter outside the alphabet", id.User, "usr_01K4W9T5V8QK3M7ZB0YHXC2FNU", false},
		{"lowercase", id.User, "usr_01k4w9t5v8qk3m7zb0yhxc2fne", false},
		{"empty", id.User, "", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := id.Valid(test.prefix, test.value); got != test.want {
				t.Fatalf("Valid(%q, %q) = %v, want %v", test.prefix, test.value, got, test.want)
			}
		})
	}
}
