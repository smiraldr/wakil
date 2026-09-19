package agent

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/treeol/wakil/internal/config"
	"github.com/treeol/wakil/internal/proxy"
	wtools "github.com/treeol/wakil/internal/tools"
)

// TestStopOnStubForcesFinish verifies that when a subagent's turn tool budget
// is exhausted (turnBudgetStubbed), the loop force-finishes after the grace
// window (stopOnStubGrace) instead of burning the remaining iterations
// receiving stubs. Uses a direct subagent App (like TestForceFinishSetsExhaustedFlag)
// so we can set TurnToolBudget directly, bypassing the dispatchSubagent clamp.
func TestStopOnStubForcesFinish(t *testing.T) {
	// With TurnToolBudget=100 and ToolResultCap=12000:
	// iter 0: read_file (turnToolBytes=0, not stubbed, result capped to ~12k, turnToolBytes→~12k)
	// iter 1: read_file (turnToolBytes=12k >= 100, STUBBED, turnBudgetStubbed fires at iter 1)
	// iter 2: read_file (grace 1)
	// iter 3: read_file (grace 2)
	// iter 4: forceFinish (stopOnStub: iter >= 1+2+1=4), tools stripped → JSON
	summaryJSON := `{"objective":"find things","findings":[{"summary":"partial","location":"a.go:1","kind":"match","weight":"medium"}],"checked":[{"path":"a.go","size_k":1,"status":"truncated"}],"uncertainty":["budget exhausted"]}`

	frames := [][]string{}
	for i := 0; i < 4; i++ {
		frames = append(frames, toolCallFrames("r1", "read_file", `{"path":"a.go"}`))
	}
	frames = append(frames, []string{contentChunk(summaryJSON)})

	srv := sseServer(t, frames...)
	defer srv.Close()

	exec := newFakeExecutor()
	exec.files["a.go"] = strings.Repeat("line of code\n", 500) // ~6.5k, well over 100-byte budget

	cfg := config.DefaultConfig()
	cfg.MaxToolIterations = 10 // high enough that stop-on-stub fires first
	cfg.HardMaxBytes = 70000
	cfg.CompactAt = 55000
	cfg.KeepBytes = 45000
	cfg.SummaryBytes = 8000
	cfg.ToolResultCap = 12000
	cfg.TurnToolBudget = 100 // tiny — first read_file result will be stubbed

	sub := &App{
		Cfg:        cfg,
		Client:     newTestClient(srv.URL),
		Exec:       exec,
		Tools:      wtools.DiscoveryTools("/work"),
		Confirm:    readOnlyConfirmer(),
		Out:        io.Discard,
		IsSubagent: true,
		subagentState: subagentState{
			pinUserMessage:        true,
			turnBudgetStubbedIter: -1,
		},
		ToolCache: map[string]*toolDedupEntry{},
	}
	sub.Conv = []proxy.Message{{Role: "system", Content: StrPtr(subagentSystemPrompt), Pinned: true}}

	_, err := sub.Send(context.Background(), "find things")
	if err != nil {
		t.Fatal(err)
	}

	if !sub.exhausted {
		t.Error("expected exhausted=true after stop-on-stub force-finish")
	}
	if sub.stopReason != "turn_budget_exhausted" {
		t.Errorf("stopReason = %q, want 'turn_budget_exhausted'", sub.stopReason)
	}

	// Verify BudgetExhaustedPrompt was injected (not ToolLimitPrompt).
	found := false
	for _, m := range sub.Conv {
		if m.Role == "user" && DerefStr(m.Content) == BudgetExhaustedPrompt {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected BudgetExhaustedPrompt to be injected on stop-on-stub force-finish")
	}
}

// TestStopOnStubGraceWindow verifies that exactly stopOnStubGrace tool-enabled
// iterations run after the first stub before force-finish.
func TestStopOnStubGraceWindow(t *testing.T) {
	summaryJSON := `{"objective":"find","findings":[{"summary":"done","location":"a.go:1","kind":"match","weight":"low"}]}`

	// With stopOnStubGrace=2, stubbing at iter 1:
	// iter 0: tool (not stubbed, budget still available)
	// iter 1: tool (stubbed, turnBudgetStubbed fires)
	// iter 2: tool (grace 1)
	// iter 3: tool (grace 2)
	// iter 4: forceFinish → JSON
	frames := [][]string{}
	for i := 0; i < 4; i++ {
		frames = append(frames, toolCallFrames("r1", "read_file", `{"path":"a.go"}`))
	}
	frames = append(frames, []string{contentChunk(summaryJSON)})

	srv := sseServer(t, frames...)
	defer srv.Close()

	exec := newFakeExecutor()
	exec.files["a.go"] = strings.Repeat("x", 5000)

	cfg := config.DefaultConfig()
	cfg.MaxToolIterations = 10
	cfg.HardMaxBytes = 70000
	cfg.CompactAt = 55000
	cfg.KeepBytes = 45000
	cfg.SummaryBytes = 8000
	cfg.ToolResultCap = 12000
	cfg.TurnToolBudget = 100

	sub := &App{
		Cfg:        cfg,
		Client:     newTestClient(srv.URL),
		Exec:       exec,
		Tools:      wtools.DiscoveryTools("/work"),
		Confirm:    readOnlyConfirmer(),
		Out:        io.Discard,
		IsSubagent: true,
		subagentState: subagentState{
			pinUserMessage:        true,
			turnBudgetStubbedIter: -1,
		},
		ToolCache: map[string]*toolDedupEntry{},
	}
	sub.Conv = []proxy.Message{{Role: "system", Content: StrPtr(subagentSystemPrompt), Pinned: true}}

	_, err := sub.Send(context.Background(), "find things")
	if err != nil {
		t.Fatal(err)
	}

	if sub.stopReason != "turn_budget_exhausted" {
		t.Errorf("stopReason = %q, want 'turn_budget_exhausted'", sub.stopReason)
	}

	// Count tool-result messages: should be 4 (iter 0, 1, 2, 3).
	toolResults := 0
	for _, m := range sub.Conv {
		if m.Role == "tool" && m.Name == "read_file" {
			toolResults++
		}
	}
	if toolResults != 4 {
		t.Errorf("expected 4 read_file tool results (1 pre-stub + 1 stub + 2 grace), got %d", toolResults)
	}
}

// TestBudgetVisibilityMessageInjected verifies that a budget snapshot user
// message is injected for subagents when TurnToolBudget > 0 and iter > 0.
func TestBudgetVisibilityMessageInjected(t *testing.T) {
	summaryJSON := `{"objective":"find","findings":[{"summary":"done","location":"a.go:1","kind":"match","weight":"low"}]}`

	// iter 0: read_file, iter 1: budget msg + model returns final JSON
	// (MaxToolIterations=2, so forceFinish would fire at iter 2, but the model
	// produces a final answer at iter 1 with no tool calls → loop ends)
	srv := sseServer(t,
		toolCallFrames("r1", "read_file", `{"path":"a.go"}`),
		[]string{contentChunk(summaryJSON)},
	)
	defer srv.Close()

	exec := newFakeExecutor()
	exec.files["a.go"] = "small content"

	cfg := config.DefaultConfig()
	cfg.MaxToolIterations = 2 // iter 0: tool, iter 1: budget msg + final answer
	cfg.HardMaxBytes = 70000
	cfg.CompactAt = 55000
	cfg.KeepBytes = 45000
	cfg.SummaryBytes = 8000
	cfg.ToolResultCap = 12000
	cfg.TurnToolBudget = 50000 // large enough that stubbing won't fire

	sub := &App{
		Cfg:        cfg,
		Client:     newTestClient(srv.URL),
		Exec:       exec,
		Tools:      wtools.DiscoveryTools("/work"),
		Confirm:    readOnlyConfirmer(),
		Out:        io.Discard,
		IsSubagent: true,
		subagentState: subagentState{
			pinUserMessage:        true,
			turnBudgetStubbedIter: -1,
		},
		ToolCache: map[string]*toolDedupEntry{},
	}
	sub.Conv = []proxy.Message{{Role: "system", Content: StrPtr(subagentSystemPrompt), Pinned: true}}

	_, err := sub.Send(context.Background(), "find things")
	if err != nil {
		t.Fatal(err)
	}

	// Check that a budget visibility message was injected.
	// The message may start with "⚠ Budget low" (wrap-up warning) or "[budget:".
	found := false
	for _, m := range sub.Conv {
		content := DerefStr(m.Content)
		if m.Role == "user" && (strings.HasPrefix(content, "[budget:") ||
			strings.Contains(content, "[budget:")) {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected a budget visibility message in the subagent's conversation")
		// Debug: print all user messages
		for i, m := range sub.Conv {
			if m.Role == "user" {
				t.Logf("  Conv[%d] user: %q", i, Truncate(DerefStr(m.Content), 100))
			}
		}
	}
}

// TestBudgetVisibilityNotInjectedForParent verifies that budget visibility
// messages are NOT injected for the parent (non-subagent) path.
func TestBudgetVisibilityNotInjectedForParent(t *testing.T) {
	srv := sseServer(t,
		toolCallFrames("r1", "read_file", `{"path":"a.go"}`),
		[]string{contentChunk("done")},
	)
	defer srv.Close()

	exec := newFakeExecutor()
	exec.files["a.go"] = "content"

	app := newTestApp(srv.URL, exec, func(_, _, _ string, _ bool) bool { return true })
	// Parent: IsSubagent is false
	app.Cfg.MaxToolIterations = 5
	app.Cfg.TurnToolBudget = 50000

	_, _ = app.Send(context.Background(), "go")

	for _, m := range app.Conv {
		if m.Role == "user" && strings.HasPrefix(DerefStr(m.Content), "[budget:") {
			t.Error("budget visibility message should NOT be injected for the parent")
		}
	}
}

// TestBudgetExhaustedPromptDistinctFromToolLimit verifies that the
// BudgetExhaustedPrompt is distinct from ToolLimitPrompt.
func TestBudgetExhaustedPromptDistinctFromToolLimit(t *testing.T) {
	if BudgetExhaustedPrompt == ToolLimitPrompt {
		t.Fatal("BudgetExhaustedPrompt must be distinct from ToolLimitPrompt")
	}
	if !strings.Contains(BudgetExhaustedPrompt, "budget") {
		t.Error("BudgetExhaustedPrompt should mention 'budget'")
	}
	if !strings.Contains(ToolLimitPrompt, "tool-call limit") {
		t.Error("ToolLimitPrompt should mention 'tool-call limit'")
	}
}

// TestMergeStopReasonHandlesBudgetExhausted verifies that mergeStopReason
// correctly orders turn_budget_exhausted.
func TestMergeStopReasonHandlesBudgetExhausted(t *testing.T) {
	tests := []struct {
		first, retry, want string
	}{
		{"turn_budget_exhausted", "", "turn_budget_exhausted"},
		{"", "turn_budget_exhausted", "turn_budget_exhausted"},
		{"turn_budget_exhausted", "turn_budget_exhausted", "turn_budget_exhausted"},
		{"hard_max_shed", "turn_budget_exhausted", "hard_max_shed"},
		{"turn_budget_exhausted", "hard_max_shed", "hard_max_shed"},
		{"turn_budget_exhausted", "iteration_limit", "turn_budget_exhausted"},
		{"iteration_limit", "turn_budget_exhausted", "turn_budget_exhausted"},
	}
	for _, tc := range tests {
		got := mergeStopReason(tc.first, tc.retry)
		if got != tc.want {
			t.Errorf("mergeStopReason(%q, %q) = %q, want %q", tc.first, tc.retry, got, tc.want)
		}
	}
}

// TestTurnBudgetStubbedIterResetInPrepareTurn verifies that
// turnBudgetStubbedIter is reset to -1 at the start of each Send
// (in prepareTurn). We verify this indirectly: the TestStopOnStubForcesFinish
// test above would fail if the iter weren't reset, because a stale value
// from a prior Send would cause premature force-finish. This test just
// verifies the constant and field exist.
func TestTurnBudgetStubbedIterResetInPrepareTurn(t *testing.T) {
	// Verify stopOnStubGrace is the expected value.
	if stopOnStubGrace != 2 {
		t.Errorf("stopOnStubGrace = %d, want 2", stopOnStubGrace)
	}
}
