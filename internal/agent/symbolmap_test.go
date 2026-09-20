package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/treeol/wakil/internal/lsp"
)

// TestSymbolMap_DiscoverSourceFiles verifies the file enumeration logic:
// finds .go files, skips vendor/testdata/_test.go/generated.
func TestSymbolMap_DiscoverSourceFiles(t *testing.T) {
	fe := newFakeExecutor()
	fe.files["main.go"] = "package main"
	fe.files["app.go"] = "package main"
	fe.files["main_test.go"] = "package main"
	fe.files["gen.pb.go"] = "package main"
	fe.dirs["vendor"] = true
	fe.dirs["vendor/foo"] = true
	fe.files["vendor/foo/foo.go"] = "package foo"
	fe.dirs["testdata"] = true
	fe.files["testdata/input.go"] = "package testdata"
	fe.dirs["internal"] = true
	fe.dirs["internal/agent"] = true
	fe.files["internal/agent/handler.go"] = "package agent"

	files, err := discoverSourceFiles(context.Background(), fe, "go")
	if err != nil {
		t.Fatal(err)
	}

	if len(files) != 3 {
		t.Errorf("expected 3 source files, got %d: %v", len(files), files)
	}

	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			t.Errorf("test file should be excluded: %s", f)
		}
		if strings.HasSuffix(f, ".pb.go") {
			t.Errorf("generated file should be excluded: %s", f)
		}
		if strings.Contains(f, "vendor/") {
			t.Errorf("vendor file should be excluded: %s", f)
		}
		if strings.Contains(f, "testdata/") {
			t.Errorf("testdata file should be excluded: %s", f)
		}
	}
}

// TestSymbolMap_RankFiles verifies files are ranked by depth (shallow first),
// then alphabetically as tie-break.
func TestSymbolMap_RankFiles(t *testing.T) {
	files := []string{
		"internal/deep/nested/very/file.go",
		"main.go",
		"internal/agent/app.go",
		"cmd/wakil/main.go",
	}
	rankFiles(files)

	if files[0] != "main.go" {
		t.Errorf("expected main.go first (0 separators), got %s", files[0])
	}
	if files[1] != "cmd/wakil/main.go" {
		t.Errorf("expected cmd/wakil/main.go second (1 separator), got %s", files[1])
	}
	if files[2] != "internal/agent/app.go" {
		t.Errorf("expected internal/agent/app.go third (2 separators), got %s", files[2])
	}
}

// TestSymbolMap_SymbolKindName verifies kind names are human-readable.
func TestSymbolMap_SymbolKindName(t *testing.T) {
	tests := []struct {
		kind uint32
		want string
	}{
		{12, "func"},
		{6, "method"},
		{23, "struct"},
		{11, "interface"},
		{8, "field"},
		{13, "var"},
		{14, "const"},
		{999, "kind999"},
	}
	for _, tt := range tests {
		got := symbolKindName(lsp.SymbolKind(tt.kind))
		if got != tt.want {
			t.Errorf("symbolKindName(%d) = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

// TestSymbolMap_NilLSP verifies graceful failure when LSP is not enabled.
func TestSymbolMap_NilLSP(t *testing.T) {
	fe := newFakeExecutor()
	fe.files["main.go"] = "package main"
	_, err := BuildSymbolMap(context.Background(), fe, nil, "go")
	if err == nil {
		t.Fatal("expected error when LSP manager is nil")
	}
	if !strings.Contains(err.Error(), "not available") {
		t.Errorf("expected 'not available' in error, got %v", err)
	}
}

// TestSymbolMap_RenderFileSection verifies rendering of hierarchical symbols.
func TestSymbolMap_RenderFileSection(t *testing.T) {
	syms := []lsp.DocumentSymbol{
		{Name: "main", Kind: lsp.SymFunction, Range: lspRange(12, 0, 15, 0)},
		{Name: "User", Kind: lsp.SymStruct, Range: lspRange(4, 0, 8, 0)},
	}
	out := renderFileSection("main.go", syms, nil)
	if !strings.Contains(out, "main.go:") {
		t.Error("expected file path header")
	}
	if !strings.Contains(out, "func main") {
		t.Error("expected function symbol")
	}
	if !strings.Contains(out, "struct User") {
		t.Error("expected struct symbol")
	}
	if !strings.Contains(out, "line 13") {
		t.Error("expected line number (0-based 12 → 1-based 13)")
	}
}

// TestSymbolMap_IsServerLevelError verifies error classification.
func TestSymbolMap_IsServerLevelError(t *testing.T) {
	tests := []struct {
		err  string
		want bool
	}{
		{"server \"go\" does not support documentSymbol", true},
		{"spawn failed: gopls not found", true},
		{"initialize failed: timeout", true},
		{"documentSymbol call for main.go: context deadline exceeded", false},
		{"decode documentSymbol: unexpected EOF", false},
	}
	for _, tt := range tests {
		got := isServerLevelError(strErr(tt.err))
		if got != tt.want {
			t.Errorf("isServerLevelError(%q) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

// strErr wraps a string as an error for testing.
type strErr string

func (s strErr) Error() string { return string(s) }

// lspRange creates an LSP Range for testing.
func lspRange(startLine, startChar, endLine, endChar uint32) lsp.Range {
	return lsp.Range{
		Start: lsp.Position{Line: startLine, Character: startChar},
		End:   lsp.Position{Line: endLine, Character: endChar},
	}
}
