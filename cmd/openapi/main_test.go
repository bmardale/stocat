package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api", "openapi.yaml")
	if err := run(path); err != nil {
		t.Fatal(err)
	}
	spec, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"openapi: 3.1.0", "/auth/register:", "/healthz:", "/admin/storage-backends:"} {
		if !strings.Contains(string(spec), want) {
			t.Errorf("OpenAPI document does not contain %q", want)
		}
	}
}
