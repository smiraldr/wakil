package lsp

// spike_document_symbol_test.go — Compatibility spike for #20 semantic code search.
//
// Tests documentSymbol and workspace/symbol against real LSP servers (gopls,
// rust-analyzer, pyright, typescript-language-server) to determine:
//
//  1. Can workspace/symbol enumerate with empty query? (Mashūra says no)
//  2. Does documentSymbol work per-file? (the proposed primitive for symbol map)
//  3. What fields are actually returned? (name, kind, range, children?)
//  4. How long does cold indexing take? (the "too slow" claim is unbenchmarked)
//  5. Are results complete after Ready, or partial?
//
// These tests are skipped when the server binary is not found or when
// WAKIL_LSPI_SPIKE is not set (they need real servers and are slow).
//
// Run: WAKIL_LSPI_SPIKE=1 go test -run TestSpike -v -count=1 -timeout 120s ./internal/lsp/

import (
	"context"
	"encoding/json"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/treeol/wakil/internal/config"
	"github.com/treeol/wakil/internal/exec"
)

// spikeEnv returns true if the spike tests should run.
func spikeEnv() bool {
	return os.Getenv("WAKIL_LSPI_SPIKE") != ""
}

// spikeDir creates a temp directory with a small Go module for testing.
func spikeDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for path, content := range files {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// spikeGoModule creates a small Go project for LSP testing.
func spikeGoModule(t *testing.T) string {
	return spikeDir(t, map[string]string{
		"go.mod": "module spike\n\ngo 1.22\n",
		"main.go": `package main

import "fmt"

type User struct {
	Name string
	Age  int
}

func (u *User) Greet() string {
	return fmt.Sprintf("Hi, I'm %s", u.Name)
}

func main() {
	u := &User{Name: "Test", Age: 30}
	fmt.Println(u.Greet())
}
`,
		"utils/utils.go": `package utils

func Helper(x int) int {
	return x * 2
}

type Config struct {
	Timeout int
}
`,
	})
}

// spikeTSModule creates a small TypeScript project for LSP testing.
func spikeTSModule(t *testing.T) string {
	return spikeDir(t, map[string]string{
		"package.json":  `{"name":"spike","version":"1.0.0"}`,
		"tsconfig.json": `{"compilerOptions":{"target":"es2020","module":"commonjs"}}`,
		"index.ts": `
interface User {
  name: string;
  age: number;
}

function greet(u: User): string {
  return "Hi, I'm " + u.name;
}

class Greeter {
  private user: User;

  constructor(u: User) {
    this.user = u;
  }

  greet(): string {
    return greet(this.user);
  }
}

export { User, greet, Greeter };
`,
		"utils.ts": `
export function helper(x: number): number {
  return x * 2;
}

export interface Config {
  timeout: number;
}
`,
	})
}

// spikePythonModule creates a small Python project for LSP testing.
func spikePythonModule(t *testing.T) string {
	return spikeDir(t, map[string]string{
		"main.py": `
class User:
    def __init__(self, name: str, age: int):
        self.name = name
        self.age = age

    def greet(self) -> str:
        return f"Hi, I'm {self.name}"

def main():
    u = User("Test", 30)
    print(u.greet())
`,
		"utils.py": `
def helper(x: int) -> int:
    return x * 2

class Config:
    timeout: int
`,
	})
}

// spikeManager creates a real LSP manager with a direct executor pointing at dir.
func spikeManager(t *testing.T, dir string) (*Manager, *exec.DirectExecutor) {
	t.Helper()
	de, err := exec.NewDirectExecutor(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.LSPEnabled = true
	cfg.LSPIndexTimeoutSeconds = 60 // generous for cold start
	uri := "file://" + dir
	mgr := NewManager(de, cfg, uri)
	t.Cleanup(func() { mgr.Shutdown() })
	return mgr, de
}

// binaryExists checks if a binary is in PATH.
func binaryExists(name string) bool {
	_, err := osexec.LookPath(name)
	return err == nil
}

// ─── Spike 1: workspace/symbol with empty query ──────────────────────────

// TestSpike_WorkspaceSymbolEmptyQuery tests whether workspace/symbol returns
// anything useful with an empty query. Mashūra claims this is unreliable and
// servers cap results.
func TestSpike_WorkspaceSymbolEmptyQuery(t *testing.T) {
	if !spikeEnv() {
		t.Skip("set WAKIL_LSPI_SPIKE=1 to run LSP spike tests")
	}
	if !binaryExists("gopls") {
		t.Skip("gopls not found")
	}

	dir := spikeGoModule(t)
	mgr, _ := spikeManager(t, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	srv, err := mgr.EnsureServer(ctx, "go")
	if err != nil {
		t.Fatalf("EnsureServer: %v", err)
	}

	// Wait for ready.
	if err := srv.waitForReady(ctx); err != nil {
		t.Fatalf("waitForReady: %v", err)
	}

	// Test 1a: empty query
	raw, err := srv.Call(ctx, "workspace/symbol", WorkspaceSymbolParams{Query: ""})
	if err != nil {
		t.Logf("empty query returned error: %v", err)
	} else {
		var syms []SymbolInformation
		if err := json.Unmarshal(raw, &syms); err != nil {
			t.Logf("empty query: unmarshal error: %v (raw: %s)", err, string(raw)[:min(200, len(raw))])
		} else {
			t.Logf("empty query: returned %d symbols", len(syms))
			for i, s := range syms {
				if i < 10 {
					t.Logf("  %d. %s (kind=%d) at %s:%d", i+1, s.Name, s.Kind,
						s.Location.URI, s.Location.Range.Start.Line)
				}
			}
			if len(syms) > 10 {
				t.Logf("  ... and %d more", len(syms)-10)
			}
		}
	}

	// Test 1b: single-char query "a" (broad fuzzy)
	raw2, err := srv.Call(ctx, "workspace/symbol", WorkspaceSymbolParams{Query: "a"})
	if err != nil {
		t.Logf("query 'a' returned error: %v", err)
	} else {
		var syms2 []SymbolInformation
		if err := json.Unmarshal(raw2, &syms2); err != nil {
			t.Logf("query 'a': unmarshal error: %v", err)
		} else {
			t.Logf("query 'a': returned %d symbols", len(syms2))
		}
	}

	// Test 1c: specific query "User"
	raw3, err := srv.Call(ctx, "workspace/symbol", WorkspaceSymbolParams{Query: "User"})
	if err != nil {
		t.Logf("query 'User' returned error: %v", err)
	} else {
		var syms3 []SymbolInformation
		if err := json.Unmarshal(raw3, &syms3); err != nil {
			t.Logf("query 'User': unmarshal error: %v", err)
		} else {
			t.Logf("query 'User': returned %d symbols", len(syms3))
			for i, s := range syms3 {
				if i < 5 {
					t.Logf("  %d. %s (kind=%d) at %s:%d", i+1, s.Name, s.Kind,
						s.Location.URI, s.Location.Range.Start.Line)
				}
			}
		}
	}
}

// ─── Spike 2: documentSymbol per file ─────────────────────────────────────

// TestSpike_DocumentSymbol tests textDocument/documentSymbol — the proposed
// primitive for building a symbol map. This is the key test: if documentSymbol
// works well, we can build a symbol map from it.
func TestSpike_DocumentSymbol(t *testing.T) {
	if !spikeEnv() {
		t.Skip("set WAKIL_LSPI_SPIKE=1 to run LSP spike tests")
	}
	if !binaryExists("gopls") {
		t.Skip("gopls not found")
	}

	dir := spikeGoModule(t)
	mgr, _ := spikeManager(t, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	srv, err := mgr.EnsureServer(ctx, "go")
	if err != nil {
		t.Fatalf("EnsureServer: %v", err)
	}
	if err := srv.waitForReady(ctx); err != nil {
		t.Fatalf("waitForReady: %v", err)
	}

	// Open the file first (documentSymbol requires didOpen).
	mainGo := filepath.Join(dir, "main.go")
	content, _ := os.ReadFile(mainGo)
	uri := "file://" + mainGo
	if err := srv.DidOpen(ctx, uri, "go", string(content)); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}

	// Request documentSymbol.
	params := DocumentSymbolParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
	}
	raw, err := srv.Call(ctx, "textDocument/documentSymbol", params)
	if err != nil {
		t.Fatalf("documentSymbol error: %v", err)
	}

	// Try hierarchical DocumentSymbol[] first (gopls supports this).
	var docSyms []DocumentSymbol
	if err := json.Unmarshal(raw, &docSyms); err != nil {
		// Try flat SymbolInformation[] (legacy servers).
		var symInfos []SymbolInformation
		if err2 := json.Unmarshal(raw, &symInfos); err2 != nil {
			t.Fatalf("failed to decode as both DocumentSymbol[] and SymbolInformation[]: %v\nraw: %s",
				err, string(raw)[:min(500, len(raw))])
		}
		t.Logf("got SymbolInformation[] (flat): %d symbols", len(symInfos))
		for i, s := range symInfos {
			t.Logf("  %d. %s (kind=%d) at %s:%d", i+1, s.Name, s.Kind,
				s.Location.URI, s.Location.Range.Start.Line)
		}
		return
	}

	t.Logf("got DocumentSymbol[] (hierarchical): %d top-level symbols", len(docSyms))
	printDocSyms(t, docSyms, 0)

	// Also test utils/utils.go.
	utilsGo := filepath.Join(dir, "utils", "utils.go")
	utilsContent, _ := os.ReadFile(utilsGo)
	utilsURI := "file://" + utilsGo
	if err := srv.DidOpen(ctx, utilsURI, "go", string(utilsContent)); err != nil {
		t.Fatalf("DidOpen utils: %v", err)
	}

	params2 := DocumentSymbolParams{
		TextDocument: TextDocumentIdentifier{URI: utilsURI},
	}
	raw2, err := srv.Call(ctx, "textDocument/documentSymbol", params2)
	if err != nil {
		t.Fatalf("documentSymbol utils error: %v", err)
	}

	var docSyms2 []DocumentSymbol
	if err := json.Unmarshal(raw2, &docSyms2); err != nil {
		var symInfos2 []SymbolInformation
		if err2 := json.Unmarshal(raw2, &symInfos2); err2 != nil {
			t.Logf("utils: failed to decode: %v", err)
			return
		}
		t.Logf("utils: got SymbolInformation[] (flat): %d symbols", len(symInfos2))
		return
	}

	t.Logf("utils: got DocumentSymbol[] (hierarchical): %d top-level symbols", len(docSyms2))
	printDocSyms(t, docSyms2, 0)
}

func printDocSyms(t *testing.T, syms []DocumentSymbol, depth int) {
	for _, s := range syms {
		prefix := strings.Repeat("  ", depth)
		t.Logf("%s- %s (kind=%d, line=%d, children=%d)",
			prefix, s.Name, s.Kind, s.Range.Start.Line, len(s.Children))
		if len(s.Children) > 0 && depth < 2 {
			printDocSyms(t, s.Children, depth+1)
		}
	}
}

// ─── Spike 3: Cold indexing time ──────────────────────────────────────────

// TestSpike_ColdIndexTime measures how long gopls takes from spawn to Ready
// on the wakil repo itself (a real ~50k LOC Go project).
func TestSpike_ColdIndexTime(t *testing.T) {
	if !spikeEnv() {
		t.Skip("set WAKIL_LSPI_SPIKE=1 to run LSP spike tests")
	}
	if !binaryExists("gopls") {
		t.Skip("gopls not found")
	}

	// Use the wakil repo itself.
	wakilDir, _ := os.Getwd()
	// Go up from internal/lsp to the repo root.
	wakilDir = filepath.Join(wakilDir, "..", "..")

	de, err := exec.NewDirectExecutor(wakilDir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.LSPEnabled = true
	cfg.LSPIndexTimeoutSeconds = 120
	uri := "file://" + wakilDir
	mgr := NewManager(de, cfg, uri)
	t.Cleanup(func() { mgr.Shutdown() })

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	start := time.Now()
	srv, err := mgr.EnsureServer(ctx, "go")
	if err != nil {
		t.Fatalf("EnsureServer: %v", err)
	}

	if err := srv.waitForReady(ctx); err != nil {
		t.Fatalf("waitForReady: %v (elapsed: %v)", err, time.Since(start))
	}
	elapsed := time.Since(start)
	t.Logf("gopls cold index time on wakil repo: %v", elapsed)

	// Now measure a workspace/symbol query.
	queryStart := time.Now()
	raw, err := srv.Call(ctx, "workspace/symbol", WorkspaceSymbolParams{Query: "App"})
	queryElapsed := time.Since(queryStart)
	if err != nil {
		t.Logf("workspace/symbol 'App' error: %v", err)
	} else {
		var syms []SymbolInformation
		if err := json.Unmarshal(raw, &syms); err == nil {
			t.Logf("workspace/symbol 'App': %d results in %v", len(syms), queryElapsed)
		}
	}

	// Categorize: fast (<5s), acceptable (<15s), slow (<30s), unacceptable (≥30s).
	category := "fast"
	if elapsed >= 5*time.Second {
		category = "acceptable"
	}
	if elapsed >= 15*time.Second {
		category = "slow"
	}
	if elapsed >= 30*time.Second {
		category = "unacceptable"
	}
	t.Logf("index speed category: %s", category)
}

// ─── Spike 4: TypeScript documentSymbol ───────────────────────────────────

func TestSpike_TSDocumentSymbol(t *testing.T) {
	if !spikeEnv() {
		t.Skip("set WAKIL_LSPI_SPIKE=1 to run LSP spike tests")
	}
	if !binaryExists("typescript-language-server") {
		t.Skip("typescript-language-server not found")
	}

	dir := spikeTSModule(t)
	mgr, _ := spikeManager(t, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	start := time.Now()
	srv, err := mgr.EnsureServer(ctx, "typescript")
	if err != nil {
		t.Fatalf("EnsureServer: %v", err)
	}
	if err := srv.waitForReady(ctx); err != nil {
		t.Fatalf("waitForReady: %v (elapsed: %v)", err, time.Since(start))
	}
	t.Logf("typescript-language-server cold index: %v", time.Since(start))

	// Open index.ts.
	indexTS := filepath.Join(dir, "index.ts")
	content, _ := os.ReadFile(indexTS)
	uri := "file://" + indexTS
	if err := srv.DidOpen(ctx, uri, "typescript", string(content)); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}

	params := DocumentSymbolParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
	}
	raw, err := srv.Call(ctx, "textDocument/documentSymbol", params)
	if err != nil {
		t.Fatalf("documentSymbol error: %v", err)
	}

	var docSyms []DocumentSymbol
	if err := json.Unmarshal(raw, &docSyms); err != nil {
		var symInfos []SymbolInformation
		if err2 := json.Unmarshal(raw, &symInfos); err2 != nil {
			t.Fatalf("failed to decode: %v\nraw: %s", err, string(raw)[:min(500, len(raw))])
		}
		t.Logf("got SymbolInformation[] (flat): %d symbols", len(symInfos))
		for i, s := range symInfos {
			t.Logf("  %d. %s (kind=%d) at %s:%d", i+1, s.Name, s.Kind,
				s.Location.URI, s.Location.Range.Start.Line)
		}
		return
	}

	t.Logf("got DocumentSymbol[] (hierarchical): %d top-level symbols", len(docSyms))
	printDocSyms(t, docSyms, 0)
}

// ─── Spike 5: Python documentSymbol ───────────────────────────────────────

func TestSpike_PythonDocumentSymbol(t *testing.T) {
	if !spikeEnv() {
		t.Skip("set WAKIL_LSPI_SPIKE=1 to run LSP spike tests")
	}
	if !binaryExists("pyright-langserver") {
		t.Skip("pyright-langserver not found")
	}

	dir := spikePythonModule(t)
	mgr, _ := spikeManager(t, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	start := time.Now()
	srv, err := mgr.EnsureServer(ctx, "python")
	if err != nil {
		t.Fatalf("EnsureServer: %v", err)
	}
	if err := srv.waitForReady(ctx); err != nil {
		t.Fatalf("waitForReady: %v (elapsed: %v)", err, time.Since(start))
	}
	t.Logf("pyright cold index: %v", time.Since(start))

	// Open main.py.
	mainPy := filepath.Join(dir, "main.py")
	content, _ := os.ReadFile(mainPy)
	uri := "file://" + mainPy
	if err := srv.DidOpen(ctx, uri, "python", string(content)); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}

	params := DocumentSymbolParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
	}
	raw, err := srv.Call(ctx, "textDocument/documentSymbol", params)
	if err != nil {
		t.Fatalf("documentSymbol error: %v", err)
	}

	var docSyms []DocumentSymbol
	if err := json.Unmarshal(raw, &docSyms); err != nil {
		var symInfos []SymbolInformation
		if err2 := json.Unmarshal(raw, &symInfos); err2 != nil {
			t.Fatalf("failed to decode: %v\nraw: %s", err, string(raw)[:min(500, len(raw))])
		}
		t.Logf("got SymbolInformation[] (flat): %d symbols", len(symInfos))
		for i, s := range symInfos {
			t.Logf("  %d. %s (kind=%d) at %s:%d", i+1, s.Name, s.Kind,
				s.Location.URI, s.Location.Range.Start.Line)
		}
		return
	}

	t.Logf("got DocumentSymbol[] (hierarchical): %d top-level symbols", len(docSyms))
	printDocSyms(t, docSyms, 0)
}

// ─── Spike 6: workspace/symbol result cap ─────────────────────────────────

// TestSpike_WorkspaceSymbolCap tests whether gopls caps workspace/symbol
// results and at what threshold.
func TestSpike_WorkspaceSymbolCap(t *testing.T) {
	if !spikeEnv() {
		t.Skip("set WAKIL_LSPI_SPIKE=1 to run LSP spike tests")
	}
	if !binaryExists("gopls") {
		t.Skip("gopls not found")
	}

	// Use the wakil repo (large enough to produce many symbols).
	wakilDir, _ := os.Getwd()
	wakilDir = filepath.Join(wakilDir, "..", "..")

	de, err := exec.NewDirectExecutor(wakilDir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.LSPEnabled = true
	cfg.LSPIndexTimeoutSeconds = 120
	uri := "file://" + wakilDir
	mgr := NewManager(de, cfg, uri)
	t.Cleanup(func() { mgr.Shutdown() })

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	srv, err := mgr.EnsureServer(ctx, "go")
	if err != nil {
		t.Fatalf("EnsureServer: %v", err)
	}
	if err := srv.waitForReady(ctx); err != nil {
		t.Fatalf("waitForReady: %v", err)
	}

	// Query with a very broad pattern to try to hit the cap.
	for _, q := range []string{"a", "e", "i", "Test", "Get", "Handle"} {
		raw, err := srv.Call(ctx, "workspace/symbol", WorkspaceSymbolParams{Query: q})
		if err != nil {
			t.Logf("query %q: error: %v", q, err)
			continue
		}
		var syms []SymbolInformation
		if err := json.Unmarshal(raw, &syms); err != nil {
			t.Logf("query %q: unmarshal error: %v", q, err)
			continue
		}
		t.Logf("query %q: %d symbols", q, len(syms))
	}

	// Also test: does gopls support workspace/symbol with empty string?
	raw, err := srv.Call(ctx, "workspace/symbol", WorkspaceSymbolParams{Query: ""})
	if err != nil {
		t.Logf("empty query on wakil repo: error: %v", err)
	} else {
		var syms []SymbolInformation
		if err := json.Unmarshal(raw, &syms); err != nil {
			t.Logf("empty query: unmarshal error: %v (raw len: %d)", err, len(raw))
		} else {
			t.Logf("empty query on wakil repo: %d symbols", len(syms))
			if len(syms) > 0 {
				t.Logf("  first: %s (kind=%d) at %s", syms[0].Name, syms[0].Kind, syms[0].Location.URI)
			}
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
