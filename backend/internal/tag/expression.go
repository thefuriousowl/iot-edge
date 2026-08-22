package tag

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/uuid"
)

const (
	MaxExpressionLength       = 2048
	MaxExpressionTokens       = 256
	MaxExpressionDepth        = 32
	MaxExpressionDependencies = 32
)

var (
	ErrInvalidExpression    = errors.New("invalid expression")
	ErrExpressionTooComplex = errors.New("expression is too complex")
	ErrExpressionEvaluation = errors.New("expression evaluation failed")
	ErrExpressionReference  = errors.New("expression reference failed")
)

type ReferenceResolver func(context.Context, uuid.UUID) (any, error)

type Expression struct {
	root         expressionNode
	dependencies []uuid.UUID
}

func ParseExpression(raw string) (*Expression, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: expression is required", ErrInvalidExpression)
	}
	if len(raw) > MaxExpressionLength {
		return nil, fmt.Errorf("%w: expression exceeds %d bytes", ErrExpressionTooComplex, MaxExpressionLength)
	}
	tokens, err := lexExpression(raw)
	if err != nil {
		return nil, err
	}
	parser := expressionParser{tokens: tokens, dependencySet: map[uuid.UUID]struct{}{}}
	root, err := parser.parseOr()
	if err != nil {
		return nil, err
	}
	if token := parser.current(); token.kind != tokenEOF {
		return nil, expressionSyntaxError(token.position, "unexpected token %q", token.text)
	}
	return &Expression{root: root, dependencies: parser.dependencies}, nil
}

func ValidateExpression(raw string) ([]uuid.UUID, error) {
	expression, err := ParseExpression(raw)
	if err != nil {
		return nil, err
	}
	return expression.Dependencies(), nil
}

func (e *Expression) Dependencies() []uuid.UUID {
	if e == nil {
		return nil
	}
	return append([]uuid.UUID(nil), e.dependencies...)
}

func (e *Expression) Evaluate(ctx context.Context, resolver ReferenceResolver) (any, error) {
	if e == nil || e.root == nil {
		return nil, fmt.Errorf("%w: expression is not parsed", ErrExpressionEvaluation)
	}
	evaluator := expressionEvaluator{ctx: ctx, resolver: resolver, cache: map[uuid.UUID]any{}}
	return evaluator.evaluate(e.root)
}

type tokenKind uint8

const (
	tokenEOF tokenKind = iota
	tokenNumber
	tokenTrue
	tokenFalse
	tokenReference
	tokenLeftParenthesis
	tokenRightParenthesis
	tokenOperator
)

type expressionToken struct {
	kind      tokenKind
	text      string
	position  int
	number    float64
	reference uuid.UUID
}

type expressionLexer struct {
	raw    string
	index  int
	tokens []expressionToken
}

func lexExpression(raw string) ([]expressionToken, error) {
	lexer := expressionLexer{raw: raw, tokens: make([]expressionToken, 0)}
	for lexer.index < len(raw) {
		character := raw[lexer.index]
		if unicode.IsSpace(rune(character)) {
			lexer.index++
			continue
		}
		position := lexer.index
		switch {
		case isASCIIDigit(character) || character == '.' && lexer.index+1 < len(raw) && isASCIIDigit(raw[lexer.index+1]):
			token, err := lexer.scanNumber()
			if err != nil {
				return nil, err
			}
			if err := lexer.append(token); err != nil {
				return nil, err
			}
		case isASCIIAlpha(character):
			token, err := lexer.scanKeyword()
			if err != nil {
				return nil, err
			}
			if err := lexer.append(token); err != nil {
				return nil, err
			}
		case character == '$':
			token, err := lexer.scanReference()
			if err != nil {
				return nil, err
			}
			if err := lexer.append(token); err != nil {
				return nil, err
			}
		case character == '(':
			lexer.index++
			if err := lexer.append(expressionToken{kind: tokenLeftParenthesis, text: "(", position: position}); err != nil {
				return nil, err
			}
		case character == ')':
			lexer.index++
			if err := lexer.append(expressionToken{kind: tokenRightParenthesis, text: ")", position: position}); err != nil {
				return nil, err
			}
		default:
			token, err := lexer.scanOperator()
			if err != nil {
				return nil, err
			}
			if err := lexer.append(token); err != nil {
				return nil, err
			}
		}
	}
	lexer.tokens = append(lexer.tokens, expressionToken{kind: tokenEOF, position: len(raw)})
	return lexer.tokens, nil
}

