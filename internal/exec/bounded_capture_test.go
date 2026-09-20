package exec

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// helper: build a sh -c command rooted in dir.
func shIn(dir, script string) *osexec.Cmd {
	cmd := osexec.Command("sh", "-c", script)
	cmd.Dir = dir
	return cmd
}

// H4 tests: bounded host-memory acquisition.

// TestRunShellCappedBoundedMemory — a command producing far more than the cap
// returns only the retained tail, and the child was fully drained (no
// deadlock).
func TestRunShellCappedBoundedMemory(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runShellCapped(shIn(dir, "yes '0123456789' | head -c 2000000"), 64*1024)
	if err != nil {
		t.Fatalf("capped run: %v", err)
	}
	if int64(len(out)) > 64*1024 {
		t.Errorf("output %d bytes exceeds cap", len(out))
	}
	if !strings.Contains(out, "0123456789") {
		t.Errorf("tail content not retained: ending %q", tail30(out))
	}
}

// TestRunShellCappedExitErrorPreserved — a failing chatty command still
// reports its exit error.
func TestRunShellCappedExitErrorPreserved(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runShellCapped(shIn(dir, "yes x | head -c 300000; exit 3"), 1024)
	if err == nil || !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("err = %v, want exit status 3", err)
	}
	if len(out) == 0 || len(out) > 2048 {
		t.Errorf("output length %d out of bounds", len(out))
	}
}

// TestRunShellCappedSmallOutputUnchanged — under-cap output returns whole.
func TestRunShellCappedSmallOutputUnchanged(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runShellCapped(shIn(dir, "echo hello world"), 1024)
	if err != nil || !strings.Contains(out, "hello world") {
		t.Fatalf("got %q, err %v", out, err)
	}
}

// TestRunShellCappedMixedStreams — stdout+stderr land in one capture and
// both drain.
func TestRunShellCappedMixedStreams(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runShellCapped(shIn(dir, "echo out1; echo err1 1>&2; echo out2"), 4096)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "out1") || !strings.Contains(out, "err1") || !strings.Contains(out, "out2") {
		t.Errorf("mixed capture incomplete: %q", out)
	}
}

// TestReadFileBoundedDirect — direct executor bounds allocation and flags
// truncation; small files read whole; missing file keeps not-found semantics.
func TestReadFileBoundedDirect(t *testing.T) {
	dir := t.TempDir()
	e, err := NewDirectExecutor(dir)
	if err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(big, []byte(strings.Repeat("a", 100000)), 0o644); err != nil {
		t.Fatal(err)
	}
	out, truncated, err := e.ReadFileBounded(context.Background(), "big.txt", 1000)
	if err != nil || !truncated || len(out) != 1000 {
		t.Fatalf("big: len=%d trunc=%v err=%v", len(out), truncated, err)
	}
	small := filepath.Join(dir, "small.txt")
	os.WriteFile(small, []byte("tiny"), 0o644)
	out, truncated, err = e.ReadFileBounded(context.Background(), "small.txt", 1000)
	if err != nil || truncated || out != "tiny" {
		t.Fatalf("small: %q trunc=%v err=%v", out, truncated, err)
	}
	if _, _, err := e.ReadFileBounded(context.Background(), "nope.txt", 100); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Errorf("missing file: err %v, want ErrFileNotFound", err)
	}
	if _, _, err := e.ReadFileBounded(context.Background(), "x", 0); err == nil {
		t.Error("maxBytes=0 must error")
	}
}

func tail30(s string) string {
	if len(s) > 30 {
		return s[len(s)-30:]
	}
	return s
}

// TestBoundedTailBufferBoundary — exact-cap, cap-1, cap+1, and single-write
// ≥ cap branches, plus the omission count.
func TestBoundedTailBufferBoundary(t *testing.T) {
	// cap=4, one write of 10 bytes → last 4, omitted 6.
	b := newBoundedTailBuffer(4)
	b.Write([]byte("0123456789"))
	tail, omitted := b.Snapshot()
	if tail != "6789" || omitted != 6 {
		t.Fatalf("big write: tail=%q omitted=%d, want 6789/6", tail, omitted)
	}
	// Exactly cap: nothing omitted.
	b2 := newBoundedTailBuffer(4)
	b2.Write([]byte("abcd"))
	tail, omitted = b2.Snapshot()
	if tail != "abcd" || omitted != 0 {
		t.Fatalf("exact cap: tail=%q omitted=%d", tail, omitted)
	}
	// cap-1 then 1 byte: whole input, nothing omitted.
	b3 := newBoundedTailBuffer(4)
	b3.Write([]byte("abc"))
	b3.Write([]byte("d"))
	tail, omitted = b3.Snapshot()
	if tail != "abcd" || omitted != 0 {
		t.Fatalf("cap-1+1: tail=%q omitted=%d", tail, omitted)
	}
	// cap+1 across small writes: first byte dropped.
	b4 := newBoundedTailBuffer(4)
	b4.Write([]byte("ab"))
	b4.Write([]byte("cde"))
	tail, omitted = b4.Snapshot()
	if tail != "bcde" || omitted != 1 {
		t.Fatalf("cap+1 small: tail=%q omitted=%d", tail, omitted)
	}
	// Empty write is a no-op.
	b5 := newBoundedTailBuffer(4)
	b5.Write(nil)
	if tail, omitted := b5.Snapshot(); tail != "" || omitted != 0 {
		t.Fatalf("empty write: tail=%q omitted=%d", tail, omitted)
	}
}

// TestRunShellCappedOmissionReported — a > cap stream reports the exact
// omitted byte count.
func TestRunShellCappedOmissionReported(t *testing.T) {
	dir := t.TempDir()
	_, omitted, err := runShellCapped(shIn(dir, "yes 'x' | head -c 100000"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if omitted != 100000-1024 {
		t.Errorf("omitted = %d, want %d", omitted, 100000-1024)
	}
}
