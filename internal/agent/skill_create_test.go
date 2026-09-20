package agent

// skill_create_test.go — unit tests for the /skill-create draft extraction,
// validation, and rendering. Dispatch-level behavior is exercised through the
// extractSkillDraft / parseSkillDraft seams (decoupled from dispatch per the
// Mashūra plan review).

import (
	"context"
	"strings"
	"testing"

	"github.com/treeol/wakil/internal/memory"
)

func draftSummary(findings ...Finding) SubagentSummary {
	return SubagentSummary{
		Objective: "research the topic",
		Status:    "complete",
		Findings:  findings,
	}
}

func TestExtractSkillDraft_HappyPath(t *testing.T) {
	body := "Deploy the service with make deploy\nRun the smoke tests afterwards."
	s := draftSummary(Finding{Kind: "skill_draft", Location: "deploy-service", Summary: body, Weight: "medium"})
	d, err := extractSkillDraft(s)
	if err != nil {
		t.Fatalf("extractSkillDraft: %v", err)
	}
	if d == nil {
		t.Fatal("expected a draft, got nil")
	}
	if d.Key != "deploy-service" {
		t.Errorf("Key = %q, want deploy-service", d.Key)
	}
	if d.Value != body {
		t.Errorf("Value = %q, want the full body", d.Value)
	}
}

func TestExtractSkillDraft_IgnoresOtherFindings(t *testing.T) {
	s := draftSummary(
		Finding{Kind: "fact", Location: "a.go:10", Summary: "some fact", Weight: "low"},
		Finding{Kind: "skill_draft", Location: "my-skill", Summary: "Description line\nBody."},
	)
	d, err := extractSkillDraft(s)
	if err != nil {
		t.Fatalf("extractSkillDraft: %v", err)
	}
	if d == nil || d.Key != "my-skill" {
		t.Fatalf("expected my-skill draft, got %+v", d)
	}
}

func TestExtractSkillDraft_NoDraftIsDistinctFromMalformed(t *testing.T) {
	// No skill_draft finding at all → (nil, nil): valid "nothing to save".
	s := draftSummary(Finding{Kind: "fact", Location: "x", Summary: "unrelated"})
	d, err := extractSkillDraft(s)
	if err != nil {
		t.Fatalf("expected nil error for no-draft outcome, got %v", err)
	}
	if d != nil {
		t.Fatalf("expected nil draft, got %+v", d)
	}

	// Incomplete dispatch is an error, not a silent no-draft.
	incomplete := SubagentSummary{Status: "incomplete", StopReason: "iteration_limit"}
	if _, err := extractSkillDraft(incomplete); err == nil {
		t.Fatal("expected error for incomplete summary")
	}
	// Empty objective also counts as "did not complete".
	empty := SubagentSummary{Status: "complete"}
	if _, err := extractSkillDraft(empty); err == nil {
		t.Fatal("expected error for empty objective")
	}
	// Unknown non-"complete" status is rejected (only documented success
	// states may reach approval).
	unknown := SubagentSummary{Status: "partial", Objective: "did things"}
	if _, err := extractSkillDraft(unknown); err == nil {
		t.Fatal("expected error for non-complete status")
	}
}

func TestExtractSkillDraft_MultipleDraftsRejected(t *testing.T) {
	s := draftSummary(
		Finding{Kind: "skill_draft", Location: "a", Summary: "one"},
		Finding{Kind: "skill_draft", Location: "b", Summary: "two"},
	)
	if _, err := extractSkillDraft(s); err == nil {
		t.Fatal("expected error for multiple skill_draft findings")
	}
}

func TestParseSkillDraft_KeyValidation(t *testing.T) {
	cases := []struct {
		key    string
		wantOK bool
	}{
		{"my-skill", true},
		{"", false},
		{"Has Spaces", false},
		{"UPPER", false},
	}
	for _, c := range cases {
		_, err := parseSkillDraft(c.key, "Body line.")
		if c.wantOK && err != nil {
			t.Errorf("parseSkillDraft(%q): unexpected error %v", c.key, err)
		}
		if !c.wantOK && err == nil {
			t.Errorf("parseSkillDraft(%q): expected error", c.key)
		}
	}
}