func (lexer *expressionLexer) append(token expressionToken) error {
	if len(lexer.tokens) >= MaxExpressionTokens {
		return fmt.Errorf("%w: expression exceeds %d tokens", ErrExpressionTooComplex, MaxExpressionTokens)
	}
	lexer.tokens = append(lexer.tokens, token)
	return nil
}

func (lexer *expressionLexer) scanNumber() (expressionToken, error) {
	start := lexer.index
	for lexer.index < len(lexer.raw) && isASCIIDigit(lexer.raw[lexer.index]) {
		lexer.index++
	}
	if lexer.index < len(lexer.raw) && lexer.raw[lexer.index] == '.' {
		lexer.index++
		for lexer.index < len(lexer.raw) && isASCIIDigit(lexer.raw[lexer.index]) {
			lexer.index++
		}
	}
	if lexer.index < len(lexer.raw) && (lexer.raw[lexer.index] == 'e' || lexer.raw[lexer.index] == 'E') {
		lexer.index++
		if lexer.index < len(lexer.raw) && (lexer.raw[lexer.index] == '+' || lexer.raw[lexer.index] == '-') {
			lexer.index++
		}
		exponentStart := lexer.index
		for lexer.index < len(lexer.raw) && isASCIIDigit(lexer.raw[lexer.index]) {
			lexer.index++
		}
		if lexer.index == exponentStart {
			return expressionToken{}, expressionSyntaxError(start, "invalid number")
		}
	}
	text := lexer.raw[start:lexer.index]
	value, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return expressionToken{}, expressionSyntaxError(start, "invalid number %q", text)
	}
	return expressionToken{kind: tokenNumber, text: text, number: value, position: start}, nil
}

func (lexer *expressionLexer) scanKeyword() (expressionToken, error) {
	start := lexer.index
	for lexer.index < len(lexer.raw) && isASCIIAlpha(lexer.raw[lexer.index]) {
		lexer.index++
	}
	text := lexer.raw[start:lexer.index]
	switch text {
	case "true":
		return expressionToken{kind: tokenTrue, text: text, position: start}, nil
	case "false":
		return expressionToken{kind: tokenFalse, text: text, position: start}, nil
	default:
		return expressionToken{}, expressionSyntaxError(start, "unknown identifier %q", text)
	}
}

func (lexer *expressionLexer) scanReference() (expressionToken, error) {
	start := lexer.index
	if lexer.index+1 >= len(lexer.raw) || lexer.raw[lexer.index+1] != '{' {
		return expressionToken{}, expressionSyntaxError(start, "tag reference must use ${uuid}")
	}
	closingOffset := strings.IndexByte(lexer.raw[lexer.index+2:], '}')
	if closingOffset < 0 {
		return expressionToken{}, expressionSyntaxError(start, "unterminated tag reference")
	}
	closing := lexer.index + 2 + closingOffset
	text := lexer.raw[lexer.index+2 : closing]
	reference, err := uuid.Parse(text)
	if err != nil || reference == uuid.Nil {
		return expressionToken{}, expressionSyntaxError(start, "invalid tag reference %q", text)
	}
	lexer.index = closing + 1
	return expressionToken{kind: tokenReference, text: lexer.raw[start:lexer.index], reference: reference, position: start}, nil
}

