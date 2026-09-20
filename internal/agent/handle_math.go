package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/treeol/wakil/internal/proxy"
)

// handle_math.go — Pure Go expression evaluator for the math_eval tool.
//
// A hand-rolled recursive descent parser with an allowlisted function set.
// No external dependencies, no shell, no Python, no eval. Runs in-process.
//
// Grammar (lowest to highest precedence):
//
//	expr     = term (('+' | '-') term)*
//	term     = factor (('*' | '/' | '%') factor)*
//	factor   = power
//	power    = unary ('^' power)?           // right-associative
//	unary    = ('-' | '+') unary | primary
//	primary  = number | func '(' args ')' | '(' expr ')'
//	args     = expr (',' expr)*
//
// Supported functions: sqrt, pow, abs, floor, ceil, min, max
// Constants: pi, e
//
// DoS guards: expression length cap (256), nesting depth cap (10),
// division-by-zero → error (not panic).

const (
	mathExprMaxLen   = 256
	mathMaxDepth     = 10
	mathNumberMaxLen = 30 // digits in a single number literal
)

// mathError is returned by the parser/evaluator for any error condition.
type mathError struct{ msg string }

func (e mathError) Error() string { return e.msg }

// mathParser is a recursive descent parser/evaluator for arithmetic.
type mathParser struct {
	s     string // input
	pos   int    // current position
	depth int    // nesting depth
}

// EvalMath parses and evaluates a mathematical expression. Returns the
// result as a float64 or an error. No panics on any input.
func EvalMath(expr string) (float64, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return 0, mathError{"empty expression"}
	}
	if len(expr) > mathExprMaxLen {
		return 0, mathError{fmt.Sprintf("expression too long (max %d chars)", mathExprMaxLen)}
	}
	p := &mathParser{s: expr}
	result, err := p.parseExpr()
	if err != nil {
		return 0, err
	}
	p.skipWS()
	if p.pos < len(p.s) {
		return 0, mathError{fmt.Sprintf("unexpected character %q at position %d", p.s[p.pos], p.pos)}
	}
	// Terminal overflow/NaN check — catches overflow from +, -, *, /, %
	// that wasn't caught by the per-operation power checks.
	if math.IsInf(result, 0) || math.IsNaN(result) {
		return 0, mathError{"overflow or undefined result"}
	}
	return result, nil
}

func (p *mathParser) skipWS() {
	for p.pos < len(p.s) && (p.s[p.pos] == ' ' || p.s[p.pos] == '\t' || p.s[p.pos] == '\n' || p.s[p.pos] == '\r') {
		p.pos++
	}
}

func (p *mathParser) consume(b byte) bool {
	p.skipWS()
	if p.pos < len(p.s) && p.s[p.pos] == b {
		p.pos++
		return true
	}
	return false
}

func (p *mathParser) parseExpr() (float64, error) {
	left, err := p.parseTerm()
	if err != nil {
		return 0, err
	}
	for {
		p.skipWS()
		if p.pos >= len(p.s) {
			break
		}
		op := p.s[p.pos]
		if op != '+' && op != '-' {
			break
		}
		p.pos++
		right, err := p.parseTerm()
		if err != nil {
			return 0, err
		}
		if op == '+' {
			left += right
		} else {
			left -= right
		}
	}
	return left, nil
}

func (p *mathParser) parseTerm() (float64, error) {
	left, err := p.parsePower()
	if err != nil {
		return 0, err
	}
	for {
		p.skipWS()
		if p.pos >= len(p.s) {
			break
		}
		op := p.s[p.pos]
		if op != '*' && op != '/' && op != '%' {
			break
		}
		p.pos++
		right, err := p.parsePower()
		if err != nil {
			return 0, err
		}
		switch op {
		case '*':
			left *= right
		case '/':
			if right == 0 {
				return 0, mathError{"division by zero"}
			}
			left /= right
		case '%':
			if right == 0 {
				return 0, mathError{"modulo by zero"}
			}
			left = math.Mod(left, right)
		}
	}
	return left, nil
}

