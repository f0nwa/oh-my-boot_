package unattend

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// A small XML DOM with the parts of System.Xml.XmlDocument the generator
// uses. Whitespace-only text is dropped on load, as XmlDocument does without
// PreserveWhitespace; elements remember whether they were written empty
// (<a/>) so they serialize the same way.

const (
	nsUnattend = "urn:schemas-microsoft-com:unattend"
	nsWCM      = "http://schemas.microsoft.com/WMIConfig/2002/State"
	nsMine     = "https://schneegans.de/windows/unattend-generator/"
	nsXMLNS    = "http://www.w3.org/2000/xmlns/"
	nsXML      = "http://www.w3.org/XML/1998/namespace"
)

type nodeKind int

const (
	documentNode nodeKind = iota
	elementNode
	textNode
	commentNode
	cdataNode
)

// Node is a document, an element, a text, a CDATA section or a comment.
type Node struct {
	kind     nodeKind
	Space    string // namespace URI of an element
	Local    string // local name of an element
	Prefix   string // prefix the element was written with, if any
	Attrs    []Attr
	Children []*Node
	Parent   *Node
	Data     string // text, CDATA or comment content
	// IsEmpty is XmlElement.IsEmpty: an element without children is written
	// as <a /> when true and as <a></a> when false.
	IsEmpty bool
}

// Attr is an attribute; namespace declarations are attributes in the xmlns
// namespace, as in System.Xml.
type Attr struct {
	Space, Local, Prefix, Value string
}

// Document is the root of a tree.
type Document = Node

// NewDocument returns an empty document.
func NewDocument() *Document { return &Node{kind: documentNode} }

// Root returns the document element.
func (n *Node) Root() *Node {
	for _, c := range n.Children {
		if c.kind == elementNode {
			return c
		}
	}
	return nil
}

// IsElement reports whether the node is an element.
func (n *Node) IsElement() bool { return n.kind == elementNode }

// IsText reports whether the node is text or CDATA.
func (n *Node) IsText() bool { return n.kind == textNode || n.kind == cdataNode }

// OwnerDocument walks up to the document.
func (n *Node) OwnerDocument() *Node {
	for n.Parent != nil {
		n = n.Parent
	}
	return n
}

// NewElement makes a detached element in a namespace, like
// XmlDocument.CreateElement(name, ns).
func NewElement(space, local string) *Node {
	return &Node{kind: elementNode, Space: space, Local: local, IsEmpty: true}
}

// NewText makes a detached text node.
func NewText(s string) *Node { return &Node{kind: textNode, Data: s} }

// AppendChild adds c as the last child of n, detaching it first.
func (n *Node) AppendChild(c *Node) *Node {
	c.detach()
	c.Parent = n
	n.Children = append(n.Children, c)
	n.IsEmpty = false
	return c
}

// PrependChild adds c as the first child of n.
func (n *Node) PrependChild(c *Node) *Node {
	c.detach()
	c.Parent = n
	n.Children = append([]*Node{c}, n.Children...)
	n.IsEmpty = false
	return c
}

// InsertAfter puts c right after ref, a child of n.
func (n *Node) InsertAfter(c, ref *Node) *Node {
	c.detach()
	i := n.indexOf(ref)
	c.Parent = n
	n.Children = append(n.Children[:i+1], append([]*Node{c}, n.Children[i+1:]...)...)
	return c
}

// InsertBefore puts c right before ref, a child of n.
func (n *Node) InsertBefore(c, ref *Node) *Node {
	c.detach()
	i := n.indexOf(ref)
	c.Parent = n
	n.Children = append(n.Children[:i], append([]*Node{c}, n.Children[i:]...)...)
	return c
}

func (n *Node) indexOf(c *Node) int {
	for i, x := range n.Children {
		if x == c {
			return i
		}
	}
	panic("unattend: node is not a child")
}