func (lexer *expressionLexer) scanOperator() (expressionToken, error) {
	start := lexer.index
	remaining := lexer.raw[lexer.index:]
	for _, operator := range []string{"&&", "||", "==", "!=", "<=", ">="} {
		if strings.HasPrefix(remaining, operator) {
			lexer.index += len(operator)
			return expressionToken{kind: tokenOperator, text: operator, position: start}, nil
		}
	}
	if strings.ContainsRune("+-*/%!<>", rune(lexer.raw[lexer.index])) {
		lexer.index++
		return expressionToken{kind: tokenOperator, text: lexer.raw[start:lexer.index], position: start}, nil
	}
	return expressionToken{}, expressionSyntaxError(start, "unexpected character %q", lexer.raw[lexer.index])
}

type expressionParser struct {
	tokens        []expressionToken
	index         int
	depth         int
	dependencies  []uuid.UUID
	dependencySet map[uuid.UUID]struct{}
}

func (parser *expressionParser) parseOr() (expressionNode, error) {
	return parser.parseBinary(parser.parseAnd, "||")
}

func (parser *expressionParser) parseAnd() (expressionNode, error) {
	return parser.parseBinary(parser.parseEquality, "&&")
}

func (parser *expressionParser) parseEquality() (expressionNode, error) {
	return parser.parseBinary(parser.parseComparison, "==", "!=")
}

func (parser *expressionParser) parseComparison() (expressionNode, error) {
	return parser.parseBinary(parser.parseTerm, "<", "<=", ">", ">=")
}

func (parser *expressionParser) parseTerm() (expressionNode, error) {
	return parser.parseBinary(parser.parseFactor, "+", "-")
}

func (parser *expressionParser) parseFactor() (expressionNode, error) {
	return parser.parseBinary(parser.parseUnary, "*", "/", "%")
}

func (parser *expressionParser) parseBinary(next func() (expressionNode, error), operators ...string) (expressionNode, error) {
	left, err := next()
	if err != nil {
		return nil, err
	}
	for parser.matchesOperator(operators...) {
		operator := parser.previous().text
		right, err := next()
		if err != nil {
			return nil, err
		}
		left = binaryExpressionNode{operator: operator, left: left, right: right}
	}
	return left, nil
}

func (parser *expressionParser) parseUnary() (expressionNode, error) {
	if parser.matchesOperator("!", "+", "-") {
		operator := parser.previous()
		if err := parser.enterDepth(operator.position); err != nil {
			return nil, err
		}
		operand, err := parser.parseUnary()
		parser.depth--
		if err != nil {
			return nil, err
		}
		return unaryExpressionNode{operator: operator.text, operand: operand}, nil
	}
	return parser.parsePrimary()
}

func (parser *expressionParser) parsePrimary() (expressionNode, error) {
	token := parser.current()
	switch token.kind {
	case tokenNumber:
		parser.index++
		return literalExpressionNode{value: token.number}, nil
	case tokenTrue:
		parser.index++
		return literalExpressionNode{value: true}, nil
	case tokenFalse:
		parser.index++
		return literalExpressionNode{value: false}, nil
	case tokenReference:
		parser.index++
		if _, exists := parser.dependencySet[token.reference]; !exists {
			if len(parser.dependencies) >= MaxExpressionDependencies {
				return nil, fmt.Errorf("%w: expression exceeds %d dependencies", ErrExpressionTooComplex, MaxExpressionDependencies)
			}
			parser.dependencySet[token.reference] = struct{}{}
			parser.dependencies = append(parser.dependencies, token.reference)
		}
		return referenceExpressionNode{id: token.reference}, nil
	case tokenLeftParenthesis:
		parser.index++
		if err := parser.enterDepth(token.position); err != nil {
			return nil, err
		}
		node, err := parser.parseOr()
		parser.depth--
		if err != nil {
			return nil, err
		}
		if parser.current().kind != tokenRightParenthesis {
			return nil, expressionSyntaxError(parser.current().position, "expected closing parenthesis")
		}
		parser.index++
		return node, nil
	default:
		if token.kind == tokenEOF {
			return nil, expressionSyntaxError(token.position, "unexpected end of expression")
		}
		return nil, expressionSyntaxError(token.position, "expected value, found %q", token.text)
	}
}

