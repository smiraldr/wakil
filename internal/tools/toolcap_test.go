package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestToolCacheRootUsesXDGDataHome verifies ToolCacheRoot resolves under
// XDG_DATA_HOME/wakil/toolcache, matching toolCacheDir's own precedence so
// the two never point at different directories.
func TestToolCacheRootUsesXDGDataHome(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("WAKIL_SESSIONS_DIR", "")

	root := ToolCacheRoot()
	want := filepath.Join(tmpDir, "wakil", "toolcache")
	if root != want {
		t.Errorf("ToolCacheRoot() = %q, want %q", root, want)
	}

	// toolCacheDir for a given chatID must be a subdirectory of the root.
	dir := toolCacheDir("chat1")
	if filepath.Dir(dir) != root {
		t.Errorf("toolCacheDir(%q) = %q, parent is not ToolCacheRoot() %q", "chat1", dir, root)
	}
}

// TestIsToolCacheHostPathExactPrefix verifies the exact-prefix design: a path
// equal to the root, a path properly nested under it, and paths that merely
// share a string prefix (without the path separator) are all classified
// correctly — no naive strings.HasPrefix(p, root) substring bug.
func TestIsToolCacheHostPathExactPrefix(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("WAKIL_SESSIONS_DIR", "")

	root := ToolCacheRoot()

	cases := []struct {
		name string
		path string
		want bool
	}{
		{"exact root", root, true},
		{"nested file", filepath.Join(root, "chat1", "read_file_full-123.txt"), true},
		{"sibling with shared string prefix but no separator", root + "-decoy", false},
		{"unrelated absolute path", "/etc/passwd", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		got := IsToolCacheHostPath(c.path)
		if got != c.want {
			t.Errorf("%s: IsToolCacheHostPath(%q) = %v, want %v", c.name, c.path, got, c.want)
		}
	}
}

// TestReadHostCacheFileRoundTrip verifies ReadHostCacheFile reads back exactly
// what SpillToCache wrote, and StatHostCacheFile reports the same size.
func TestReadHostCacheFileRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("WAKIL_SESSIONS_DIR", "")

	content := "hello from the host cache\nsecond line\n"
	path := SpillToCache("chat42", "read_file_full", content)
	if path == "" {
		t.Fatal("SpillToCache returned empty path")
	}

	got, err := ReadHostCacheFile(path)
	if err != nil {
		t.Fatalf("ReadHostCacheFile error: %v", err)
	}
	if got != content {
		t.Errorf("ReadHostCacheFile = %q, want %q", got, content)
	}

	size, err := StatHostCacheFile(path)
	if err != nil {
		t.Fatalf("StatHostCacheFile error: %v", err)
	}
	if size != int64(len(content)) {
		t.Errorf("StatHostCacheFile = %d, want %d", size, len(content))
	}
}

// TestCapToolResultTruncationMarker verifies the truncation-feedback marker:
// a result over the cap ends with an explicit TRUNCATED marker, the total
// output never exceeds the cap, and read_file's marker carries the
// offset/limit hint (silent truncation is the re-read-loop failure this fixes).
func TestCapToolResultTruncationMarker(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("WAKIL_SESSIONS_DIR", "")

	const cap = 500
	big := ""
	for i := 0; len(big) < 3*cap; i++ {
		big += "line of tool output content\n"
	}
	total := len(big)

	out := CapToolResult(big, "read_file", "chat-cap-marker", cap)

	if len(out) > cap {
		t.Errorf("capped output is %d chars, exceeds cap %d — marker must fit within the cap", len(out), cap)
	}
	if !strings.HasSuffix(strings.TrimRight(out, " \t\r\n"), "]") {
		t.Errorf("output does not end with the bracketed marker: %q", out[max(0, len(out)-120):])
	}
	if !strings.Contains(out, "TRUNCATED") {
		t.Errorf("marker missing TRUNCATED signal: %q", out[max(0, len(out)-200):])
	}
	if !strings.Contains(out, "offset/limit") {
		t.Errorf("read_file marker missing offset/limit hint: %q", out[max(0, len(out)-200):])
	}
	wantTotal := fmt.Sprintf("of %d chars", total)
	if !strings.Contains(out, wantTotal) {
		t.Errorf("marker missing actual total size %q: %q", wantTotal, out[max(0, len(out)-200):])
	}
	// The spill path must still be recoverable from the marker.
	if p := ExtractSpillPath(out); p == "" {
		t.Errorf("ExtractSpillPath failed on the new marker format: %q", out[max(0, len(out)-200):])
	}
}

