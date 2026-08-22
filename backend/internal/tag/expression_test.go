package tag

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestExpressionEvaluatorHonorsPrecedenceAndTypes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		expression string
		want       any
	}{
		{name: "multiplication before addition", expression: "1 + 2 * 3", want: float64(7)},
		{name: "parentheses", expression: "(1 + 2) * 3", want: float64(9)},
		{name: "left associative subtraction", expression: "10 - 3 - 2", want: float64(5)},
		{name: "unary operators", expression: "-2 * 3 + +4", want: float64(-2)},
		{name: "scientific notation", expression: "1.5e2 / 3", want: float64(50)},
		{name: "modulo", expression: "10 % 4", want: float64(2)},
		{name: "comparison and boolean", expression: "1 + 2 == 3 && !false", want: true},
		{name: "comparison precedence", expression: "2 * 3 >= 6 == true", want: true},
		{name: "boolean precedence", expression: "false || true && false", want: false},
		{name: "numeric inequality", expression: "1 != 2", want: true},
		{name: "boolean equality", expression: "true == !false", want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expression, err := ParseExpression(test.expression)
			if err != nil {
				t.Fatalf("ParseExpression() error = %v", err)
			}
			got, err := expression.Evaluate(context.Background(), nil)
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if got != test.want {
				t.Errorf("Evaluate() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestExpressionExtractsStableDependenciesAndCachesResolvedValues(t *testing.T) {
	t.Parallel()
	firstID := uuid.MustParse("75f159ac-abf2-4a95-8b56-1ce354df14ce")
	secondID := uuid.MustParse("17962178-2887-4a78-8dd5-a22d6283ab87")
	raw := fmt.Sprintf("${%s} * 2 + ${%s} + ${%s}", firstID, firstID, secondID)
	expression, err := ParseExpression(raw)
	if err != nil {
		t.Fatalf("ParseExpression() error = %v", err)
	}
	dependencies := expression.Dependencies()
	if len(dependencies) != 2 || dependencies[0] != firstID || dependencies[1] != secondID {
		t.Fatalf("Dependencies() = %v", dependencies)
	}
	dependencies[0] = uuid.Nil
	if expression.Dependencies()[0] != firstID {
		t.Fatal("Dependencies() returned mutable internal storage")
	}
	calls := map[uuid.UUID]int{}
	value, err := expression.Evaluate(context.Background(), func(_ context.Context, id uuid.UUID) (any, error) {
		calls[id]++
		switch id {
		case firstID:
			return uint16(10), nil
		case secondID:
			return float32(1.5), nil
		default:
			return nil, errors.New("unexpected reference")
		}
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if value != float64(31.5) {
		t.Errorf("Evaluate() = %v, want 31.5", value)
	}
	if calls[firstID] != 1 || calls[secondID] != 1 {
		t.Errorf("resolver calls = %v, want one per dependency", calls)
	}
}

func TestExpressionBooleanOperatorsShortCircuitReferences(t *testing.T) {
	t.Parallel()
	referenceID := uuid.MustParse("dcf7ad54-b957-49e1-a92b-e3ee79186938")
	tests := []string{
		fmt.Sprintf("true || ${%s}", referenceID),
		fmt.Sprintf("false && ${%s}", referenceID),
	}
	for _, raw := range tests {
		expression, err := ParseExpression(raw)
		if err != nil {
			t.Fatalf("ParseExpression(%q) error = %v", raw, err)
		}
		value, err := expression.Evaluate(context.Background(), func(context.Context, uuid.UUID) (any, error) {
			t.Fatal("resolver called for short-circuited branch")
			return nil, nil
		})
		if err != nil {
			t.Fatalf("Evaluate(%q) error = %v", raw, err)
		}
		if _, ok := value.(bool); !ok {
			t.Errorf("Evaluate(%q) = %#v, want bool", raw, value)
		}
	}
}

func TestValidateExpressionReturnsDependenciesWithoutEvaluation(t *testing.T) {
	t.Parallel()
	referenceID := uuid.MustParse("fbb419dd-50bc-4f3d-8d29-83fd6218fe27")
	dependencies, err := ValidateExpression(fmt.Sprintf("${%s} / 0", referenceID))
	if err != nil {
		t.Fatalf("ValidateExpression() error = %v", err)
	}
	if len(dependencies) != 1 || dependencies[0] != referenceID {
		t.Errorf("ValidateExpression() = %v", dependencies)
	}
}

func TestParseExpressionRejectsInvalidSyntax(t *testing.T) {
	t.Parallel()
	tests := []string{
		"",
		"   ",
		"1 +",
		"(1 + 2",
		"1 2",
		"unknown",
		"TRUE",
		"1 & 2",
		"1 = 1",
		"$bad",
		"${bad}",
		"${00000000-0000-0000-0000-000000000000}",
		"${75f159ac-abf2-4a95-8b56-1ce354df14ce",
		"1e+",
	}
	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			if _, err := ParseExpression(raw); !errors.Is(err, ErrInvalidExpression) {
				t.Fatalf("ParseExpression(%q) error = %v, want invalid expression", raw, err)
			}
		})
	}
}

func TestParseExpressionEnforcesComplexityLimits(t *testing.T) {
	t.Parallel()
	dependencies := make([]string, 0, MaxExpressionDependencies+1)
	for index := 0; index <= MaxExpressionDependencies; index++ {
		dependencies = append(dependencies, "${"+uuid.NewString()+"}")
	}
	tests := []struct {
		name string
		raw  string
	}{
		{name: "length", raw: strings.Repeat("1", MaxExpressionLength+1)},
		{name: "tokens", raw: strings.Repeat("1+", MaxExpressionTokens/2) + "1"},
		{name: "parenthesis depth", raw: strings.Repeat("(", MaxExpressionDepth+1) + "1" + strings.Repeat(")", MaxExpressionDepth+1)},
		{name: "unary depth", raw: strings.Repeat("!", MaxExpressionDepth+1) + "true"},
		{name: "dependencies", raw: strings.Join(dependencies, "+")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseExpression(test.raw); !errors.Is(err, ErrExpressionTooComplex) {
				t.Fatalf("ParseExpression() error = %v, want too complex", err)
			}
		})
	}
}

func TestExpressionEvaluationRejectsTypeAndNumericErrors(t *testing.T) {
	t.Parallel()
	tests := []string{
		"true + 1",
		"1 && true",
		"true < false",
		"true == 1",
		"1 / 0",
		"1 % 0",
		"1e308 * 1e308",
		"-true",
	}
	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			expression, err := ParseExpression(raw)
			if err != nil {
				t.Fatalf("ParseExpression() error = %v", err)
			}
			if _, err := expression.Evaluate(context.Background(), nil); !errors.Is(err, ErrExpressionEvaluation) {
				t.Fatalf("Evaluate() error = %v, want evaluation error", err)
			}
		})
	}
}

