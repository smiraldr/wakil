package agent

// Tests for the global subagent semaphore (ensureSubagentGlobalSem):
// race-free concurrent initialization, identity stability, the
// small-first-batch regression, and overlapping-batch cap enforcement.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestEnsureSubagentGlobalSemConcurrentInit verifies that concurrent callers
// all receive the same non-nil channel. Run with -race to confirm no data race
// on the lazy initialization path.
func TestEnsureSubagentGlobalSemConcurrentInit(t *testing.T) {
	app := newTestApp("http://unused", newFakeExecutor(), func(_, _, _ string, _ bool) bool { return true })

	barrier, wg := startBarrier(50)
	results := make([]chan chan struct{}, 50)
	for i := range results {
		results[i] = make(chan chan struct{}, 1)
	}
	for i := 0; i < 50; i++ {
		go func(idx int) {
			defer wg.Done()
			<-barrier
			s := app.ensureSubagentGlobalSem(4)
			results[idx] <- s
		}(i)
	}
	close(barrier)
	wg.Wait()

	first := <-results[0]
	if first == nil {
		t.Fatal("semaphore must not be nil")
	}
	if cap(first) != 4 {
		t.Fatalf("first: cap=%d, want 4", cap(first))
	}
	// Verify all remaining callers got the same channel instance.
	for i := 1; i < len(results); i++ {
		got := <-results[i]
		if got != first {
			t.Errorf("caller %d got a different channel instance — all callers must share one semaphore", i)
		}
		if cap(got) != 4 {
			t.Errorf("caller %d: cap=%d, want 4", i, cap(got))
		}
	}
}

// TestEnsureSubagentGlobalSemIdentityStability verifies that later calls with
// larger or smaller maxPar return the SAME channel (never replaced).
func TestEnsureSubagentGlobalSemIdentityStability(t *testing.T) {
	app := newTestApp("http://unused", newFakeExecutor(), func(_, _, _ string, _ bool) bool { return true })

	first := app.ensureSubagentGlobalSem(2)
	if first == nil || cap(first) != 2 {
		t.Fatalf("first: cap=%d, want 2", cap(first))
	}

	// Larger request: must return the same channel, not a new bigger one.
	larger := app.ensureSubagentGlobalSem(8)
	if larger != first {
		t.Error("larger maxPar returned a different channel — must never replace")
	}
	if cap(larger) != 2 {
		t.Errorf("larger: cap=%d, want 2 (unchanged)", cap(larger))
	}

	// Smaller request: same channel.
	smaller := app.ensureSubagentGlobalSem(1)
	if smaller != first {
		t.Error("smaller maxPar returned a different channel")
	}
	if cap(smaller) != 2 {
		t.Errorf("smaller: cap=%d, want 2 (unchanged)", cap(smaller))
	}
}

// TestGlobalSemSmallFirstBatchRegression verifies that a first batch with 1
// job (maxPar=4 config) does NOT permanently limit the global semaphore to 1.
// A subsequent batch must be able to run up to 4 children concurrently.
func TestGlobalSemSmallFirstBatchRegression(t *testing.T) {
	var inFlight, maxInFlight atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := inFlight.Add(1)
		for {
			prev := maxInFlight.Load()
			if cur <= prev || maxInFlight.CompareAndSwap(prev, cur) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		inFlight.Add(-1)
		writeSSE(w, contentChunk(summaryFor(taskFromBody([]byte(r.URL.Path)))))
	}))
	defer srv.Close()

	app := newTestApp(srv.URL, newFakeExecutor(), func(_, _, _ string, _ bool) bool { return true })
	app.Cfg.MaxParallelSubagents = 4

	// First batch: 1 job. The global semaphore should be sized to 4 (the
	// unclamped config), NOT 1 (the job count).
	jobs1 := []subagentJob{{Index: 0, Task: "warmup", ChatID: NewChatID()}}
	app.runSubagentJobs(context.Background(), jobs1, "", 0, nil)

	// Verify the semaphore was sized to 4, not 1.
	sem := app.ensureSubagentGlobalSem(4)
	if cap(sem) != 4 {
		t.Fatalf("global sem cap=%d after first batch, want 4 — small first batch pinned the cap", cap(sem))
	}

	// Second batch: 4 jobs. All 4 should run concurrently because the global
	// sem was sized to 4 from the start.
	maxInFlight.Store(0)
	jobs2 := []subagentJob{
		{Index: 0, Task: "A", ChatID: NewChatID()},
		{Index: 1, Task: "B", ChatID: NewChatID()},
		{Index: 2, Task: "C", ChatID: NewChatID()},
		{Index: 3, Task: "D", ChatID: NewChatID()},
	}
	app.runSubagentJobs(context.Background(), jobs2, "", 0, nil)

	if got := maxInFlight.Load(); got < 4 {
		t.Errorf("second batch: max concurrent=%d, want >=4 — global sem was pinned by first batch", got)
	}
}

