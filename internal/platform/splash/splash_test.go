package splash

import (
	"bytes"
	"strings"
	"testing"
)

func TestProfileFromEnv(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want profile
	}{
		{"no color wins", map[string]string{"NO_COLOR": "1", "COLORTERM": "truecolor"}, noColor},
		{"dumb terminal", map[string]string{"TERM": "dumb"}, noColor},
		{"truecolor", map[string]string{"TERM": "xterm-256color", "COLORTERM": "truecolor"}, trueColor},
		{"24bit", map[string]string{"COLORTERM": "24bit"}, trueColor},
		{"256 colors", map[string]string{"TERM": "xterm-256color"}, ansi256},
		{"basic", map[string]string{"TERM": "xterm"}, ansi16},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(key string) string { return tt.env[key] }
			if got := profileFromEnv(getenv); got != tt.want {
				t.Errorf("profileFromEnv() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDetectIgnoresNonTerminal(t *testing.T) {
	getenv := func(string) string { return "truecolor" }
	if got := detect(&bytes.Buffer{}, getenv); got != noColor {
		t.Errorf("detect() = %v, want %v", got, noColor)
	}
}

func TestRender(t *testing.T) {
	tests := []struct {
		name    string
		profile profile
		want    string
	}{
		{"truecolor", trueColor, "\x1b[38;2;"},
		{"256 colors", ansi256, "\x1b[38;5;"},
		{"basic", ansi16, "\x1b[95m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := render(tt.profile, "v1.2.3")
			if !strings.Contains(got, tt.want) {
				t.Errorf("render() has no %q sequence", tt.want)
			}
			if !strings.Contains(got, "\x1b[0m") {
				t.Error("render() has no reset sequence")
			}
		})
	}
}

func TestRenderPlain(t *testing.T) {
	got := render(noColor, "v1.2.3")
	if strings.Contains(got, "\x1b") {
		t.Errorf("render() = %q, want no escape sequences", got)
	}
	for _, want := range []string{"( o.o )", "███████╗", "v1.2.3"} {
		if !strings.Contains(got, want) {
			t.Errorf("render() has no %q", want)
		}
	}
}

func TestCubeLevel(t *testing.T) {
	tests := []struct {
		in   uint8
		want int
	}{
		{0, 0}, {47, 0}, {48, 1}, {95, 1}, {115, 2}, {175, 3}, {215, 4}, {255, 5},
	}
	for _, tt := range tests {
		if got := cubeLevel(tt.in); got != tt.want {
			t.Errorf("cubeLevel(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
