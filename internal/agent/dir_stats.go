package agent

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// dir_stats.go — Directory stats extension for list_dir.
//
// When include_stats is true, list_dir returns file sizes per entry and
// aggregate file count + total size for subdirectories. Uses shell commands
// (stat, find) via the executor — the same pattern as search_files.
//
// Bounds:
//   - Entry cap: 200 entries from ls -Ap
//   - Find traversal: -maxdepth 100 + output capped at 1MB
//   - .git/ and node_modules/ pruned (not just filtered) from traversal
//   - Up to 200 sequential find calls; each bounded by ctx

const (
	// dirStatEntryCap limits the number of entries stat'd to prevent
	// unbounded output on huge directories.
	dirStatEntryCap = 200
	// findMaxDepth bounds recursive traversal depth.
	findMaxDepth = 100
	// findOutputCap limits aggregate find output to 1MB to bound parsing.
	findOutputCap = 1 << 20
)

// formatBytes formats a byte count as a human-readable string (e.g. "1.2K", "3.4M").
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for nn := n / unit; nn >= unit; nn /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%c", float64(n)/float64(div), "KMGTPE"[exp])
}

// dirStatsResult holds the stats for one directory entry.
type dirStatsResult struct {
	name     string // as returned by ls -Ap (with trailing / for dirs)
	isDir    bool
	fileSize int64 // for files: byte size; for dirs: 0
	// For directories only:
	dirFileCount  int   // number of regular files recursively (pruned)
	dirTotalSize  int64 // total size of all regular files recursively
	dirUnreadable bool  // true if find/stat failed for this directory
}

// computeDirStats runs shell commands via the executor to gather file sizes
// and directory aggregate stats. Returns one entry per line from ls -Ap,
// bounded by dirStatEntryCap, plus a truncated flag.
func (a *App) computeDirStats(ctx context.Context, canonical string) ([]dirStatsResult, bool, error) {
	// Step 1: list entries (reuse the existing ls -Ap approach).
	raw, err := a.Exec.ListDir(ctx, canonical)
	if err != nil {
		return nil, false, err
	}
	raw = strings.TrimRight(raw, "\n")
	if raw == "" {
		return nil, false, nil
	}
	allEntries := strings.Split(raw, "\n")
	truncated := len(allEntries) > dirStatEntryCap
	entries := allEntries
	if truncated {
		entries = entries[:dirStatEntryCap]
	}

	// Step 2: stat all entries in one shell call for file sizes.
	// Use stat -c '%s\t%n' (size first, then name) so SplitN on tab
	// gives size as first field and the full name (which may contain
	// tabs) as the second. Paths are canonical-absolute so no leading
	// dash risk.
	var statCmd strings.Builder
	statCmd.WriteString("stat -c '%s\t%n' -- ")
	for i, e := range entries {
		name := strings.TrimSuffix(e, "/")
		full := canonical + "/" + name
		if i > 0 {
			statCmd.WriteString(" ")
		}
		statCmd.WriteString(shellQuote(full))
	}

	statOut, statErr := a.Exec.RunShell(ctx, statCmd.String())
	// stat may partially succeed (some entries deleted between ls and stat).
	// Use whatever output we got; statErr is noted but not fatal.
	_ = statErr

	// Parse stat output into a map: entry name → size.
	// Only trim \r\n (not spaces — filenames can have leading/trailing spaces).
	sizeMap := make(map[string]int64, len(entries))
	if statOut != "" {
		for _, line := range strings.Split(statOut, "\n") {
			line = strings.TrimRight(line, "\r")
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "\t", 2)
			if len(parts) != 2 {
				continue
			}
			size, err := strconv.ParseInt(parts[0], 10, 64)
			if err != nil {
				continue
			}
			path := parts[1]
			// Extract the entry name from the full path.
			name := path
			if idx := strings.LastIndex(path, "/"); idx >= 0 {
				name = path[idx+1:]
			}
			sizeMap[name] = size
		}
	}

	// Step 3: for directories, compute file count and total size
	// using find with -prune to skip .git/ and node_modules/ subtrees.
	results := make([]dirStatsResult, 0, len(entries))
	for _, e := range entries {
		isDir := strings.HasSuffix(e, "/")
		name := strings.TrimSuffix(e, "/")
		ds := dirStatsResult{name: e, isDir: isDir}
		if !isDir {
			ds.fileSize = sizeMap[name]
		}
		if isDir && name != ".git" && name != "node_modules" {
			full := canonical + "/" + name
			ds.dirFileCount, ds.dirTotalSize, ds.dirUnreadable = a.aggregateDir(ctx, full)
		}
		results = append(results, ds)
	}

	return results, truncated, nil
}