func (n *Node) detach() {
	if n.Parent == nil {
		return
	}
	p := n.Parent
	i := p.indexOf(n)
	p.Children = append(p.Children[:i], p.Children[i+1:]...)
	n.Parent = nil
}

// RemoveSelf takes the node out of its parent.
func (n *Node) RemoveSelf() { n.detach() }

// RemoveAll drops all children and attributes, like XmlElement.RemoveAll.
func (n *Node) RemoveAll() {
	for _, c := range n.Children {
		c.Parent = nil
	}
	n.Children = nil
	n.Attrs = nil
}

// InnerText is the concatenated text of the node and its descendants.
func (n *Node) InnerText() string {
	if n.kind != elementNode && n.kind != documentNode {
		return n.Data
	}
	var b strings.Builder
	var walk func(*Node)
	walk = func(x *Node) {
		for _, c := range x.Children {
			switch c.kind {
			case textNode, cdataNode:
				b.WriteString(c.Data)
			case elementNode:
				walk(c)
			}
		}
	}
	walk(n)
	return b.String()
}

// SetInnerText replaces the children with one text node, as the
// XmlElement.InnerText setter does; an empty string still leaves an (empty)
// text child.
func (n *Node) SetInnerText(s string) {
	if len(n.Children) == 1 && n.Children[0].kind == textNode {
		n.Children[0].Data = s
		return
	}
	for _, c := range n.Children {
		c.Parent = nil
	}
	n.Children = nil
	n.AppendChild(NewText(s))
}

// Attr returns the value of an attribute without namespace.
func (n *Node) Attr(local string) (string, bool) {
	return n.AttrNS("", local)
}

// AttrNS returns the value of an attribute in a namespace.
func (n *Node) AttrNS(space, local string) (string, bool) {
	for _, a := range n.Attrs {
		if a.Space == space && a.Local == local {
			return a.Value, true
		}
	}
	return "", false
}

// SetAttr sets an attribute without namespace.
func (n *Node) SetAttr(local, value string) { n.SetAttrNS("", local, value) }

// SetAttrNS sets an attribute in a namespace, keeping its place if present.
func (n *Node) SetAttrNS(space, local, value string) {
	for i, a := range n.Attrs {
		if a.Space == space && a.Local == local {
			n.Attrs[i].Value = value
			return
		}
	}
	n.Attrs = append(n.Attrs, Attr{Space: space, Local: local, Value: value})
}

// RemoveAttr drops an attribute without namespace.
func (n *Node) RemoveAttr(local string) {
	for i, a := range n.Attrs {
		if a.Space == "" && a.Local == local {
			n.Attrs = append(n.Attrs[:i], n.Attrs[i+1:]...)
			return
		}
	}
}

// Clone deep-copies the node, detached.
func (n *Node) Clone() *Node {
	c := *n
	c.Parent = nil
	c.Attrs = append([]Attr(nil), n.Attrs...)
	c.Children = nil
	for _, x := range n.Children {
		cc := x.Clone()
		cc.Parent = &c
		c.Children = append(c.Children, cc)
	}
	return &c
}

// Normalize merges adjacent text nodes, like XmlNode.Normalize.
func (n *Node) Normalize() {
	var out []*Node
	for _, c := range n.Children {
		if c.kind == textNode && len(out) > 0 && out[len(out)-1].kind == textNode {
			out[len(out)-1].Data += c.Data
			continue
		}
		out = append(out, c)
		if c.kind == elementNode {
			c.Normalize()
		}
	}
	n.Children = out
}

// Elements returns the child elements.
func (n *Node) Elements() []*Node {
	var out []*Node
	for _, c := range n.Children {
		if c.kind == elementNode {
			out = append(out, c)
		}
	}
	return out
}

// Depth counts the element ancestors, like ancestor::* in XPath.
func (n *Node) Depth() int {
	d := 0
	for p := n.Parent; p != nil && p.kind == elementNode; p = p.Parent {
		d++
	}
	return d
}

