package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bmardale/stocat/internal/server"
)

const outputPath = "api/openapi.yaml"

func main() {
	if err := run(outputPath); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(path string) error {
	spec, err := server.New(server.Config{}, nil).OpenAPI()
	if err != nil {
		return fmt.Errorf("generate OpenAPI: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create OpenAPI directory: %w", err)
	}
	if err := os.WriteFile(path, spec, 0o644); err != nil {
		return fmt.Errorf("write OpenAPI: %w", err)
	}
	return nil
}