// TestCapToolResultMarkerNonRangedTool verifies that tools without offset/limit
// access get "result truncated" instead of the misleading offset/limit hint.
func TestCapToolResultMarkerNonRangedTool(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("WAKIL_SESSIONS_DIR", "")

	big := strings.Repeat("x", 2000)
	out := CapToolResult(big, "run_shell", "chat-cap-shell", 500)

	if strings.Contains(out, "offset/limit") {
		t.Errorf("non-ranged tool marker must not suggest offset/limit: %q", out[max(0, len(out)-200):])
	}
	if !strings.Contains(out, "result truncated") {
		t.Errorf("non-ranged tool marker missing 'result truncated': %q", out[max(0, len(out)-200):])
	}
	if len(out) > 500 {
		t.Errorf("capped output is %d chars, exceeds cap 500", len(out))
	}
}

// TestCapToolResultNoMarkerUnderCap verifies results within the cap pass
// through unchanged — no marker, no spill.
func TestCapToolResultNoMarkerUnderCap(t *testing.T) {
	small := "short result"
	out := CapToolResult(small, "read_file", "chat-under", 500)
	if out != small {
		t.Errorf("under-cap result was modified: %q", out)
	}
	if strings.Contains(out, "TRUNCATED") {
		t.Error("under-cap result must not carry a truncation marker")
	}
}

// TestReadHostCacheFileMissing verifies a nonexistent path under the toolcache
// root returns a real error rather than panicking or silently succeeding.
func TestReadHostCacheFileMissing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("WAKIL_SESSIONS_DIR", "")

	missing := filepath.Join(ToolCacheRoot(), "nonexistent-chat", "nope.txt")
	if _, err := ReadHostCacheFile(missing); err == nil {
		t.Error("expected an error reading a nonexistent toolcache file")
	}
	if _, err := StatHostCacheFile(missing); err == nil {
		t.Error("expected an error statting a nonexistent toolcache file")
	}
}