func TestExpressionEvaluationMapsReferenceFailures(t *testing.T) {
	t.Parallel()
	referenceID := uuid.MustParse("8f638926-66e7-43cf-a8ee-f1dbe4b1f4f3")
	expression, err := ParseExpression("${" + referenceID.String() + "}")
	if err != nil {
		t.Fatalf("ParseExpression() error = %v", err)
	}
	resolverFailure := errors.New("value unavailable")
	tests := []struct {
		name     string
		resolver ReferenceResolver
		cause    error
	}{
		{name: "nil resolver", cause: ErrExpressionReference},
		{name: "resolver error", resolver: func(context.Context, uuid.UUID) (any, error) { return nil, resolverFailure }, cause: resolverFailure},
		{name: "unsupported value", resolver: func(context.Context, uuid.UUID) (any, error) { return "12", nil }, cause: ErrExpressionEvaluation},
		{name: "not a number", resolver: func(context.Context, uuid.UUID) (any, error) { return math.NaN(), nil }, cause: ErrExpressionEvaluation},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := expression.Evaluate(context.Background(), test.resolver)
			if !errors.Is(err, ErrExpressionReference) || !errors.Is(err, test.cause) {
				t.Fatalf("Evaluate() error = %v, want reference and %v", err, test.cause)
			}
		})
	}
}

func TestExpressionEvaluationHonorsCanceledContext(t *testing.T) {
	t.Parallel()
	expression, err := ParseExpression("1 + 2")
	if err != nil {
		t.Fatalf("ParseExpression() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := expression.Evaluate(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Evaluate() error = %v, want context canceled", err)
	}
}

func TestNilExpressionIsSafe(t *testing.T) {
	t.Parallel()
	var expression *Expression
	if expression.Dependencies() != nil {
		t.Error("nil Dependencies() returned non-nil slice")
	}
	if _, err := expression.Evaluate(context.Background(), nil); !errors.Is(err, ErrExpressionEvaluation) {
		t.Fatalf("nil Evaluate() error = %v", err)
	}
}
