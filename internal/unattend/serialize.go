package unattend

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Serialize writes a document as UnattendGenerator.Serialize does: an
// ASCII-only file with a utf-8 declaration, tab indentation and CRLF line
// ends. Windows Setup reads only ASCII, so other characters become
// character references.
func Serialize(doc *Document) []byte {
	var w xmlWriter
	w.b.WriteString("<?xml version=\"1.0\" encoding=\"utf-8\"?>\r\n")
	w.indent = true
	w.ascii = true
	root := doc.Root()
	w.element(root, 0, false, []map[string]string{{"xml": nsXML}})
	return []byte(w.b.String())
}

// prettyString writes an element tree without declaration, as
// Modifier.EmbedXmlFile does for embedded XML files (Unicode kept).
func prettyString(doc *Document) string {
	var w xmlWriter
	w.indent = true
	w.element(doc.Root(), 0, false, []map[string]string{{"xml": nsXML}})
	return w.b.String()
}

type xmlWriter struct {
	b      strings.Builder
	indent bool
	ascii  bool
	wrote  bool // anything written by the element writer yet
}

func (w *xmlWriter) newline(depth int) {
	if !w.indent || !w.wrote {
		return
	}
	w.b.WriteString("\r\n")
	for i := 0; i < depth; i++ {
		w.b.WriteByte('\t')
	}
}

// element writes n at depth. mixed is true inside content that already had
// text, where XmlWriter stops indenting.
func (w *xmlWriter) element(n *Node, depth int, mixed bool, scopes []map[string]string) {
	if !mixed {
		w.newline(depth)
	}
	w.wrote = true
	// Namespace declarations: those written on the node, plus one for the
	// element's own namespace if not in scope.
	declared := map[string]string{}
	for _, a := range n.Attrs {
		if a.Space == nsXMLNS {
			if a.Prefix == "" && a.Local == "xmlns" {
				declared[""] = a.Value
			} else {
				declared[a.Local] = a.Value
			}
		}
	}
	inScope := func(prefix string) (string, bool) {
		if uri, ok := declared[prefix]; ok {
			return uri, true
		}
		for i := len(scopes) - 1; i >= 0; i-- {
			if uri, ok := scopes[i][prefix]; ok {
				return uri, true
			}
		}
		return "", prefix == ""
	}
	prefixFor := func(space string) (string, bool) {
		for p, uri := range declared {
			if uri == space && p != "" {
				return p, true
			}
		}
		for i := len(scopes) - 1; i >= 0; i-- {
			for p, uri := range scopes[i] {
				if uri == space && p != "" {
					if cur, _ := inScope(p); cur == space {
						return p, true
					}
				}
			}
		}
		return "", false
	}
	var extra []Attr
	prefix := n.Prefix
	if uri, _ := inScope(prefix); uri != n.Space {
		if prefix != "" {
			if p, ok := prefixFor(n.Space); ok {
				prefix = p
			} else {
				declared[prefix] = n.Space
				extra = append(extra, Attr{Space: nsXMLNS, Local: prefix, Prefix: "xmlns", Value: n.Space})
			}
		} else {
			declared[""] = n.Space
			extra = append(extra, Attr{Space: nsXMLNS, Local: "xmlns", Value: n.Space})
		}
	}
	name := n.Local
	if prefix != "" {
		name = prefix + ":" + n.Local
	}
	w.b.WriteByte('<')
	w.b.WriteString(name)
	attrs := append(append([]Attr(nil), n.Attrs...), extra...)
	for _, a := range attrs {
		var an string
		switch {
		case a.Space == nsXMLNS && a.Prefix == "" && a.Local == "xmlns":
			an = "xmlns"
		case a.Space == nsXMLNS:
			an = "xmlns:" + a.Local
		case a.Space == "":
			an = a.Local
		case a.Space == nsXML:
			an = "xml:" + a.Local
		default:
			p, ok := prefixFor(a.Space)
			if !ok {
				p = a.Prefix
				if p == "" {
					p = "d1p1"
				}
				declared[p] = a.Space
				w.b.WriteString(fmt.Sprintf(` xmlns:%s="%s"`, p, w.escapeAttr(a.Space)))
			}
			an = p + ":" + a.Local
		}
		w.b.WriteByte(' ')
		w.b.WriteString(an)
		w.b.WriteString(`="`)
		w.b.WriteString(w.escapeAttr(a.Value))
		w.b.WriteByte('"')
	}
	if len(n.Children) == 0 {
		if n.IsEmpty {
			w.b.WriteString(" />")
		} else {
			w.b.WriteString("></")
			w.b.WriteString(name)
			w.b.WriteByte('>')
		}
		return
	}
	w.b.WriteByte('>')
	child := append(scopes, declared)
	inner := mixed
	for _, c := range n.Children {
		switch c.kind {
		case textNode:
			w.b.WriteString(w.escapeText(c.Data))
			inner = true
		case cdataNode:
			w.b.WriteString("<![CDATA[")
			w.b.WriteString(c.Data)
			w.b.WriteString("]]>")
			inner = true
		case commentNode:
			if !inner {
				w.newline(depth + 1)
			}
			w.b.WriteString("<!--")
			w.b.WriteString(c.Data)
			w.b.WriteString("-->")
		case elementNode:
			w.element(c, depth+1, inner, child)
		}
	}
	if !inner {
		w.newline(depth)
	}
	w.b.WriteString("</")
	w.b.WriteString(name)
	w.b.WriteByte('>')
}

// escapeText escapes element content as XmlWriter does with
// NewLineHandling.Replace: line breaks become CRLF.
func (w *xmlWriter) escapeText(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '&':
			b.WriteString("&amp;")
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '\r':
			b.WriteString("\r\n")
			if i+1 < len(s) && s[i+1] == '\n' {
				size++
			}
		case r == '\n':
			b.WriteString("\r\n")
		case w.ascii && r > 0x7f:
			fmt.Fprintf(&b, "&#x%X;", r)
		default:
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}

// escapeAttr escapes an attribute value; line breaks and tabs become
// character references.
func (w *xmlWriter) escapeAttr(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '&':
			b.WriteString("&amp;")
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '"':
			b.WriteString("&quot;")
		case r == '\t':
			b.WriteString("&#x9;")
		case r == '\n':
			b.WriteString("&#xA;")
		case r == '\r':
			b.WriteString("&#xD;")
		case w.ascii && r > 0x7f:
			fmt.Fprintf(&b, "&#x%X;", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
