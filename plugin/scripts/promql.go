package main

import (
	"fmt"
	"strconv"
	"strings"
)

// The PromQL reader here finds vector selectors and their label matchers.
// It doesn't evaluate anything. It rejects what it can't read (unbalanced
// brackets, unterminated or badly escaped strings, unknown characters) so
// that a broken or cut-off expression is never reported as clean.

type tokKind int

const (
	tIdent tokKind = iota
	tString
	tNumber
	tVar // Grafana variable: $x, ${x}, [[x]]
	tPunct
)

type token struct {
	kind tokKind
	text string // identifier, punctuation, or the unquoted string value
	raw  string // source text
}

type matcher struct {
	Label string
	Op    string // =, !=, =~, !~
	Value string
	// HasVar is set when the value holds a Grafana variable.
	HasVar bool
}

type selector struct {
	Name     string // "" if unknown
	NameVar  bool   // the name is a Grafana variable or a regex
	Matchers []matcher
}

func lexPromQL(s string) ([]token, error) {
	var out []token
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '#':
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case c == '"' || c == '\'' || c == '`':
			j := i + 1
			for j < len(s) && s[j] != c {
				if s[j] == '\\' && c != '`' {
					j++
				}
				if c != '`' && j < len(s) && s[j] == '\n' {
					return nil, fmt.Errorf("unterminated string at %q", truncate(s[i:], 30))
				}
				j++
			}
			if j >= len(s) {
				return nil, fmt.Errorf("unterminated string at %q", truncate(s[i:], 30))
			}
			raw := s[i : j+1]
			v, err := unquotePromQL(raw)
			if err != nil {
				return nil, fmt.Errorf("bad string %s: %v", truncate(raw, 30), err)
			}
			out = append(out, token{tString, v, raw})
			i = j + 1
		case c == '$':
			j := i + 1
			if j < len(s) && s[j] == '{' {
				k := strings.IndexByte(s[j:], '}')
				if k < 0 {
					return nil, fmt.Errorf("unterminated variable at %q", truncate(s[i:], 30))
				}
				j += k + 1
			} else {
				for j < len(s) && (isIdentChar(s[j]) || (s[j] >= '0' && s[j] <= '9')) {
					j++
				}
			}
			if j == i+1 {
				return nil, fmt.Errorf("lone $ at %q", truncate(s[i:], 30))
			}
			out = append(out, token{tVar, s[i:j], s[i:j]})
			i = j
		case c == '[' && i+1 < len(s) && s[i+1] == '[':
			k := strings.Index(s[i:], "]]")
			if k < 0 {
				return nil, fmt.Errorf("unterminated [[variable]] at %q", truncate(s[i:], 30))
			}
			out = append(out, token{tVar, s[i : i+k+2], s[i : i+k+2]})
			i += k + 2
		case isIdentChar(c):
			j := i
			for j < len(s) && (isIdentChar(s[j]) || (s[j] >= '0' && s[j] <= '9') || s[j] == ':' || s[j] == '.') {
				j++
			}
			// Label and metric names can't contain dots unless quoted; a dot
			// here would be an error in Prometheus too.
			if strings.Contains(s[i:j], ".") {
				return nil, fmt.Errorf("unexpected . in %q", s[i:j])
			}
			out = append(out, token{tIdent, s[i:j], s[i:j]})
			i = j
		case c == ':' && i+1 < len(s) && isIdentChar(s[i+1]):
			// metric names may start with a colon
			j := i
			for j < len(s) && (isIdentChar(s[j]) || (s[j] >= '0' && s[j] <= '9') || s[j] == ':') {
				j++
			}
			out = append(out, token{tIdent, s[i:j], s[i:j]})
			i = j
		case (c >= '0' && c <= '9') || (c == '.' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9'):
			j := i
			for j < len(s) {
				d := s[j]
				if (d >= '0' && d <= '9') || (d >= 'a' && d <= 'z') || (d >= 'A' && d <= 'Z') || d == '.' || d == '_' {
					j++
					if (d == 'e' || d == 'E') && j < len(s) && (s[j] == '+' || s[j] == '-') && !strings.HasPrefix(strings.ToLower(s[i:j]), "0x") {
						j++
					}
					continue
				}
				break
			}
			out = append(out, token{tNumber, s[i:j], s[i:j]})
			i = j
		default:
			two := ""
			if i+1 < len(s) {
				two = s[i : i+2]
			}
			switch two {
			case "!=", "=~", "!~", "==", "<=", ">=":
				out = append(out, token{tPunct, two, two})
				i += 2
				continue
			}
			if strings.IndexByte("(){}[],=<>+-*/%^@:", c) >= 0 {
				out = append(out, token{tPunct, string(c), string(c)})
				i++
				continue
			}
			return nil, fmt.Errorf("unexpected character %q", string(rune(c)))
		}
	}
	return out, nil
}

