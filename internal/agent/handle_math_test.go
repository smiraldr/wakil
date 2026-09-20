package agent

import (
	"math"
	"strings"
	"testing"
)

func TestEvalMath_BasicArithmetic(t *testing.T) {
	tests := []struct {
		expr string
		want float64
	}{
		{"2 + 3", 5},
		{"2 + 3 * 4", 14},
		{"(2 + 3) * 4", 20},
		{"10 - 3 - 2", 5},
		{"6 / 2", 3},
		{"7 % 3", 1},
		{"2 ^ 10", 1024},
		{"2 ^ 3 ^ 2", 512}, // right-associative: 2^(3^2) = 2^9 = 512
	}
	for _, tc := range tests {
		got, err := EvalMath(tc.expr)
		if err != nil {
			t.Errorf("EvalMath(%q) error: %v", tc.expr, err)
			continue
		}
		if got != tc.want {
			t.Errorf("EvalMath(%q) = %g, want %g", tc.expr, got, tc.want)
		}
	}
}

func TestEvalMath_Functions(t *testing.T) {
	tests := []struct {
		expr string
		want float64
	}{
		{"sqrt(144)", 12},
		{"pow(2, 10)", 1024},
		{"abs(-5)", 5},
		{"floor(3.7)", 3},
		{"ceil(3.2)", 4},
		{"min(3, 7)", 3},
		{"max(3, 7)", 7},
		{"min(1, 2, 3)", 1},
		{"max(1, 2, 3)", 3},
	}
	for _, tc := range tests {
		got, err := EvalMath(tc.expr)
		if err != nil {
			t.Errorf("EvalMath(%q) error: %v", tc.expr, err)
			continue
		}
		if got != tc.want {
			t.Errorf("EvalMath(%q) = %g, want %g", tc.expr, got, tc.want)
		}
	}
}

func TestEvalMath_Constants(t *testing.T) {
	got, err := EvalMath("pi")
	if err != nil || got != math.Pi {
		t.Errorf("EvalMath(\"pi\") = %g, err=%v, want %g", got, err, math.Pi)
	}
	got, err = EvalMath("e")
	if err != nil || got != math.E {
		t.Errorf("EvalMath(\"e\") = %g, err=%v, want %g", got, err, math.E)
	}
}

func TestEvalMath_Unary(t *testing.T) {
	got, err := EvalMath("-5")
	if err != nil || got != -5 {
		t.Errorf("EvalMath(\"-5\") = %g, err=%v, want -5", got, err)
	}
	got, err = EvalMath("--5")
	if err != nil || got != 5 {
		t.Errorf("EvalMath(\"--5\") = %g, err=%v, want 5", got, err)
	}
	got, err = EvalMath("-(3 + 4)")
	if err != nil || got != -7 {
		t.Errorf("EvalMath(\"-(3+4)\") = %g, err=%v, want -7", got, err)
	}
}

// TestEvalMath_UnaryPowerPrecedence documents the design decision:
// unary binds tighter than ^, so -2^2 = (-2)^2 = 4 (not -4).
func TestEvalMath_UnaryPowerPrecedence(t *testing.T) {
	got, err := EvalMath("-2^2")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got != 4 {
		t.Errorf("EvalMath(\"-2^2\") = %g, want 4 (unary binds tighter than ^)", got)
	}
}

func TestEvalMath_DivisionByZero(t *testing.T) {
	_, err := EvalMath("1 / 0")
	if err == nil {
		t.Fatal("division by zero should return error")
	}
	if !strings.Contains(err.Error(), "division by zero") {
		t.Errorf("error should mention 'division by zero', got %q", err.Error())
	}
}

func TestEvalMath_ModuloByZero(t *testing.T) {
	_, err := EvalMath("5 % 0")
	if err == nil {
		t.Fatal("modulo by zero should return error")
	}
}

func TestEvalMath_TooLong(t *testing.T) {
	longExpr := strings.Repeat("1+", 200) + "1"
	_, err := EvalMath(longExpr)
	if err == nil {
		t.Fatal("expression > 256 chars should return error")
	}
	if !strings.Contains(err.Error(), "too long") {
		t.Errorf("error should mention 'too long', got %q", err.Error())
	}
}

