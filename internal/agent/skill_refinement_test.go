package agent

// skill_refinement_test.go — tests for the Part B3 refinement invitation and
// the update_skill diff/no-op additions.

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestHandleLoadSkill_RefinementHintNotForSubagents(t *testing.T) {
	// Seed ONE store via the main agent, then load it from both a main and a
	// subagent App sharing that store (skillTestApp creates isolated stores
	// per call, so build the sub manually over the same store).
	main := skillTestApp(t, false, nil)
	main.handleSaveSkill(context.Background(), skillTC("save_skill", `{"key":"hint-test","value":"body line"}`))

	out := main.handleLoadSkill(context.Background(), skillTC("load_skill", `{"key":"hint-test"}`))
	if !strings.Contains(out, "body line") {
		t.Fatalf("main load should return the skill body, got: %s", out)
	}
	if !strings.Contains(out, "update_skill") {
		t.Errorf("main-agent load should carry the refinement hint, got: %s", out)
	}

	sub := &App{SkillStore: main.SkillStore, AgentPrefix: "sub-abc12345", IsSubagent: true}
	outSub := sub.handleLoadSkill(context.Background(), skillTC("load_skill", `{"key":"hint-test"}`))
	if !strings.Contains(outSub, "body line") {
		t.Fatalf("subagent load should return the skill body, got: %s", outSub)
	}
	if strings.Contains(outSub, "update_skill with the improved content") {
		t.Errorf("subagent load should NOT carry the refinement hint (writes are main-only), got: %s", outSub)
	}
}

func TestHandleUpdateSkill_NoOpGuardSkipsConfirmAndWrite(t *testing.T) {
	prompted := false
	app := skillTestApp(t, false, func(_, _, _ string, _ bool) bool {
		prompted = true
		return true
	})
	app.handleSaveSkill(context.Background(), skillTC("save_skill", `{"key":"noop-test","value":"same content"}`))
	prompted = false // the seed save legitimately prompted; only the update must not

	out := app.handleUpdateSkill(context.Background(), skillTC("update_skill", `{"key":"noop-test","value":"same content"}`))
	if !strings.Contains(out, "no-op") {
		t.Fatalf("expected no-op response, got: %s", out)
	}
	if prompted {
		t.Error("no-op update must not prompt the user")
	}
	// History must be unchanged (exactly 1 version).
	hist := app.handleSkillHistory(context.Background(), skillTC("skill_history", `{"key":"noop-test"}`))
	if strings.Count(hist, "#") != 1 {
		t.Errorf("no-op must not create a history version, got:\n%s", hist)
	}
}

func TestHandleUpdateSkill_DescriptionComposedBeforeNoOp(t *testing.T) {
	app := skillTestApp(t, false, nil)
	// Stored form is "desc\nbody". An update passing body + same description
	// composes to the same value → no-op, not a write.
	app.handleSaveSkill(context.Background(), skillTC("save_skill", `{"key":"desc-test","description":"My desc","value":"the body"}`))
	out := app.handleUpdateSkill(context.Background(), skillTC("update_skill", `{"key":"desc-test","description":"My desc","value":"the body"}`))
	if !strings.Contains(out, "no-op") {
		t.Errorf("description-composed identical update should be a no-op, got: %s", out)
	}
}

func TestHandleUpdateSkill_DeclineWritesNothing(t *testing.T) {
	app := skillTestApp(t, false, nil) // auto-approve for the seed save
	app.handleSaveSkill(context.Background(), skillTC("save_skill", `{"key":"decl-test","value":"old"}`))
	app.Confirm = func(_, _, _ string, _ bool) bool { return false } // decline the update

	out := app.handleUpdateSkill(context.Background(), skillTC("update_skill", `{"key":"decl-test","value":"new content"}`))
	if !strings.Contains(out, "declined") {
		t.Fatalf("expected declined response, got: %s", out)
	}
	e, err := app.SkillStore.getActiveSkill(context.Background(), "decl-test")
	if err != nil {
		t.Fatalf("get after decline: %v", err)
	}
	if e.Value != "old" {
		t.Errorf("declined update must not write, got value %q", e.Value)
	}
}