// aggregateDir counts files and sums their sizes in a directory tree,
// pruning .git/ and node_modules/ subtrees from traversal (not just
// filtering). Returns (fileCount, totalBytes, unreadable).
func (a *App) aggregateDir(ctx context.Context, dirPath string) (int, int64, bool) {
	// Use -prune to avoid descending into excluded subtrees:
	// \( -name .git -o -name node_modules \) -prune -o -type f -printf '%s\n'
	cmd := fmt.Sprintf(
		"find %s -maxdepth %d \\( -name .git -o -name node_modules \\) -prune -o -type f -printf '%%s\\n' 2>/dev/null | head -c %d",
		shellQuote(dirPath), findMaxDepth, findOutputCap,
	)
	out, err := a.Exec.RunShell(ctx, cmd)
	if err != nil && strings.TrimSpace(out) == "" {
		return 0, 0, true // unreadable
	}
	out = strings.TrimRight(out, "\n")
	if out == "" {
		if err != nil {
			return 0, 0, true
		}
		return 0, 0, false // genuinely empty (no regular files)
	}
	lines := strings.Split(out, "\n")
	var totalSize int64
	validCount := 0
	for _, line := range lines {
		n, err := strconv.ParseInt(strings.TrimSpace(line), 10, 64)
		if err == nil {
			totalSize += n
			validCount++
		}
	}
	// Check if output was truncated (hit the byte cap).
	if len(out) >= findOutputCap-1 {
		// Last line may be partial — don't count it if it fails to parse.
		// Return with a note that results are partial.
		return validCount, totalSize, true // partial = unreadable flag used as "partial"
	}
	return validCount, totalSize, false
}

// formatDirStats renders the stats-augmented directory listing.
func formatDirStats(results []dirStatsResult, truncated bool) string {
	if len(results) == 0 {
		return "(empty directory)"
	}

	// Sort: directories first (alphabetical), then files (alphabetical).
	sort.SliceStable(results, func(i, j int) bool {
		ri, rj := results[i], results[j]
		if ri.isDir != rj.isDir {
			return ri.isDir // dirs first
		}
		return ri.name < rj.name
	})

	var b strings.Builder
	// Compute max name width for alignment (byte length — acceptable for
	// alignment; exact display width would need wcwidth which we don't have).
	maxName := 0
	for _, r := range results {
		if l := len(r.name); l > maxName {
			maxName = l
		}
	}
	if maxName < 20 {
		maxName = 20
	}

	for _, r := range results {
		if r.isDir {
			if r.name == ".git/" || r.name == "node_modules/" {
				fmt.Fprintf(&b, "%-*s  (subtree skipped)\n", maxName, r.name)
			} else if r.dirUnreadable {
				if r.dirFileCount > 0 {
					fmt.Fprintf(&b, "%-*s  ≥%d files, ≥%s (partial)\n", maxName, r.name, r.dirFileCount, formatBytes(r.dirTotalSize))
				} else {
					fmt.Fprintf(&b, "%-*s  (unreadable)\n", maxName, r.name)
				}
			} else if r.dirFileCount > 0 {
				fmt.Fprintf(&b, "%-*s  %d files, %s\n", maxName, r.name, r.dirFileCount, formatBytes(r.dirTotalSize))
			} else {
				fmt.Fprintf(&b, "%-*s  (no regular files)\n", maxName, r.name)
			}
		} else {
			fmt.Fprintf(&b, "%-*s  %s\n", maxName, r.name, formatBytes(r.fileSize))
		}
	}
	if truncated {
		fmt.Fprintf(&b, "\n… [capped at %d entries — use list_dir on a subdirectory for more]\n", dirStatEntryCap)
	}
	return strings.TrimRight(b.String(), "\n")
}

// handleListDirWithStats is the include_stats=true path for list_dir.
func (a *App) handleListDirWithStats(ctx context.Context, canonical string) string {
	results, truncated, err := a.computeDirStats(ctx, canonical)
	if err != nil {
		return formatResult("", err)
	}
	if len(results) == 0 {
		return "(empty directory)"
	}
	return formatDirStats(results, truncated)
}