// TestReadHostCacheFileConfinement — H2 hardening pins. ReadHostCacheFile and
// StatHostCacheFile must confine to the canonical (symlink-resolved) toolcache
// root: traversal and symlink escapes rejected with ErrHostCacheEscape,
// non-regular files with ErrHostCacheNotRegular, ordinary spill reads and
// root-behind-a-symlinked-parent still work. os.Symlink may be unprivileged
// on some platforms (Windows); skip symlink cases there.
func TestReadHostCacheFileConfinement(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("WAKIL_SESSIONS_DIR", "")

	root := ToolCacheRoot()
	chatDir := filepath.Join(root, "chat-h2")
	if err := os.MkdirAll(chatDir, 0o755); err != nil {
		t.Fatal(err)
	}
	spill := filepath.Join(chatDir, "spill-abc123.txt")
	if err := os.WriteFile(spill, []byte("spill content"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Sibling of the root (shares a string prefix with it).
	outside := filepath.Join(tmpDir, "wakil-secret.txt")
	if err := os.WriteFile(outside, []byte("OUTSIDE"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A sibling directory of tmpDir's parent for cross-tree escapes.
	otherTree := t.TempDir()
	otherFile := filepath.Join(otherTree, "victim.txt")
	if err := os.WriteFile(otherFile, []byte("OTHER"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Ordinary spill read still works.
	got, err := ReadHostCacheFile(spill)
	if err != nil || got != "spill content" {
		t.Fatalf("ordinary spill read: got %q, err %v", got, err)
	}
	if size, err := StatHostCacheFile(spill); err != nil || size != int64(len("spill content")) {
		t.Fatalf("ordinary spill stat: size=%d err=%v", size, err)
	}

	// Root itself and directories are non-regular.
	if _, err := ReadHostCacheFile(root); !errors.Is(err, ErrHostCacheNotRegular) {
		t.Errorf("read(root) err = %v, want ErrHostCacheNotRegular", err)
	}
	if _, err := ReadHostCacheFile(chatDir); !errors.Is(err, ErrHostCacheNotRegular) {
		t.Errorf("read(dir) err = %v, want ErrHostCacheNotRegular", err)
	}

	// Lexical traversal escapes (constructed by hand — filepath.Join would
	// Clean the .. away before the classifier could reject it).
	traversal := root + "/../wakil-secret.txt"
	if _, err := ReadHostCacheFile(traversal); !errors.Is(err, ErrHostCacheEscape) {
		t.Errorf("read(traversal) err = %v, want ErrHostCacheEscape", err)
	}
	if _, err := StatHostCacheFile(traversal); !errors.Is(err, ErrHostCacheEscape) {
		t.Errorf("stat(traversal) err = %v, want ErrHostCacheEscape", err)
	}
	// An outside path that never classified inside at all.
	if _, err := ReadHostCacheFile(otherFile); !errors.Is(err, ErrHostCacheEscape) {
		t.Errorf("read(outside) err = %v, want ErrHostCacheEscape", err)
	}

	if runtime.GOOS == "windows" {
		t.Log("skipping symlink cases: os.Symlink may require privilege on windows")
		return
	}

	// Symlink INSIDE the root pointing OUT: leaf escape.
	leafEscape := filepath.Join(chatDir, "leaf-escape")
	if err := os.Symlink(outside, leafEscape); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadHostCacheFile(leafEscape); !errors.Is(err, ErrHostCacheEscape) {
		t.Errorf("read(leaf symlink escape) err = %v, want ErrHostCacheEscape", err)
	}
	if _, err := StatHostCacheFile(leafEscape); !errors.Is(err, ErrHostCacheEscape) {
		t.Errorf("stat(leaf symlink escape) err = %v, want ErrHostCacheEscape", err)
	}

	// Intermediate-directory symlink escape: chatDir-style entry that is a
	// symlink to a directory outside the root.
	interEscape := filepath.Join(root, "chat-escape")
	if err := os.Symlink(otherTree, interEscape); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadHostCacheFile(filepath.Join(interEscape, "victim.txt")); !errors.Is(err, ErrHostCacheEscape) {
		t.Errorf("read(intermediate symlink escape) err = %v, want ErrHostCacheEscape", err)
	}

	// Dangling symlink: confinement verdict first, not a bare ENOENT.
	dangling := filepath.Join(chatDir, "dangling")
	if err := os.Symlink(filepath.Join(otherTree, "nope"), dangling); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadHostCacheFile(dangling); err == nil {
		t.Error("read(dangling) should error")
	}

	// Root reached THROUGH a symlinked parent still reads (canonicalized
	// root; common on macOS where TempDir is /var/folders -> /private/var).
	linkParent := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(tmpDir, linkParent); err != nil {
		t.Fatal(err)
	}
	viaLink := filepath.Join(linkParent, "wakil", "toolcache", "chat-h2", "spill-abc123.txt")
	if got, err := ReadHostCacheFile(viaLink); err != nil || got != "spill content" {
		t.Fatalf("read via symlinked parent: got %q err %v", got, err)
	}

	// Symlink loop: must return an error, not hang.
	loopA := filepath.Join(chatDir, "loop-a")
	loopB := filepath.Join(chatDir, "loop-b")
	if err := os.Symlink(loopB, loopA); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(loopA, loopB); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadHostCacheFile(loopA); err == nil {
		t.Error("read(symlink loop) should error")
	}

	// Valid names that merely LOOK like traversal must still read.
	dotdot := filepath.Join(chatDir, "..spill.txt")
	if err := os.WriteFile(dotdot, []byte("dotdot"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadHostCacheFile(dotdot); err != nil || got != "dotdot" {
		t.Errorf("read('..spill.txt'): got %q err %v — names starting with .. are legal", got, err)
	}

	// A missing file whose NAME contains "escapes" is not-found, not an
	// escape refusal (guards the sentinel mapping against name collisions).
	if _, err := ReadHostCacheFile(filepath.Join(chatDir, "escapes-abc.txt")); errors.Is(err, ErrHostCacheEscape) {
		t.Error("missing file named *escapes* must not classify as escape")
	}

	// Canonical-form path when the ROOT is behind a symlinked ancestor:
	// re-point XDG at a symlinked parent so ToolCacheRoot() is non-canonical.
	if runtime.GOOS != "windows" {
		can, cerr := filepath.EvalSymlinks(tmpDir)
		if cerr != nil {
			t.Fatal(cerr)
		}
		if can != tmpDir {
			// tmpDir itself is a symlink (e.g. macOS /var -> /private/var);
			// canonical paths already work via the alias branch.
			t.Logf("tmpDir is a symlink: canonical-root case covered by alias branch (%s -> %s)", tmpDir, can)
		}
		rootParent := t.TempDir()
		root2 := filepath.Join(rootParent, "alias")
		real2 := filepath.Join(rootParent, "real")
		if err := os.Mkdir(real2, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real2, root2); err != nil {
			t.Fatal(err)
		}
		t.Setenv("XDG_DATA_HOME", root2)
		t.Setenv("WAKIL_SESSIONS_DIR", "")
		spill2 := filepath.Join(ToolCacheRoot(), "c2", "s.txt")
		if err := os.MkdirAll(filepath.Dir(spill2), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(spill2, []byte("canonical-root"), 0o644); err != nil {
			t.Fatal(err)
		}
		// Lexical form via the alias works...
		if got, err := ReadHostCacheFile(spill2); err != nil || got != "canonical-root" {
			t.Fatalf("read via symlinked XDG root: got %q err %v", got, err)
		}
		// ...and the canonical form (EvalSymlinks of the same path) reads too.
		canSpill, cerr := filepath.EvalSymlinks(spill2)
		if cerr != nil {
			t.Fatal(cerr)
		}
		if got, err := ReadHostCacheFile(canSpill); err != nil || got != "canonical-root" {
			t.Fatalf("read canonical-form path under symlinked root: got %q err %v", got, err)
		}
		// Traversal out of the symlinked root still refused.
		out2 := filepath.Join(root2, "toolcache", "..", "escape.txt")
		if _, err := ReadHostCacheFile(out2); !errors.Is(err, ErrHostCacheEscape) &&
			!errors.Is(err, os.ErrNotExist) {
			t.Errorf("traversal via symlinked root: err %v, want escape/not-exist", err)
		}
	}
}
