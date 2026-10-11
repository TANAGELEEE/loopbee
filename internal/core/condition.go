package core

import (
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
)

// The completion language permits boolean logic and numeric comparisons, never calls or evaluation of code.
func condition(source string, metrics map[string]float64, verified bool) (bool, error) {
	if strings.TrimSpace(source) == "" {
		return verified, nil
	}
	if len(source) > 1000 {
		return false, errors.New("completion condition too long")
	}
	expr, err := parser.ParseExpr(source)
	if err != nil {
		return false, errors.New("invalid completion condition")
	}
	var eval func(ast.Expr) (any, error)
	eval = func(expr ast.Expr) (any, error) {
		switch e := expr.(type) {
		case *ast.ParenExpr:
			return eval(e.X)
		case *ast.Ident:
			if e.Name == "true" {
				return true, nil
			}
			if e.Name == "false" {
				return false, nil
			}
		case *ast.BasicLit:
			if e.Kind == token.INT || e.Kind == token.FLOAT {
				v, err := strconv.ParseFloat(e.Value, 64)
				return v, err
			}
		case *ast.SelectorExpr:
			if base, ok := e.X.(*ast.Ident); ok {
				if base.Name == "verify" && e.Sel.Name == "passed" {
					return verified, nil
				}
				if base.Name == "metrics" {
					if value, ok := metrics[e.Sel.Name]; ok {
						return value, nil
					}
					return nil, nil
				}
			}
		case *ast.UnaryExpr:
			v, err := eval(e.X)
			if err != nil {
				return nil, err
			}
			if e.Op == token.NOT {
				if v == nil {
					return nil, nil
				}
				b, ok := v.(bool)
				if ok {
					return !b, nil
				}
			}
			if e.Op == token.SUB {
				if n, ok := v.(float64); ok {
					return -n, nil
				}
			}
		case *ast.BinaryExpr:
			left, err := eval(e.X)
			if err != nil {
				return nil, err
			}
			right, err := eval(e.Y)
			if err != nil {
				return nil, err
			}
			switch e.Op {
			case token.LAND, token.LOR:
				l, lok := left.(bool)
				r, rok := right.(bool)
				if left == nil || right == nil {
					return false, nil
				}
				if !lok || !rok {
					return nil, errors.New("boolean operands required")
				}
				if e.Op == token.LAND {
					return l && r, nil
				}
				return l || r, nil
			case token.GEQ, token.GTR, token.LEQ, token.LSS, token.EQL, token.NEQ:
				if left == nil || right == nil {
					return false, nil
				}
				l, lok := left.(float64)
				r, rok := right.(float64)
				if !lok || !rok {
					return nil, errors.New("numeric comparison required")
				}
				switch e.Op {
				case token.GEQ:
					return l >= r, nil
				case token.GTR:
					return l > r, nil
				case token.LEQ:
					return l <= r, nil
				case token.LSS:
					return l < r, nil
				case token.EQL:
					return l == r, nil
				case token.NEQ:
					return l != r, nil
				}
			}
		}
		return nil, errors.New("only metrics.name, verify.passed, numbers, booleans, comparisons and &&/||/! are allowed")
	}
	value, err := eval(expr)
	if err != nil {
		return false, err
	}
	b, ok := value.(bool)
	if !ok && value != nil {
		return false, errors.New("completion condition must be boolean")
	}
	return b, nil
}
func metricValue(data []byte, path string) (float64, error) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return 0, errors.New("metric output must be JSON")
	}
	path = strings.TrimPrefix(path, "$.")
	for _, part := range strings.Split(path, ".") {
		obj, ok := v.(map[string]any)
		if !ok {
			return 0, errors.New("metric path not found")
		}
		v, ok = obj[part]
		if !ok {
			return 0, errors.New("metric path not found")
		}
	}
	number, ok := v.(float64)
	if !ok {
		return 0, errors.New("metric must be numeric")
	}
	return number, nil
}
