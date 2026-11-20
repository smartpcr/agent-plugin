package loader

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
)

// exprKey marks an expression object in JSON: {"$expr": "<source>"}.
const exprKey = "$expr"

// ScopeKind says where an expression is evaluated, and so which names it may use.
type ScopeKind int

const (
	// DisabledScope is an entry's "disabled": every name except "use".
	DisabledScope ScopeKind = iota
	// ConfigScope is a value inside an entry's "config", evaluated at each load of
	// the entry's fiber, once its injected services are active.
	ConfigScope
)

func (k ScopeKind) String() string {
	if k == ConfigScope {
		return "config"
	}
	return "disabled"
}

// Scope is what an evaluation sees.
type Scope struct {
	Kind ScopeKind
	// EntryID is the tree id of the entry the expression belongs to.
	EntryID string
	// Context is the entry's context: for DisabledScope the scope the entry loads in,
	// for ConfigScope the context of the entry's fiber, through which the services
	// the entry injects are reached.
	Context *cordis.Context
}

// Evaluator computes expressions in entries. The loader holds an expression's source
// in the tree and in the file, and calls Eval each time it needs the value; src/go
// ships no evaluator, the host injects one through Options.Evaluator.
type Evaluator interface {
	// Check parses and type-checks source for kind without evaluating it.
	Check(source string, kind ScopeKind) error
	// Eval computes the value of source. It has no side effects.
	Eval(source string, scope Scope) (any, error)
}

// ErrNoEvaluator rejects an expression when the loader has no Evaluator.
var ErrNoEvaluator = errors.New("expressions need an evaluator, and the loader has none")

// templateSource returns the source of a string that is exactly "${{ <source> }}".
func templateSource(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "${{") || !strings.HasSuffix(trimmed, "}}") || len(trimmed) < len("${{}}") {
		return "", false
	}
	inner := trimmed[len("${{") : len(trimmed)-len("}}")]
	if strings.Contains(inner, "${{") || strings.Contains(inner, "}}") {
		return "", false
	}
	inner = strings.TrimSpace(inner)
	return inner, inner != ""
}

// exprOf returns the source of a decoded JSON value that is an expression.
func exprOf(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return templateSource(v)
	case map[string]any:
		if len(v) != 1 {
			return "", false
		}
		source, ok := v[exprKey].(string)
		return source, ok
	}
	return "", false
}

// mayHoldExpr is a cheap test that lets configs without expressions skip decoding.
func mayHoldExpr(raw []byte) bool {
	return bytes.Contains(raw, []byte(`"`+exprKey+`"`)) || bytes.Contains(raw, []byte("${{"))
}

func decodeJSON(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	err := decoder.Decode(&value)
	return value, err
}

type foundExpr struct {
	source string
	path   string // relative to the entry, such as "config.port"
}

// exprsIn lists the expressions of a raw config in document order. Object members are
// visited in key order; the order only matters for reporting.
func exprsIn(raw json.RawMessage, path string) []foundExpr {
	if len(raw) == 0 || !mayHoldExpr(raw) {
		return nil
	}
	value, err := decodeJSON(raw)
	if err != nil {
		return nil
	}
	var found []foundExpr
	walkExprs(value, path, func(source, path string) (any, error) {
		found = append(found, foundExpr{source: source, path: path})
		return nil, nil
	})
	return found
}

// walkExprs calls eval for every expression in value, depth first, and returns value
// with each expression replaced by what eval returned.
func walkExprs(value any, path string, eval func(source, path string) (any, error)) (any, error) {
	if source, ok := exprOf(value); ok {
		return eval(source, path)
	}
	switch v := value.(type) {
	case map[string]any:
		keys := slices.Sorted(func(yield func(string) bool) {
			for key := range v {
				if !yield(key) {
					return
				}
			}
		})
		for _, key := range keys {
			replaced, err := walkExprs(v[key], joinPath(path, key), eval)
			if err != nil {
				return nil, err
			}
			v[key] = replaced
		}
	case []any:
		for i, item := range v {
			replaced, err := walkExprs(item, fmt.Sprintf("%s[%d]", path, i), eval)
			if err != nil {
				return nil, err
			}
			v[i] = replaced
		}
	}
	return value, nil
}