// ParseXML loads a document the way XmlDocument.Load does: whitespace-only
// text outside xml:space="preserve" is dropped, a BOM and the declaration
// are skipped.
func ParseXML(data []byte) (*Document, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	doc := NewDocument()
	cur := doc
	type scope map[string]string // prefix -> URI
	scopes := []scope{{"xml": nsXML, "xmlns": nsXMLNS}}
	lookup := func(prefix string) (string, bool) {
		for i := len(scopes) - 1; i >= 0; i-- {
			if uri, ok := scopes[i][prefix]; ok {
				return uri, true
			}
		}
		return "", prefix == ""
	}
	preserve := []bool{false}
	for {
		tok, err := dec.RawToken()
		if err == io.EOF {
			if cur.kind != documentNode {
				return nil, fmt.Errorf("element <%s> is not closed", cur.Local)
			}
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			sc := scope{}
			p := preserve[len(preserve)-1]
			for _, a := range t.Attr {
				switch {
				case a.Name.Space == "xmlns":
					sc[a.Name.Local] = a.Value
				case a.Name.Space == "" && a.Name.Local == "xmlns":
					sc[""] = a.Value
				case a.Name.Space == "xml" && a.Name.Local == "space":
					p = a.Value == "preserve"
				}
			}
			scopes = append(scopes, sc)
			preserve = append(preserve, p)
			space, ok := lookup(t.Name.Space)
			if !ok {
				return nil, fmt.Errorf("undeclared prefix %q", t.Name.Space)
			}
			el := &Node{kind: elementNode, Space: space, Local: t.Name.Local, Prefix: t.Name.Space, IsEmpty: true}
			for _, a := range t.Attr {
				var at Attr
				switch {
				case a.Name.Space == "xmlns":
					at = Attr{Space: nsXMLNS, Local: a.Name.Local, Prefix: "xmlns", Value: a.Value}
				case a.Name.Space == "" && a.Name.Local == "xmlns":
					at = Attr{Space: nsXMLNS, Local: "xmlns", Value: a.Value}
				case a.Name.Space == "":
					at = Attr{Local: a.Name.Local, Value: a.Value}
				default:
					uri, ok := lookup(a.Name.Space)
					if !ok {
						return nil, fmt.Errorf("undeclared prefix %q", a.Name.Space)
					}
					at = Attr{Space: uri, Local: a.Name.Local, Prefix: a.Name.Space, Value: a.Value}
				}
				el.Attrs = append(el.Attrs, at)
			}
			cur.AppendChild(el)
			el.IsEmpty = true
			cur = el
		case xml.EndElement:
			if cur.kind != elementNode || t.Name.Local != cur.Local || t.Name.Space != cur.Prefix {
				return nil, fmt.Errorf("unexpected end element </%s>", t.Name.Local)
			}
			// An element closed right after it was opened stays IsEmpty only
			// when written as <a/>; encoding/xml reports both forms alike,
			// so look at the raw input.
			if len(cur.Children) == 0 {
				off := dec.InputOffset()
				cur.IsEmpty = off >= 2 && string(data[off-2:off]) == "/>"
			}
			scopes = scopes[:len(scopes)-1]
			preserve = preserve[:len(preserve)-1]
			cur = cur.Parent
		case xml.CharData:
			s := string(t)
			if strings.TrimSpace(s) == "" && !preserve[len(preserve)-1] {
				continue
			}
			if cur.kind == documentNode {
				continue
			}
			cur.AppendChild(NewText(s))
		case xml.Comment:
			if cur.kind == documentNode {
				continue
			}
			cur.AppendChild(&Node{kind: commentNode, Data: string(t)})
		}
	}
	if doc.Root() == nil {
		return nil, fmt.Errorf("no root element")
	}
	return doc, nil
}

func mustParseXML(data []byte) *Document {
	doc, err := ParseXML(data)
	if err != nil {
		panic(err)
	}
	return doc
}
