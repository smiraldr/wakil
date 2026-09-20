package agent

import (
	"fmt"
	"strings"
)

// smart_read.go — File-type heuristic for read_file.
//
// The heuristic warns (never refuses) when a file looks likely noisy —
// minified, generated, or a VCS/dependency artifact — and returns a
// preview (head + tail + stats) instead of flooding context. The model
// can always override with an explicit limit.
//
// This does NOT apply to:
//   - read_file_full (its contract promises complete contents)
//   - toolcache/spill paths (already bypassed before this runs)
//   - reads with an explicit limit (the caller already bounded the read)

const (
	// previewLines is the number of head and tail lines shown in a preview.
	previewLines = 50
	// previewLineCap is the maximum number of bytes shown per line in a
	// preview. Lines exceeding this are truncated with a marker so a
	// single 900KB minified line cannot flood context through the preview.
	previewLineCap = 300
	// avgLineLenThreshold flags files where the average line is very long
	// (minified JS, source maps, compiled output, base64 dumps).
	avgLineLenThreshold = 500
	// maxLineLenThreshold flags files with even a single enormous line
	// (single-line JSON dumps, concatenated bundles).
	maxLineLenThreshold = 10_000
)

// likelyNoisyPath reports whether canonical contains a directory component
// that is a known VCS or dependency artifact directory. Matching is on path
// components (split on "/"), not substrings — "foo.git/bar" does not match,
// ".git/objects" does.
func likelyNoisyPath(canonical string) bool {
	for _, part := range strings.Split(canonical, "/") {
		if part == ".git" || part == "node_modules" || part == "vendor" {
			return true
		}
	}
	return false
}

// lineStats splits content into lines (dropping a trailing empty line from
// a final newline, matching formatFileView) and computes the average and
// maximum line byte-lengths. Shared by isLikelyNoisy and filePreview to
// avoid duplicate work.
func lineStats(content string) (lines []string, avgLen, maxLen int) {
	lines = strings.Split(content, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	if len(lines) == 0 {
		return lines, 0, 0
	}
	var totalLen int
	for _, l := range lines {
		nl := len(l)
		totalLen += nl
		if nl > maxLen {
			maxLen = nl
		}
	}
	avgLen = totalLen / len(lines)
	return lines, avgLen, maxLen
}

// truncatePreviewLine clips a line to previewLineCap bytes, appending a
// truncation marker if it was longer. UTF-8 boundaries are preserved by
// searching for the last valid rune boundary at or before the cap.
func truncatePreviewLine(line string) string {
	if len(line) <= previewLineCap {
		return line
	}
	// Find the last rune boundary at or before previewLineCap.
	end := previewLineCap
	for end > 0 && (line[end]&0xC0) == 0x80 {
		end--
	}
	return line[:end] + fmt.Sprintf("…[%d more bytes]", len(line)-end)
}

// filePreview returns a warn-prefixed preview of content: the first and last
// previewLines lines (each truncated to previewLineCap bytes), plus a stats
// line (total lines, avg/max line length in bytes). This is used when
// isLikelyNoisy returns true and the caller did not explicitly bound the
// read with limit.
func filePreview(content string) string {
	lines, avgLen, maxLen := lineStats(content)
	total := len(lines)
	if total == 0 {
		return "(empty file)"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "⚠ Preview — file looks generated or noisy (avg line %d bytes, max %d bytes, %d lines).\n", avgLen, maxLen, total)
	b.WriteString("Use read_file with offset/limit to read specific ranges, or search_files to find patterns within it.\n\n")

	if total <= previewLines*2 {
		// File is short enough to show entirely — but still warn.
		for i, l := range lines {
			fmt.Fprintf(&b, "%6d\t%s\n", i+1, truncatePreviewLine(l))
		}
		return strings.TrimRight(b.String(), "\n")
	}

	// Head.
	for i := 0; i < previewLines; i++ {
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, truncatePreviewLine(lines[i]))
	}
	fmt.Fprintf(&b, "\n… … [%d lines omitted] …\n\n", total-previewLines*2)
	// Tail.
	start := total - previewLines
	for i := start; i < total; i++ {
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, truncatePreviewLine(lines[i]))
	}
	return strings.TrimRight(b.String(), "\n")
}

// isLikelyNoisy decides whether content read from canonicalPath should be
// previewed instead of returned in full. Returns true when:
//   - The path has a known VCS/dependency directory component, OR
//   - The average line length exceeds avgLineLenThreshold, OR
//   - The maximum line length exceeds maxLineLenThreshold.
func isLikelyNoisy(canonicalPath, content string) bool {
	if likelyNoisyPath(canonicalPath) {
		return true
	}
	_, avgLen, maxLen := lineStats(content)
	if len(strings.Split(content, "\n")) == 0 {
		return false
	}
	return avgLen > avgLineLenThreshold || maxLen > maxLineLenThreshold
}
