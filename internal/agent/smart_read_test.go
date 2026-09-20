package agent

import (
	"strings"
	"testing"
)

func TestIsLikelyNoisy_MinifiedJS(t *testing.T) {
	// Simulated minified JS: few lines, very long.
	content := strings.Repeat("var x=function(a,b){return a+b};", 200)
	if !isLikelyNoisy("/workspace/app.js", content) {
		t.Error("minified JS should be flagged as noisy (avg line length > 500)")
	}
}

func TestIsLikelyNoisy_GiantSingleLine(t *testing.T) {
	content := strings.Repeat("a", 20_000)
	if !isLikelyNoisy("/workspace/dump.txt", content) {
		t.Error("single 20KB line should be flagged as noisy (max line length > 10K)")
	}
}

func TestIsLikelyNoisy_MaxLineDilutedByShortLines(t *testing.T) {
	// One >10K line diluted by enough short lines to keep average ≤500.
	// This isolates the max-line branch from the avg-line branch.
	lines := []string{strings.Repeat("x", 11_000)}
	for i := 0; i < 100; i++ {
		lines = append(lines, "short")
	}
	content := strings.Join(lines, "\n")
	if !isLikelyNoisy("/workspace/data.txt", content) {
		t.Error("file with one 11K line (avg diluted to ~109) should be flagged via max-line threshold")
	}
}

func TestIsLikelyNoisy_GitPath(t *testing.T) {
	content := "ref: refs/heads/main\n"
	if !isLikelyNoisy("/workspace/.git/HEAD", content) {
		t.Error(".git/ path should be flagged as noisy regardless of content")
	}
}

func TestIsLikelyNoisy_NodeModulesPath(t *testing.T) {
	content := "module.exports = function() {};\n"
	if !isLikelyNoisy("/workspace/node_modules/react/index.js", content) {
		t.Error("node_modules/ path should be flagged as noisy regardless of content")
	}
}

func TestIsLikelyNoisy_VendorPath(t *testing.T) {
	content := "package main\n"
	if !isLikelyNoisy("/workspace/vendor/foo/bar.go", content) {
		t.Error("vendor/ path should be flagged as noisy regardless of content")
	}
}

func TestIsLikelyNoisy_NormalSourceFile(t *testing.T) {
	content := "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"
	if isLikelyNoisy("/workspace/main.go", content) {
		t.Error("normal source file should NOT be flagged as noisy")
	}
}

func TestIsLikelyNoisy_EmptyFile(t *testing.T) {
	if isLikelyNoisy("/workspace/empty.txt", "") {
		t.Error("empty file should NOT be flagged as noisy")
	}
}

func TestIsLikelyNoisy_GitPathSubstring(t *testing.T) {
	// "foo.git" as a substring should NOT match — only ".git" as a directory component.
	content := "package main\n"
	if isLikelyNoisy("/workspace/foo.git/bar.txt", content) {
		t.Error("foo.git/ should NOT be flagged as noisy (only .git/ directory component)")
	}
}

func TestIsLikelyNoisy_CRLF(t *testing.T) {
	// CRLF should not cause false positives — \r\n is normal in Windows files.
	content := "line1\r\nline2\r\nline3\r\n"
	if isLikelyNoisy("/workspace/windows.txt", content) {
		t.Error("normal CRLF file should NOT be flagged as noisy")
	}
}

func TestLikelyNoisyPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/workspace/.git/HEAD", true},
		{"/workspace/.git/objects/ab/cdef", true},
		{"/workspace/node_modules/react/index.js", true},
		{"/workspace/vendor/golang.org/x/sys/exec.go", true},
		{"/workspace/src/main.go", false},
		{"/workspace/foo.git/config", false},
		{"/workspace/my_node_modules/file.go", false},
		{"/workspace/.github/workflows/ci.yml", false},
	}
	for _, tc := range tests {
		if got := likelyNoisyPath(tc.path); got != tc.want {
			t.Errorf("likelyNoisyPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestFilePreview_ShortFile(t *testing.T) {
	content := "line1\nline2\nline3\n"
	result := filePreview(content)
	if !strings.Contains(result, "⚠") {
		t.Error("preview should start with warning")
	}
	if !strings.Contains(result, "line1") || !strings.Contains(result, "line3") {
		t.Error("short file preview should contain all lines")
	}
	if !strings.Contains(result, "3 lines") {
		t.Error("preview should mention total line count")
	}
}

func TestFilePreview_LongFile(t *testing.T) {
	// Generate 200 lines.
	var lines []string
	for i := 0; i < 200; i++ {
		lines = append(lines, "line "+string(rune('a'+i%26))+strings.Repeat("x", 600))
	}
	content := strings.Join(lines, "\n")
	result := filePreview(content)
	if !strings.Contains(result, "⚠") {
		t.Error("preview should start with warning")
	}
	if !strings.Contains(result, "lines omitted") {
		t.Error("long file preview should contain [lines omitted] marker")
	}
	if !strings.Contains(result, "200 lines") {
		t.Error("preview should mention total line count")
	}
}

func TestFilePreview_EmptyFile(t *testing.T) {
	result := filePreview("")
	if result != "(empty file)" {
		t.Errorf("empty file should return '(empty file)', got %q", result)
	}
}

func TestFilePreview_GiantSingleLineTruncated(t *testing.T) {
	// A single 900KB line should be truncated, not returned in full.
	content := strings.Repeat("a", 900_000)
	result := filePreview(content)
	if !strings.Contains(result, "⚠") {
		t.Error("preview should start with warning")
	}
	if strings.Contains(result, strings.Repeat("a", 900_000)) {
		t.Error("giant line should be truncated, not returned in full")
	}
	if !strings.Contains(result, "more bytes") {
		t.Error("truncated line should contain 'more bytes' marker")
	}
}

func TestTruncatePreviewLine(t *testing.T) {
	// Short line passes through.
	short := "hello world"
	if got := truncatePreviewLine(short); got != short {
		t.Errorf("short line should pass through, got %q", got)
	}
	// Long line is truncated.
	long := strings.Repeat("x", 500)
	got := truncatePreviewLine(long)
	if len(got) > previewLineCap+50 { // +50 for the marker text
		t.Errorf("truncated line should be roughly previewLineCap bytes, got %d", len(got))
	}
	if !strings.Contains(got, "more bytes") {
		t.Error("truncated line should contain 'more bytes' marker")
	}
}

func TestFilePreview_BytesNotChars(t *testing.T) {
	content := "line1\nline2\n"
	result := filePreview(content)
	if !strings.Contains(result, "bytes") {
		t.Error("preview should say 'bytes' not 'chars'")
	}
}