func (p *mathParser) parsePower() (float64, error) {
	base, err := p.parseUnary()
	if err != nil {
		return 0, err
	}
	p.skipWS()
	if p.pos < len(p.s) && p.s[p.pos] == '^' {
		p.pos++
		exp, err := p.parsePower() // right-associative
		if err != nil {
			return 0, err
		}
		// Guard against overflow.
		if base == 0 && exp < 0 {
			return 0, mathError{"zero to negative power"}
		}
		if math.IsInf(exp, 0) || math.IsNaN(exp) {
			return 0, mathError{"exponent overflow"}
		}
		result := math.Pow(base, exp)
		if math.IsInf(result, 0) || math.IsNaN(result) {
			return 0, mathError{"power overflow"}
		}
		return result, nil
	}
	return base, nil
}

func (p *mathParser) parseUnary() (float64, error) {
	p.skipWS()
	if p.pos < len(p.s) && p.s[p.pos] == '-' {
		p.pos++
		v, err := p.parseUnary()
		return -v, err
	}
	if p.pos < len(p.s) && p.s[p.pos] == '+' {
		p.pos++
		return p.parseUnary()
	}
	return p.parsePrimary()
}

func (p *mathParser) parsePrimary() (float64, error) {
	p.skipWS()
	if p.pos >= len(p.s) {
		return 0, mathError{"unexpected end of expression"}
	}

	// Parenthesized expression.
	if p.s[p.pos] == '(' {
		p.depth++
		if p.depth > mathMaxDepth {
			return 0, mathError{fmt.Sprintf("nesting too deep (max %d)", mathMaxDepth)}
		}
		p.pos++
		result, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		if !p.consume(')') {
			return 0, mathError{"missing closing parenthesis"}
		}
		p.depth--
		return result, nil
	}

	// Number or function/constant.
	if p.s[p.pos] == '.' || (p.s[p.pos] >= '0' && p.s[p.pos] <= '9') {
		return p.parseNumber()
	}

	// Try to read an identifier (function name or constant).
	if isIdentStart(p.s[p.pos]) {
		return p.parseIdentOrFunc()
	}

	return 0, mathError{fmt.Sprintf("unexpected character %q at position %d", p.s[p.pos], p.pos)}
}

func (p *mathParser) parseNumber() (float64, error) {
	start := p.pos
	// Integer part.
	for p.pos < len(p.s) && p.s[p.pos] >= '0' && p.s[p.pos] <= '9' {
		p.pos++
	}
	// Fractional part.
	if p.pos < len(p.s) && p.s[p.pos] == '.' {
		p.pos++
		for p.pos < len(p.s) && p.s[p.pos] >= '0' && p.s[p.pos] <= '9' {
			p.pos++
		}
	}
	// Exponent.
	if p.pos < len(p.s) && (p.s[p.pos] == 'e' || p.s[p.pos] == 'E') {
		p.pos++
		if p.pos < len(p.s) && (p.s[p.pos] == '+' || p.s[p.pos] == '-') {
			p.pos++
		}
		for p.pos < len(p.s) && p.s[p.pos] >= '0' && p.s[p.pos] <= '9' {
			p.pos++
		}
	}
	numStr := p.s[start:p.pos]
	if len(numStr) > mathNumberMaxLen {
		return 0, mathError{"number too large"}
	}
	var f float64
	_, err := fmt.Sscanf(numStr, "%g", &f)
	if err != nil {
		return 0, mathError{fmt.Sprintf("invalid number %q", numStr)}
	}
	return f, nil
}

