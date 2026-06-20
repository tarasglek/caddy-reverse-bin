package reversebin

import (
	"os"
	"strings"
	"testing"
)

func TestRepositoryModulePathMatchesGitHubRepository(t *testing.T) {
	// Intent: keep the published Go module path aligned with the release repository.
	data, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}

	firstLine, _, _ := strings.Cut(string(data), "\n")
	if firstLine != "module github.com/tarasglek/caddy-reverse-bin" {
		t.Fatalf("module path = %q, want %q", firstLine, "module github.com/tarasglek/caddy-reverse-bin")
	}
}