func (parser *expressionParser) enterDepth(position int) error {
	parser.depth++
	if parser.depth > MaxExpressionDepth {
		parser.depth--
		return fmt.Errorf("%w: nesting exceeds %d at position %d", ErrExpressionTooComplex, MaxExpressionDepth, position+1)
	}
	return nil
}

func (parser *expressionParser) matchesOperator(operators ...string) bool {
	token := parser.current()
	if token.kind != tokenOperator {
		return false
	}
	for _, operator := range operators {
		if token.text == operator {
			parser.index++
			return true
		}
	}
	return false
}

func (parser *expressionParser) current() expressionToken  { return parser.tokens[parser.index] }
func (parser *expressionParser) previous() expressionToken { return parser.tokens[parser.index-1] }

type expressionNode interface {
	evaluate(*expressionEvaluator) (any, error)
}

type literalExpressionNode struct{ value any }
type referenceExpressionNode struct{ id uuid.UUID }
type unaryExpressionNode struct {
	operator string
	operand  expressionNode
}
type binaryExpressionNode struct {
	operator string
	left     expressionNode
	right    expressionNode
}

type expressionEvaluator struct {
	ctx      context.Context
	resolver ReferenceResolver
	cache    map[uuid.UUID]any
}

func (evaluator *expressionEvaluator) evaluate(node expressionNode) (any, error) {
	if err := evaluator.ctx.Err(); err != nil {
		return nil, err
	}
	return node.evaluate(evaluator)
}

func (node literalExpressionNode) evaluate(*expressionEvaluator) (any, error) { return node.value, nil }

func (node referenceExpressionNode) evaluate(evaluator *expressionEvaluator) (any, error) {
	if value, exists := evaluator.cache[node.id]; exists {
		return value, nil
	}
	if evaluator.resolver == nil {
		return nil, fmt.Errorf("%w: no resolver for %s", ErrExpressionReference, node.id)
	}
	value, err := evaluator.resolver(evaluator.ctx, node.id)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve %s: %w", ErrExpressionReference, node.id, err)
	}
	normalized, err := normalizeExpressionValue(value)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve %s: %w", ErrExpressionReference, node.id, err)
	}
	evaluator.cache[node.id] = normalized
	return normalized, nil
}

func (node unaryExpressionNode) evaluate(evaluator *expressionEvaluator) (any, error) {
	value, err := evaluator.evaluate(node.operand)
	if err != nil {
		return nil, err
	}
	switch node.operator {
	case "!":
		boolean, ok := value.(bool)
		if !ok {
			return nil, expressionEvaluationError("operator ! requires bool")
		}
		return !boolean, nil
	case "+":
		number, ok := expressionNumber(value)
		if !ok {
			return nil, expressionEvaluationError("unary + requires number")
		}
		return number, nil
	case "-":
		number, ok := expressionNumber(value)
		if !ok {
			return nil, expressionEvaluationError("unary - requires number")
		}
		return -number, nil
	default:
		return nil, expressionEvaluationError("unsupported unary operator %q", node.operator)
	}
}