func TestEvalMath_DeepNesting(t *testing.T) {
	deep := strings.Repeat("(", 15) + "1" + strings.Repeat(")", 15)
	_, err := EvalMath(deep)
	if err == nil {
		t.Fatal("nesting > 10 should return error")
	}
	if !strings.Contains(err.Error(), "nesting") {
		t.Errorf("error should mention 'nesting', got %q", err.Error())
	}
}

func TestEvalMath_PowerOverflow(t *testing.T) {
	_, err := EvalMath("10 ^ 999")
	if err == nil {
		t.Fatal("power overflow should return error")
	}
}

func TestEvalMath_ArithmeticOverflow(t *testing.T) {
	_, err := EvalMath("1e308 + 1e308")
	if err == nil {
		t.Fatal("1e308 + 1e308 should overflow")
	}
	_, err = EvalMath("1e300 * 1e300")
	if err == nil {
		t.Fatal("1e300 * 1e300 should overflow")
	}
}

func TestEvalMath_SqrtNegative(t *testing.T) {
	_, err := EvalMath("sqrt(-1)")
	if err == nil {
		t.Fatal("sqrt of negative should return error")
	}
}

func TestEvalMath_Empty(t *testing.T) {
	_, err := EvalMath("")
	if err == nil {
		t.Fatal("empty expression should return error")
	}
}

func TestEvalMath_UnknownFunction(t *testing.T) {
	_, err := EvalMath("sin(0)")
	if err == nil {
		t.Fatal("unknown function should return error")
	}
	if !strings.Contains(err.Error(), "unknown function") {
		t.Errorf("error should mention 'unknown function', got %q", err.Error())
	}
}

func TestEvalMath_UnknownIdentifier(t *testing.T) {
	_, err := EvalMath("foo + 1")
	if err == nil {
		t.Fatal("unknown identifier should return error")
	}
}

func TestEvalMath_UnexpectedChar(t *testing.T) {
	_, err := EvalMath("2 + # 3")
	if err == nil {
		t.Fatal("unexpected character should return error")
	}
}

func TestEvalMath_MissingClosingParen(t *testing.T) {
	_, err := EvalMath("(2 + 3")
	if err == nil {
		t.Fatal("missing closing paren should return error")
	}
}

func TestEvalMath_NoPanic(t *testing.T) {
	// Feed various nasty inputs — none should panic.
	nasty := []string{
		"", "  ", "()", ")(,!", "1e999", "1/0", "0^(-1)",
		strings.Repeat("(", 20),
		"\"injection\"",
		"null\x00byte",
		"2 +\n3",
		"1e", "1e+", ".", ".e2",
		"2^-3",
		"-0",
		"1e308 + 1e308",
	}
	for _, expr := range nasty {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("EvalMath(%q) panicked: %v", expr, r)
				}
			}()
			_, _ = EvalMath(expr)
		}()
	}
}

func TestEvalMath_Whitespace(t *testing.T) {
	got, err := EvalMath("  2   +   3  ")
	if err != nil || got != 5 {
		t.Errorf("EvalMath with whitespace = %g, err=%v, want 5", got, err)
	}
}

func TestEvalMath_NewlineWhitespace(t *testing.T) {
	got, err := EvalMath("2 +\n3")
	if err != nil {
		t.Fatalf("newline in expression should work: %v", err)
	}
	if got != 5 {
		t.Errorf("2 +\\n3 = %g, want 5", got)
	}
}

func TestEvalMath_FloatArithmetic(t *testing.T) {
	got, err := EvalMath("3.14 * 2")
	if err != nil {
		t.Errorf("error: %v", err)
	}
	if math.Abs(got-6.28) > 0.001 {
		t.Errorf("3.14*2 = %g, want ~6.28", got)
	}
}

func TestEvalMath_ExactDepthBoundary(t *testing.T) {
	// Depth 10 should pass, depth 11 should fail.
	tenDeep := strings.Repeat("(", 10) + "1" + strings.Repeat(")", 10)
	_, err := EvalMath(tenDeep)
	if err != nil {
		t.Errorf("depth 10 should pass, got error: %v", err)
	}
	elevenDeep := strings.Repeat("(", 11) + "1" + strings.Repeat(")", 11)
	_, err = EvalMath(elevenDeep)
	if err == nil {
		t.Fatal("depth 11 should fail")
	}
}