func (p *mathParser) parseIdentOrFunc() (float64, error) {
	start := p.pos
	for p.pos < len(p.s) && isIdentChar(p.s[p.pos]) {
		p.pos++
	}
	name := strings.ToLower(p.s[start:p.pos])

	// Check for function call.
	p.skipWS()
	if p.pos < len(p.s) && p.s[p.pos] == '(' {
		p.depth++
		if p.depth > mathMaxDepth {
			return 0, mathError{fmt.Sprintf("nesting too deep (max %d)", mathMaxDepth)}
		}
		p.pos++
		var args []float64
		// Parse first argument.
		arg, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		args = append(args, arg)
		// Parse additional arguments.
		for {
			p.skipWS()
			if p.pos >= len(p.s) || p.s[p.pos] != ',' {
				break
			}
			p.pos++
			arg, err := p.parseExpr()
			if err != nil {
				return 0, err
			}
			args = append(args, arg)
		}
		if !p.consume(')') {
			return 0, mathError{"missing closing parenthesis"}
		}
		p.depth--
		return callMathFunc(name, args)
	}

	// Constant.
	switch name {
	case "pi":
		return math.Pi, nil
	case "e":
		return math.E, nil
	default:
		return 0, mathError{fmt.Sprintf("unknown identifier %q", name)}
	}
}

// callMathFunc dispatches to the allowlisted math functions.
func callMathFunc(name string, args []float64) (float64, error) {
	switch name {
	case "sqrt":
		if len(args) != 1 {
			return 0, mathError{"sqrt requires 1 argument"}
		}
		if args[0] < 0 {
			return 0, mathError{"sqrt of negative number"}
		}
		return math.Sqrt(args[0]), nil
	case "pow":
		if len(args) != 2 {
			return 0, mathError{"pow requires 2 arguments"}
		}
		if args[0] == 0 && args[1] < 0 {
			return 0, mathError{"zero to negative power"}
		}
		result := math.Pow(args[0], args[1])
		if math.IsInf(result, 0) || math.IsNaN(result) {
			return 0, mathError{"pow overflow"}
		}
		return result, nil
	case "abs":
		if len(args) != 1 {
			return 0, mathError{"abs requires 1 argument"}
		}
		return math.Abs(args[0]), nil
	case "floor":
		if len(args) != 1 {
			return 0, mathError{"floor requires 1 argument"}
		}
		return math.Floor(args[0]), nil
	case "ceil":
		if len(args) != 1 {
			return 0, mathError{"ceil requires 1 argument"}
		}
		return math.Ceil(args[0]), nil
	case "min":
		if len(args) < 2 {
			return 0, mathError{"min requires 2+ arguments"}
		}
		result := args[0]
		for _, a := range args[1:] {
			if a < result {
				result = a
			}
		}
		return result, nil
	case "max":
		if len(args) < 2 {
			return 0, mathError{"max requires 2+ arguments"}
		}
		result := args[0]
		for _, a := range args[1:] {
			if a > result {
				result = a
			}
		}
		return result, nil
	default:
		return 0, mathError{fmt.Sprintf("unknown function %q", name)}
	}
}

func isIdentStart(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_'
}

func isIdentChar(b byte) bool {
	return isIdentStart(b) || (b >= '0' && b <= '9')
}

// handleMathEval is the tool handler for math_eval.
func (a *App) handleMathEval(ctx context.Context, tc proxy.ToolCall) string {
	var args struct {
		Expression string `json:"expression"`
	}
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		return fmt.Sprintf("ERROR: could not parse arguments: %v", err)
	}
	if args.Expression == "" {
		return "ERROR: expression is required"
	}
	result, err := EvalMath(args.Expression)
	if err != nil {
		return fmt.Sprintf("ERROR: %v", err)
	}
	// Format: use integer formatting when the result is a whole number.
	if result == math.Trunc(result) && !math.IsInf(result, 0) && math.Abs(result) < 1e15 {
		return fmt.Sprintf("%d", int64(result))
	}
	return fmt.Sprintf("%g", result)
}