func (node binaryExpressionNode) evaluate(evaluator *expressionEvaluator) (any, error) {
	left, err := evaluator.evaluate(node.left)
	if err != nil {
		return nil, err
	}
	if node.operator == "&&" || node.operator == "||" {
		leftBoolean, ok := left.(bool)
		if !ok {
			return nil, expressionEvaluationError("operator %s requires bool operands", node.operator)
		}
		if node.operator == "&&" && !leftBoolean {
			return false, nil
		}
		if node.operator == "||" && leftBoolean {
			return true, nil
		}
		right, err := evaluator.evaluate(node.right)
		if err != nil {
			return nil, err
		}
		rightBoolean, ok := right.(bool)
		if !ok {
			return nil, expressionEvaluationError("operator %s requires bool operands", node.operator)
		}
		if node.operator == "&&" {
			return leftBoolean && rightBoolean, nil
		}
		return leftBoolean || rightBoolean, nil
	}

	right, err := evaluator.evaluate(node.right)
	if err != nil {
		return nil, err
	}
	if node.operator == "==" || node.operator == "!=" {
		equal, err := expressionValuesEqual(left, right)
		if err != nil {
			return nil, err
		}
		if node.operator == "!=" {
			return !equal, nil
		}
		return equal, nil
	}
	leftNumber, leftOK := expressionNumber(left)
	rightNumber, rightOK := expressionNumber(right)
	if !leftOK || !rightOK {
		return nil, expressionEvaluationError("operator %s requires number operands", node.operator)
	}
	switch node.operator {
	case "+":
		return finiteExpressionResult(leftNumber + rightNumber)
	case "-":
		return finiteExpressionResult(leftNumber - rightNumber)
	case "*":
		return finiteExpressionResult(leftNumber * rightNumber)
	case "/":
		if rightNumber == 0 {
			return nil, expressionEvaluationError("division by zero")
		}
		return finiteExpressionResult(leftNumber / rightNumber)
	case "%":
		if rightNumber == 0 {
			return nil, expressionEvaluationError("modulo by zero")
		}
		return finiteExpressionResult(math.Mod(leftNumber, rightNumber))
	case "<":
		return leftNumber < rightNumber, nil
	case "<=":
		return leftNumber <= rightNumber, nil
	case ">":
		return leftNumber > rightNumber, nil
	case ">=":
		return leftNumber >= rightNumber, nil
	default:
		return nil, expressionEvaluationError("unsupported binary operator %q", node.operator)
	}
}

func normalizeExpressionValue(value any) (any, error) {
	if boolean, ok := value.(bool); ok {
		return boolean, nil
	}
	if number, ok := expressionNumber(value); ok {
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, expressionEvaluationError("reference value is not finite")
		}
		return number, nil
	}
	return nil, expressionEvaluationError("unsupported reference value type %T", value)
}

func expressionNumber(value any) (float64, bool) {
	switch number := value.(type) {
	case int:
		return float64(number), true
	case int8:
		return float64(number), true
	case int16:
		return float64(number), true
	case int32:
		return float64(number), true
	case int64:
		return float64(number), true
	case uint:
		return float64(number), true
	case uint8:
		return float64(number), true
	case uint16:
		return float64(number), true
	case uint32:
		return float64(number), true
	case uint64:
		return float64(number), true
	case float32:
		return float64(number), true
	case float64:
		return number, true
	default:
		return 0, false
	}
}

func expressionValuesEqual(left, right any) (bool, error) {
	leftBoolean, leftIsBoolean := left.(bool)
	rightBoolean, rightIsBoolean := right.(bool)
	if leftIsBoolean || rightIsBoolean {
		if !leftIsBoolean || !rightIsBoolean {
			return false, expressionEvaluationError("cannot compare bool and number")
		}
		return leftBoolean == rightBoolean, nil
	}
	leftNumber, leftIsNumber := expressionNumber(left)
	rightNumber, rightIsNumber := expressionNumber(right)
	if !leftIsNumber || !rightIsNumber {
		return false, expressionEvaluationError("operator equality requires matching bool or number operands")
	}
	return leftNumber == rightNumber, nil
}

func finiteExpressionResult(value float64) (any, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil, expressionEvaluationError("numeric result is not finite")
	}
	return value, nil
}

func expressionSyntaxError(position int, format string, arguments ...any) error {
	return fmt.Errorf("%w at position %d: %s", ErrInvalidExpression, position+1, fmt.Sprintf(format, arguments...))
}

func expressionEvaluationError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrExpressionEvaluation, fmt.Sprintf(format, arguments...))
}

func isASCIIDigit(value byte) bool { return value >= '0' && value <= '9' }
func isASCIIAlpha(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}