func isIdentChar(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// unquotePromQL follows the PromQL string rules, which are Go's: double and
// single quotes take escapes, backticks are raw.
func unquotePromQL(raw string) (string, error) {
	switch raw[0] {
	case '`':
		return raw[1 : len(raw)-1], nil
	case '"':
		return strconv.Unquote(raw)
	}
	body := raw[1 : len(raw)-1]
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		if body[i] == '\\' && i+1 < len(body) && body[i+1] == '\'' {
			b.WriteByte('\'')
			i++
			continue
		}
		if body[i] == '"' {
			b.WriteString(`\"`)
			continue
		}
		if body[i] == '\\' && i+1 < len(body) {
			b.WriteByte('\\')
			b.WriteByte(body[i+1])
			i++
			continue
		}
		b.WriteByte(body[i])
	}
	return strconv.Unquote(`"` + b.String() + `"`)
}

var groupingKeywords = map[string]bool{
	"by": true, "without": true, "on": true, "ignoring": true, "group_left": true, "group_right": true,
}

var plainKeywords = map[string]bool{
	"and": true, "or": true, "unless": true, "bool": true, "offset": true, "atan2": true,
	"inf": true, "nan": true,
}

var aggregations = map[string]bool{
	"sum": true, "min": true, "max": true, "avg": true, "group": true, "stddev": true, "stdvar": true,
	"count": true, "count_values": true, "bottomk": true, "topk": true, "quantile": true,
	"limitk": true, "limit_ratio": true,
}

// selectors returns every vector selector in a PromQL expression.
func selectors(expr string) ([]selector, error) {
	toks, err := lexPromQL(expr)
	if err != nil {
		return nil, err
	}
	if len(toks) == 0 {
		return nil, fmt.Errorf("the expression is empty")
	}
	// Brackets must balance and nest.
	var stack []string
	pair := map[string]string{")": "(", "}": "{", "]": "["}
	for _, t := range toks {
		if t.kind != tPunct {
			continue
		}
		switch t.text {
		case "(", "{", "[":
			stack = append(stack, t.text)
		case ")", "}", "]":
			if len(stack) == 0 || stack[len(stack)-1] != pair[t.text] {
				return nil, fmt.Errorf("unbalanced %q", t.text)
			}
			stack = stack[:len(stack)-1]
		}
	}
	if len(stack) > 0 {
		return nil, fmt.Errorf("unclosed %q, so the expression may be cut off", stack[len(stack)-1])
	}
	if last := toks[len(toks)-1]; last.kind == tPunct && last.text != ")" && last.text != "}" && last.text != "]" {
		return nil, fmt.Errorf("the expression ends with %q, so it may be cut off", last.text)
	}

	var out []selector
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		next := func(k int) *token {
			if i+k < len(toks) {
				return &toks[i+k]
			}
			return nil
		}
		isPunct := func(tk *token, p string) bool { return tk != nil && tk.kind == tPunct && tk.text == p }
		switch {
		case t.kind == tIdent:
			low := strings.ToLower(t.text)
			if groupingKeywords[low] {
				if isPunct(next(1), "(") {
					i = skipGroup(toks, i+1)
				}
				continue
			}
			if plainKeywords[low] {
				continue
			}
			if isPunct(next(1), "(") {
				continue // function call or aggregation
			}
			if aggregations[low] && next(1) != nil && next(1).kind == tIdent && groupingKeywords[strings.ToLower(next(1).text)] {
				continue
			}
			// After @, start and end are functions; already handled by "(".
			sel := selector{Name: t.text}
			if isPunct(next(1), "{") {
				end, err := readMatchers(toks, i+2, &sel)
				if err != nil {
					return nil, err
				}
				i = end
			}
			out = append(out, sel)
		case t.kind == tVar && isPunct(next(1), "{"):
			sel := selector{NameVar: true}
			end, err := readMatchers(toks, i+2, &sel)
			if err != nil {
				return nil, err
			}
			i = end
			out = append(out, sel)
		case t.kind == tPunct && t.text == "{":
			sel := selector{}
			end, err := readMatchers(toks, i+1, &sel)
			if err != nil {
				return nil, err
			}
			i = end
			for _, m := range sel.Matchers {
				if m.Label == "__name__" {
					if m.Op == "=" && !m.HasVar {
						sel.Name = m.Value
					} else {
						sel.NameVar = true
					}
				}
			}
			if sel.Name == "" {
				sel.NameVar = true
			}
			out = append(out, sel)
		}
	}
	return out, nil
}