func TestParseSkillDraft_EmptyAndOversizedBody(t *testing.T) {
	if _, err := parseSkillDraft("ok-key", "   "); err == nil {
		t.Fatal("expected error for empty body")
	}
	huge := strings.Repeat("x", skillDraftMaxBytes+1)
	_, err := parseSkillDraft("ok-key", huge)
	if err == nil {
		t.Fatal("expected error for oversized body")
	}
	if !strings.Contains(err.Error(), "rejected, not truncated") {
		t.Errorf("error should state rejection-not-truncation, got: %v", err)
	}
	// Exact boundary passes.
	ok := strings.Repeat("x", skillDraftMaxBytes)
	if _, err := parseSkillDraft("ok-key", ok); err != nil {
		t.Errorf("body exactly at the cap should pass, got: %v", err)
	}
}

func TestExtractSkillDraft_MalformedDraftIsErrorNotNoDraft(t *testing.T) {
	// A skill_draft finding with an invalid key is a hard error — the user
	// must see that research output was unusable, not a silent "nothing".
	s := draftSummary(Finding{Kind: "skill_draft", Location: "BAD KEY", Summary: "body"})
	if _, err := extractSkillDraft(s); err == nil {
		t.Fatal("expected error for malformed skill_draft finding")
	}
}

func TestRenderSkillCreateFailure_NeverSuggestsSaved(t *testing.T) {
	out := renderSkillCreateFailure("docker", SubagentSummary{Status: "incomplete", StopReason: "error"}, nil)
	if !strings.Contains(out, "Nothing was saved") {
		t.Errorf("failure report must state nothing was saved, got: %s", out)
	}
}

func TestRenderSkillCreateNoDraft_StatesNothingSaved(t *testing.T) {
	out := renderSkillCreateNoDraft("docker", SubagentSummary{Uncertainty: []string{"topic too vague"}})
	if !strings.Contains(out, "Nothing was saved") {
		t.Errorf("no-draft report must state nothing was saved, got: %s", out)
	}
	if !strings.Contains(out, "topic too vague") {
		t.Errorf("no-draft report should surface uncertainty, got: %s", out)
	}
}

func TestRenderSkillDraftPreview_ShowsFullContent(t *testing.T) {
	body := strings.Repeat("line\n", 200) // well over the 500-byte confirm preview
	out := renderSkillDraftPreview("testing", &skillDraft{Key: "big-skill", Value: body}, nil, false, "")
	if !strings.Contains(out, body) {
		t.Error("preview must contain the FULL draft content")
	}
	if !strings.Contains(out, "Tainted: yes") {
		t.Error("preview must disclose taint")
	}
}

func TestRenderSkillDraftPreview_ShowsOverlap(t *testing.T) {
	out := renderSkillDraftPreview("deploy", &skillDraft{Key: "deploy-svc", Value: "body"},
		nil, false, "\n  - deploy-service: existing deployment skill")
	if !strings.Contains(out, "deploy-service") {
		t.Error("preview must show overlapping store entries so the user can catch near-duplicates")
	}
}

func TestRenderSkillDraftPreview_UpdateShowsOldSize(t *testing.T) {
	out := renderSkillDraftPreview("testing", &skillDraft{Key: "k", Value: "new"},
		&memory.Entry{Value: "old content here"}, true, "")
	if !strings.Contains(out, "old: 16 bytes") {
		t.Errorf("update preview should mention old size, got: %s", out)
	}
}

func TestUpdateSizeNote(t *testing.T) {
	if got := updateSizeNote(nil, "x"); got != "" {
		t.Errorf("updateSizeNote(nil) = %q, want empty", got)
	}
	note := updateSizeNote(&memory.Entry{Value: "0123456789"}, "y")
	if !strings.Contains(note, "10") {
		t.Errorf("updateSizeNote should mention old size, got %q", note)
	}
}

func TestHandleSkillCreateCommand_SubagentRefused(t *testing.T) {
	// Defense in depth: even if routing ever let a subagent reach this, the
	// write path must refuse before any research or prompt.
	app := &App{IsSubagent: true}
	_, err := handleSkillCreateCommand(context.Background(), app, "topic")
	if err == nil || !strings.Contains(err.Error(), "main-agent only") {
		t.Fatalf("expected main-agent-only refusal, got err=%v", err)
	}
}

func TestHandleSkillCreateCommand_EmptyTopic(t *testing.T) {
	app := &App{}
	_, err := handleSkillCreateCommand(context.Background(), app, "   ")
	if err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("expected usage error for blank topic, got err=%v", err)
	}
}
