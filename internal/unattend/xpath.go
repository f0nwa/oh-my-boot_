package unattend

import (
	"fmt"
	"strings"
	"sync"
)

// The XPath subset the generator uses: absolute and relative paths with / and
// //, name tests with prefixes, *, text(), predicates [@a='v'], [@a] and
// [local-name()='x' or local-name()='y'], and unions with |. Results come in
// document order. Unprefixed names match elements without a namespace, as in
// XPath 1.0.

type xpathStep struct {
	descendant bool   // reached with //
	prefix     string // name test prefix
	local      string // "*" for any element, "text()" for text
	preds      []xpathPred
}

type xpathPred struct {
	attr       string   // [@attr] or [@attr='value']
	value      *string  // nil: attribute only has to exist
	localNames []string // [local-name()='a' or …]
}

type xpathExpr struct {
	paths []xpathPath
}

type xpathPath struct {
	absolute bool
	steps    []xpathStep
}

var xpathCache sync.Map // string -> *xpathExpr

func compileXPath(expr string) *xpathExpr {
	if c, ok := xpathCache.Load(expr); ok {
		return c.(*xpathExpr)
	}
	var out xpathExpr
	for _, part := range splitTop(expr, '|') {
		out.paths = append(out.paths, parsePath(strings.TrimSpace(part)))
	}
	xpathCache.Store(expr, &out)
	return &out
}

// splitTop splits on sep outside brackets and quotes.
func splitTop(s string, sep byte) []string {
	var parts []string
	depth, quote, start := 0, byte(0), 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '[':
			depth++
		case c == ']':
			depth--
		case c == sep && depth == 0:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

func parsePath(s string) xpathPath {
	var p xpathPath
	if strings.HasPrefix(s, "/") {
		p.absolute = true
	}
	i := 0
	for i < len(s) {
		desc := false
		if strings.HasPrefix(s[i:], "//") {
			desc, i = true, i+2
		} else if s[i] == '/' {
			i++
		}
		// One step: up to the next / outside brackets.
		j, depth, quote := i, 0, byte(0)
		for j < len(s) {
			c := s[j]
			if quote != 0 {
				if c == quote {
					quote = 0
				}
			} else if c == '\'' || c == '"' {
				quote = c
			} else if c == '[' {
				depth++
			} else if c == ']' {
				depth--
			} else if c == '/' && depth == 0 {
				break
			}
			j++
		}
		p.steps = append(p.steps, parseStep(s[i:j], desc))
		i = j
	}
	return p
}

func parseStep(s string, desc bool) xpathStep {
	st := xpathStep{descendant: desc}
	name := s
	if k := strings.IndexByte(s, '['); k >= 0 {
		name = s[:k]
		rest := s[k:]
		for strings.HasPrefix(rest, "[") {
			end := matchBracket(rest)
			st.preds = append(st.preds, parsePred(rest[1:end]))
			rest = rest[end+1:]
		}
	}
	if pfx, local, ok := strings.Cut(name, ":"); ok {
		st.prefix, st.local = pfx, local
	} else {
		st.local = name
	}
	return st
}

func matchBracket(s string) int {
	depth, quote := 0, byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '[':
			depth++
		case c == ']':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	panic("unattend: unbalanced XPath predicate: " + s)
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '\'' || s[0] == '"') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	panic("unattend: XPath literal expected: " + s)
}

func parsePred(s string) xpathPred {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "local-name()") {
		var p xpathPred
		for _, alt := range strings.Split(s, " or ") {
			_, v, ok := strings.Cut(alt, "=")
			if !ok {
				panic("unattend: unsupported XPath predicate: " + s)
			}
			p.localNames = append(p.localNames, unquote(v))
		}
		return p
	}
	if strings.HasPrefix(s, "@") {
		name, v, ok := strings.Cut(s[1:], "=")
		p := xpathPred{attr: strings.TrimSpace(name)}
		if ok {
			val := unquote(v)
			p.value = &val
		}
		return p
	}
	panic("unattend: unsupported XPath predicate: " + s)
}

func (st xpathStep) matches(n *Node, ns map[string]string) bool {
	switch st.local {
	case "text()":
		if !n.IsText() {
			return false
		}
	case "*":
		if n.kind != elementNode {
			return false
		}
	default:
		if n.kind != elementNode || n.Local != st.local {
			return false
		}
		want := ""
		if st.prefix != "" {
			uri, ok := ns[st.prefix]
			if !ok {
				panic(fmt.Sprintf("unattend: unknown XPath prefix %q", st.prefix))
			}
			want = uri
		}
		if n.Space != want {
			return false
		}
	}
	for _, p := range st.preds {
		switch {
		case p.localNames != nil:
			ok := false
			for _, l := range p.localNames {
				ok = ok || n.Local == l
			}
			if !ok {
				return false
			}
		default:
			v, ok := n.Attr(p.attr)
			if !ok || (p.value != nil && v != *p.value) {
				return false
			}
		}
	}
	return true
}

// SelectNodes evaluates expr from n. ns maps XPath prefixes to URIs.
func (n *Node) SelectNodes(expr string, ns map[string]string) []*Node {
	c := compileXPath(expr)
	seen := map[*Node]bool{}
	for _, p := range c.paths {
		start := n
		if p.absolute {
			start = n.OwnerDocument()
		}
		current := []*Node{start}
		for _, st := range p.steps {
			var next []*Node
			added := map[*Node]bool{}
			for _, ctx := range current {
				var candidates []*Node
				if st.descendant {
					candidates = descendants(ctx)
				} else {
					candidates = ctx.Children
				}
				for _, cand := range candidates {
					if !added[cand] && st.matches(cand, ns) {
						added[cand] = true
						next = append(next, cand)
					}
				}
			}
			current = next
		}
		for _, r := range current {
			seen[r] = true
		}
	}
	// Document order.
	var out []*Node
	for _, d := range descendants(n.OwnerDocument()) {
		if seen[d] {
			out = append(out, d)
		}
	}
	return out
}

// SelectNode returns the first match or nil.
func (n *Node) SelectNode(expr string, ns map[string]string) *Node {
	if r := n.SelectNodes(expr, ns); len(r) > 0 {
		return r[0]
	}
	return nil
}

// MustSelectNode returns the first match or panics, like
// SelectSingleNodeOrThrow.
func (n *Node) MustSelectNode(expr string, ns map[string]string) *Node {
	if r := n.SelectNode(expr, ns); r != nil {
		return r
	}
	panic(fmt.Sprintf("No node matches XPath '%s'.", expr))
}

// descendants lists all nodes below n in document order.
func descendants(n *Node) []*Node {
	var out []*Node
	var walk func(*Node)
	walk = func(x *Node) {
		for _, c := range x.Children {
			out = append(out, c)
			walk(c)
		}
	}
	walk(n)
	return out
}
