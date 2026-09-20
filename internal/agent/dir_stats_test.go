package agent

import (
	"strings"
	"testing"
)

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0B"},
		{512, "512B"},
		{1023, "1023B"},
		{1024, "1.0K"},
		{1536, "1.5K"},
		{1048576, "1.0M"},
		{1572864, "1.5M"},
		{1073741824, "1.0G"},
	}
	for _, tc := range tests {
		if got := formatBytes(tc.n); got != tc.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestFormatDirStats_Empty(t *testing.T) {
	result := formatDirStats(nil, false)
	if result != "(empty directory)" {
		t.Errorf("empty results should return '(empty directory)', got %q", result)
	}
}

func TestFormatDirStats_FilesAndDirs(t *testing.T) {
	results := []dirStatsResult{
		{name: "main.go", isDir: false, fileSize: 1024},
		{name: "utils.go", isDir: false, fileSize: 512},
		{name: "src/", isDir: true, dirFileCount: 10, dirTotalSize: 20480},
		{name: ".git/", isDir: true},
		{name: "node_modules/", isDir: true},
	}
	result := formatDirStats(results, false)

	// Dirs should come first
	if !strings.Contains(result, "src/") {
		t.Error("should contain src/")
	}
	if !strings.Contains(result, "10 files") {
		t.Error("should show file count for src/")
	}
	if !strings.Contains(result, "20.0K") {
		t.Error("should show aggregate size for src/")
	}
	if !strings.Contains(result, "main.go") {
		t.Error("should contain main.go")
	}
	if !strings.Contains(result, "1.0K") {
		t.Error("should show file size for main.go")
	}
	if !strings.Contains(result, "(subtree skipped)") {
		t.Error("should mark .git/ and node_modules/ as subtree skipped")
	}
}

func TestFormatDirStats_Truncated(t *testing.T) {
	results := []dirStatsResult{
		{name: "file.txt", isDir: false, fileSize: 100},
	}
	result := formatDirStats(results, true)
	if !strings.Contains(result, "capped at") {
		t.Error("should contain truncation notice when truncated=true")
	}
}

func TestFormatDirStats_NotTruncated(t *testing.T) {
	results := []dirStatsResult{
		{name: "file.txt", isDir: false, fileSize: 100},
	}
	result := formatDirStats(results, false)
	if strings.Contains(result, "capped at") {
		t.Error("should NOT contain truncation notice when truncated=false")
	}
}

func TestFormatDirStats_DirNoRegularFiles(t *testing.T) {
	results := []dirStatsResult{
		{name: "emptydir/", isDir: true, dirFileCount: 0, dirTotalSize: 0},
	}
	result := formatDirStats(results, false)
	if !strings.Contains(result, "(no regular files)") {
		t.Error("dir with 0 regular files should show (no regular files), not (empty)")
	}
}

func TestFormatDirStats_UnreadableDir(t *testing.T) {
	results := []dirStatsResult{
		{name: "deny/", isDir: true, dirUnreadable: true},
	}
	result := formatDirStats(results, false)
	if !strings.Contains(result, "(unreadable)") {
		t.Error("unreadable dir should show (unreadable)")
	}
}

func TestFormatDirStats_PartialDir(t *testing.T) {
	results := []dirStatsResult{
		{name: "big/", isDir: true, dirFileCount: 500, dirTotalSize: 1000000, dirUnreadable: true},
	}
	result := formatDirStats(results, false)
	if !strings.Contains(result, "(partial)") {
		t.Error("partial dir should show (partial)")
	}
	if !strings.Contains(result, "≥500") {
		t.Error("partial dir should show ≥ prefix on count")
	}
}

func TestFormatDirStats_DirOrdering(t *testing.T) {
	results := []dirStatsResult{
		{name: "z.go", isDir: false, fileSize: 100},
		{name: "a.go", isDir: false, fileSize: 200},
		{name: "zdir/", isDir: true, dirFileCount: 1, dirTotalSize: 100},
		{name: "adir/", isDir: true, dirFileCount: 1, dirTotalSize: 100},
	}
	result := formatDirStats(results, false)
	lines := strings.Split(result, "\n")
	// Dirs should come before files, sorted alphabetically.
	if !strings.HasPrefix(lines[0], "adir/") {
		t.Errorf("first entry should be 'adir/', got %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "zdir/") {
		t.Errorf("second entry should be 'zdir/', got %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "a.go") {
		t.Errorf("third entry should be 'a.go', got %q", lines[2])
	}
	if !strings.HasPrefix(lines[3], "z.go") {
		t.Errorf("fourth entry should be 'z.go', got %q", lines[3])
	}
}