// TestGlobalSemOverlappingBatchesEnforceCap verifies that two overlapping
// batches do NOT exceed the global cap. With maxPar=2, two batches of 2 jobs
// each overlap; total concurrent in-flight must never exceed 2.
func TestGlobalSemOverlappingBatchesEnforceCap(t *testing.T) {
	var inFlight, maxInFlight atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := inFlight.Add(1)
		for {
			prev := maxInFlight.Load()
			if cur <= prev || maxInFlight.CompareAndSwap(prev, cur) {
				break
			}
		}
		time.Sleep(80 * time.Millisecond) // widen the overlap window
		inFlight.Add(-1)
		writeSSE(w, contentChunk("ok"))
	}))
	defer srv.Close()

	app := newTestApp(srv.URL, newFakeExecutor(), func(_, _, _ string, _ bool) bool { return true })
	app.Cfg.MaxParallelSubagents = 2

	jobs := []subagentJob{
		{Index: 0, Task: "A", ChatID: NewChatID()},
		{Index: 1, Task: "B", ChatID: NewChatID()},
	}

	// Launch two batches concurrently so they overlap at the semaphore.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		app.runSubagentJobs(context.Background(), jobs, "", 0, nil)
	}()
	go func() {
		defer wg.Done()
		app.runSubagentJobs(context.Background(), jobs, "", 0, nil)
	}()
	wg.Wait()

	if got := maxInFlight.Load(); got > 2 {
		t.Errorf("overlapping batches: max concurrent=%d, want <=2 (global cap violated)", got)
	}
}

// TestRaceEnsureSubagentGlobalSem exercises concurrent ensureSubagentGlobalSem
// calls to verify no data race on the lazy init path. Run with -race.
func TestRaceEnsureSubagentGlobalSem(t *testing.T) {
	app := newTestApp("http://unused", newFakeExecutor(), func(_, _, _ string, _ bool) bool { return true })

	barrier, wg := startBarrier(4)
	const N = 300
	go func() {
		defer wg.Done()
		<-barrier
		for i := 0; i < N; i++ {
			_ = app.ensureSubagentGlobalSem(4)
		}
	}()
	go func() {
		defer wg.Done()
		<-barrier
		for i := 0; i < N; i++ {
			_ = app.ensureSubagentGlobalSem(8)
		}
	}()
	go func() {
		defer wg.Done()
		<-barrier
		for i := 0; i < N; i++ {
			_ = app.ensureSubagentGlobalSem(2)
		}
	}()
	go func() {
		defer wg.Done()
		<-barrier
		for i := 0; i < N; i++ {
			_ = app.ensureSubagentGlobalSem(1)
		}
	}()
	close(barrier)
	wg.Wait()

	// Final sanity: semaphore exists and has the size from the first call.
	sem := app.ensureSubagentGlobalSem(4)
	if sem == nil {
		t.Fatal("semaphore must not be nil after concurrent init")
	}
	if cap(sem) < 1 {
		t.Errorf("sem cap=%d, want >=1", cap(sem))
	}
}

// TestGlobalSemNonPositiveMaxPar verifies that a non-positive maxPar is
// normalized to 1 (the minimum safe capacity).
func TestGlobalSemNonPositiveMaxPar(t *testing.T) {
	app := newTestApp("http://unused", newFakeExecutor(), func(_, _, _ string, _ bool) bool { return true })

	sem := app.ensureSubagentGlobalSem(0)
	if sem == nil {
		t.Fatal("sem must not be nil for maxPar=0")
	}
	if cap(sem) != 1 {
		t.Errorf("maxPar=0: cap=%d, want 1", cap(sem))
	}
}

// TestGlobalSemPersistsAcrossBatches verifies the semaphore channel is the
// same object across multiple batch calls (not re-created each time).
func TestGlobalSemPersistsAcrossBatches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeSSE(w, contentChunk("ok"))
	}))
	defer srv.Close()

	app := newTestApp(srv.URL, newFakeExecutor(), func(_, _, _ string, _ bool) bool { return true })
	app.Cfg.MaxParallelSubagents = 3

	jobs := []subagentJob{{Index: 0, Task: "x", ChatID: NewChatID()}}
	app.runSubagentJobs(context.Background(), jobs, "", 0, nil)
	sem1 := app.ensureSubagentGlobalSem(3)

	app.runSubagentJobs(context.Background(), jobs, "", 0, nil)
	sem2 := app.ensureSubagentGlobalSem(3)

	if sem1 != sem2 {
		t.Error("semaphore changed between batches — must persist for the App lifetime")
	}
}

// suppress unused-import warnings for helpers used in other test files
// in the same package.
var _ = strings.Contains