// substitute evaluates every expression of a raw config and returns the config with
// the values in their place. The path of a failing expression is returned with the
// error, relative to the config.
func substitute(raw json.RawMessage, eval func(source string) (any, error)) (json.RawMessage, string, error) {
	value, err := decodeJSON(raw)
	if err != nil {
		return nil, "", err
	}
	var failed string
	value, err = walkExprs(value, "", func(source, path string) (any, error) {
		result, err := eval(source)
		if err != nil {
			failed = path
		}
		return result, err
	})
	if err != nil {
		return nil, failed, err
	}
	encoded, err := json.Marshal(value)
	return encoded, "", err
}

// configHook is the loader's DH-D-CORDIS-5 config hook. For the fiber of an entry it
// replaces each expression of the raw config with its value before the component's
// schema runs; the raw config, the tree and the file keep the source. The core runs
// it at every load, after the fiber's injections are active and outside the tree
// lock, so a replaced provider reloads the fiber and the expression is evaluated
// again. An evaluation error rejects that fiber's config only.
func (s *Service) configHook(fiber *cordis.Fiber, candidate any) (any, error) {
	raw, ok := candidate.(json.RawMessage)
	if !ok || !mayHoldExpr(raw) {
		return candidate, nil
	}
	// a fiber is registered right after its Use returns; wait for that, so the first
	// load of a new entry is not mistaken for a foreign fiber
	s.useGate.RLock()
	s.mu.Lock()
	entry := s.byFiber[fiber]
	var options EntryOptions
	if entry != nil {
		options = entry.options
	}
	s.mu.Unlock()
	s.useGate.RUnlock()
	if entry == nil {
		return candidate, nil
	}
	resolved, path, err := substitute(raw, func(source string) (any, error) {
		if s.evaluator == nil {
			return nil, ErrNoEvaluator
		}
		return s.evaluator.Eval(source, Scope{Kind: ConfigScope, EntryID: entry.id, Context: fiber.Ctx()})
	})
	if err != nil {
		// name the file, line and column of the failing value, as a parse error does
		pos, docPath := options.position(joinPath("config", path))
		located := Issue{Pos: pos, Path: docPath, Message: "cannot evaluate expression: " + err.Error()}
		return nil, &cordis.ConfigError{Issues: []cordis.ConfigIssue{{
			Message: fmt.Sprintf("entry %q: %s", entry.id, located.Error()),
			Path:    path,
		}}}
	}
	return resolved, nil
}

// evalDisabled evaluates the entry's disabled expression in scope.
func (s *Service) evalDisabled(options EntryOptions, id string, scope *cordis.Context) (bool, error) {
	if options.DisabledExpr == "" {
		return options.Disabled, nil
	}
	fail := func(err error) (bool, error) {
		pos, path := options.position("disabled")
		return false, &EntryError{ID: id, Issues: []Issue{{Pos: pos, Path: path, Message: "cannot evaluate disabled: " + err.Error()}}}
	}
	if s.evaluator == nil {
		return fail(ErrNoEvaluator)
	}
	value, err := s.evaluator.Eval(options.DisabledExpr, Scope{Kind: DisabledScope, EntryID: id, Context: scope})
	if err != nil {
		return fail(err)
	}
	disabled, ok := value.(bool)
	if !ok {
		return fail(fmt.Errorf("want a boolean, got %T", value))
	}
	return disabled, nil
}

// checkExprs reports, for each expression of the entry, a missing evaluator or what
// the evaluator's Check rejects.
func (s *Service) checkExprs(options EntryOptions) []Issue {
	var found []foundExpr
	kinds := map[int]ScopeKind{}
	if options.DisabledExpr != "" {
		found = append(found, foundExpr{source: options.DisabledExpr, path: "disabled"})
	}
	for _, expr := range exprsIn(options.Config, "config") {
		kinds[len(found)] = ConfigScope
		found = append(found, expr)
	}
	var issues []Issue
	for i, expr := range found {
		var err error
		switch {
		case strings.TrimSpace(expr.source) == "":
			err = errors.New("expression must not be empty")
		case s.evaluator == nil:
			err = ErrNoEvaluator
		default:
			err = s.evaluator.Check(expr.source, kinds[i])
		}
		if err != nil {
			pos, path := options.position(expr.path)
			issues = append(issues, Issue{Pos: pos, Path: path, Message: fmt.Sprintf("expression %q: %v", expr.source, err)})
		}
	}
	return issues
}