// skipGroup returns the index of the ")" that closes the "(" at i.
func skipGroup(toks []token, i int) int {
	depth := 0
	for ; i < len(toks); i++ {
		if toks[i].kind != tPunct {
			continue
		}
		switch toks[i].text {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(toks) - 1
}

// readMatchers reads matchers from i (just after "{") and returns the index
// of the closing "}".
func readMatchers(toks []token, i int, sel *selector) (int, error) {
	for i < len(toks) {
		t := toks[i]
		if t.kind == tPunct && t.text == "}" {
			return i, nil
		}
		var label string
		switch t.kind {
		case tIdent:
			label = t.text
		case tString:
			label = t.text
			// A quoted name on its own is the metric name (Prometheus 3).
			if i+1 < len(toks) && toks[i+1].kind == tPunct && (toks[i+1].text == "," || toks[i+1].text == "}") {
				if sel.Name != "" {
					return 0, fmt.Errorf("two metric names in one selector")
				}
				sel.Name = t.text
				i++
				if toks[i].text == "," {
					i++
				}
				continue
			}
		default:
			return 0, fmt.Errorf("expected a label name in braces, got %q", t.raw)
		}
		if i+2 >= len(toks) {
			return 0, fmt.Errorf("label matcher for %q is cut off", label)
		}
		op := toks[i+1]
		if op.kind != tPunct || (op.text != "=" && op.text != "!=" && op.text != "=~" && op.text != "!~") {
			return 0, fmt.Errorf("expected =, !=, =~ or !~ after %q, got %q", label, op.raw)
		}
		val := toks[i+2]
		m := matcher{Label: label, Op: op.text}
		switch val.kind {
		case tString:
			m.Value = val.text
			m.HasVar = strings.Contains(val.text, "$") || strings.Contains(val.text, "[[")
		case tVar:
			m.Value, m.HasVar = val.text, true
		default:
			return 0, fmt.Errorf("matcher %s%s needs a quoted value, got %q", label, op.text, val.raw)
		}
		sel.Matchers = append(sel.Matchers, m)
		i += 3
		if i < len(toks) && toks[i].kind == tPunct && toks[i].text == "," {
			i++
			continue
		}
		if i < len(toks) && toks[i].kind == tPunct && toks[i].text == "}" {
			return i, nil
		}
		return 0, fmt.Errorf("expected , or } after matcher %s", label)
	}
	return 0, fmt.Errorf("unclosed {")
}