func TestHandleUpdateSkill_ApprovalStoresDiffedContentAndShowsDiff(t *testing.T) {
	var capturedDetail string
	app := skillTestApp(t, false, func(_, headline, detail string, _ bool) bool {
		capturedDetail = detail
		return true
	})
	app.handleSaveSkill(context.Background(), skillTC("save_skill", `{"key":"appr-test","value":"old line\nshared line"}`))

	out := app.handleUpdateSkill(context.Background(), skillTC("update_skill", `{"key":"appr-test","value":"new line\nshared line"}`))
	if !strings.Contains(out, "updated skill") {
		t.Fatalf("expected successful update, got: %s", out)
	}
	e, err := app.SkillStore.getActiveSkill(context.Background(), "appr-test")
	if err != nil {
		t.Fatalf("get after approval: %v", err)
	}
	if e.Value != "new line\nshared line" {
		t.Errorf("approval must store the exact proposed bytes, got %q", e.Value)
	}
	if !strings.Contains(capturedDetail, "- old line") || !strings.Contains(capturedDetail, "+ new line") {
		t.Errorf("confirm detail must show the diff, got:\n%s", capturedDetail)
	}
}

func TestSkillLineDiff_WhitespaceOnlyChange(t *testing.T) {
	out := skillLineDiff("a\nb", "a\nb\n")
	if !strings.Contains(out, "line endings") {
		t.Errorf("newline-only change should be labeled as such, got: %q", out)
	}
}

func TestSkillLineDiff_BasicChanges(t *testing.T) {
	oldVal := "line one\nline two\nline three"
	newVal := "line one\nline TWO updated\nline three\nline four"
	out := skillLineDiff(oldVal, newVal)
	if !strings.Contains(out, "- line two") {
		t.Errorf("diff should show removed line, got:\n%s", out)
	}
	if !strings.Contains(out, "+ line TWO updated") {
		t.Errorf("diff should show added line, got:\n%s", out)
	}
	if !strings.Contains(out, "+ line four") {
		t.Errorf("diff should show appended line, got:\n%s", out)
	}
	if strings.Contains(out, "line one") && !strings.Contains(out, "  line one") {
		// Unchanged lines are omitted (diff shows only changes).
		if strings.Contains(strings.Split(out, "\n")[0], "line one") &&
			!strings.HasPrefix(strings.Split(out, "\n")[0], "-") &&
			!strings.HasPrefix(strings.Split(out, "\n")[0], "+") {
			t.Errorf("unchanged lines should not appear as diff entries, got:\n%s", out)
		}
	}
}

func TestSkillLineDiff_IdenticalAndEmpty(t *testing.T) {
	if got := skillLineDiff("same\ncontent", "same\ncontent"); got != "(no line-level changes — differs only in line endings/trailing newline; byte counts above are exact)" {
		t.Errorf("identical content = %q, want the no-changes marker", got)
	}
	// Only trailing-newline difference should not explode.
	if got := skillLineDiff("a\nb", "a\nb\n"); got == "" {
		t.Error("trailing newline diff should still render something")
	}
}

func TestSkillLineDiff_CRLFAndLongLines(t *testing.T) {
	out := skillLineDiff("a\r\nb\r\n", "a\nB\n")
	if !strings.Contains(out, "+ B") {
		t.Errorf("CRLF should be normalized, got:\n%s", out)
	}
	long := strings.Repeat("x", 500)
	out = skillLineDiff(long, long+"!")
	if strings.Count(out, "…") == 0 {
		t.Errorf("very long lines should be truncated with ellipsis, got:\n%s", out)
	}
}

func TestSkillLineDiff_TruncationFallback(t *testing.T) {
	// A diff exceeding skillDiffMaxBytes must truncate with a note, not panic.
	var oldB, newB strings.Builder
	for i := 0; i < 1500; i++ {
		fmt.Fprintf(&oldB, "old line %d\n", i)
		fmt.Fprintf(&newB, "NEW line %d\n", i)
	}
	out := skillLineDiff(oldB.String(), newB.String())
	if !strings.Contains(out, "diff truncated") {
		t.Errorf("large diff should be truncated with a note, got len=%d", len(out))
	}
	if len(out) > skillDiffMaxBytes+200 { // small slack for the note
		t.Errorf("truncated diff too large: %d bytes", len(out))
	}
}

func TestSkillLineDiff_ControlCharsStripped(t *testing.T) {
	out := skillLineDiff("bad\x01line", "good\x02line")
	if strings.ContainsAny(out, "\x01\x02") {
		t.Errorf("control characters must be stripped, got:\n%s", out)
	}
}
