package wiki

import (
	"fmt"
	"html"
	"html/template"
	"regexp"
	"strings"
)

// TOCEntry represents one heading in the right-hand "On this page" table of contents.
type TOCEntry struct {
	Level int
	ID    string
	Text  string
}

var (
	reOrderedItem = regexp.MustCompile(`^\d+\.\s+(.*)$`)
	reLink        = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	reBold        = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	reItalic      = regexp.MustCompile(`\*([^*\n]+)\*`)
)

// RenderMarkdown converts a subset of GitHub-Flavored Markdown used by OMB Wiki
// into safe HTML (all raw HTML is escaped) and returns extracted h2/h3 TOC entries
// plus plain text for search indexing.
func RenderMarkdown(src string) (template.HTML, []TOCEntry, string) {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var out strings.Builder
	var plain strings.Builder
	var toc []TOCEntry
	usedIDs := map[string]int{}

	addPlain := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" {
			plain.WriteString(s)
			plain.WriteByte('\n')
		}
	}

	i := 0
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			i++
			continue
		}

		// Fenced code block ```lang
		if strings.HasPrefix(trimmed, "```") {
			lang := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			i++
			var codeLines []string
			for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
				codeLines = append(codeLines, lines[i])
				i++
			}
			if i < len(lines) {
				i++ // skip closing ```
			}
			code := strings.Join(codeLines, "\n")
			label := lang
			if label == "" {
				label = "text"
			}
			out.WriteString(`<div class="wiki-code-block"><div class="wiki-code-head"><span>`)
			out.WriteString(html.EscapeString(label))
			out.WriteString(`</span><button type="button" class="wiki-code-copy" data-wiki-copy>Скопировать</button></div><pre><code>`)
			out.WriteString(html.EscapeString(code))
			out.WriteString("</code></pre></div>\n")
			addPlain(code)
			continue
		}

		// Headings ## and ###
		if strings.HasPrefix(trimmed, "### ") || strings.HasPrefix(trimmed, "## ") {
			level := 2
			rawTitle := ""
			if strings.HasPrefix(trimmed, "### ") {
				level = 3
				rawTitle = strings.TrimSpace(strings.TrimPrefix(trimmed, "### "))
			} else {
				rawTitle = strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
			}
			cleanText := stripInlineMarkdown(rawTitle)
			id := uniqueAnchorID(cleanText, usedIDs)
			toc = append(toc, TOCEntry{Level: level, ID: id, Text: cleanText})
			fmt.Fprintf(&out, "<h%d id=\"%s\">%s</h%d>\n", level, html.EscapeString(id), renderInline(rawTitle), level)
			addPlain(cleanText)
			i++
			continue
		}

		// Callout / blockquote
		if strings.HasPrefix(trimmed, ">") {
			var quoteLines []string
			for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), ">") {
				q := strings.TrimSpace(lines[i])
				q = strings.TrimPrefix(q, ">")
				q = strings.TrimPrefix(q, " ")
				quoteLines = append(quoteLines, q)
				i++
			}
			kind := ""
			label := ""
			if len(quoteLines) > 0 {
				first := strings.TrimSpace(quoteLines[0])
				switch {
				case strings.HasPrefix(first, "[!NOTE]"):
					kind = "info"
					label = "Заметка"
					quoteLines[0] = strings.TrimSpace(strings.TrimPrefix(first, "[!NOTE]"))
				case strings.HasPrefix(first, "[!IMPORTANT]"):
					kind = "warn"
					label = "Важно"
					quoteLines[0] = strings.TrimSpace(strings.TrimPrefix(first, "[!IMPORTANT]"))
				case strings.HasPrefix(first, "[!WARNING]"):
					kind = "bad"
					label = "Внимание"
					quoteLines[0] = strings.TrimSpace(strings.TrimPrefix(first, "[!WARNING]"))
				}
			}
			var body []string
			for _, ql := range quoteLines {
				if strings.TrimSpace(ql) != "" {
					body = append(body, strings.TrimSpace(ql))
				}
			}
			text := strings.Join(body, " ")
			cls := "wiki-callout"
			if kind != "" {
				cls += " wiki-callout-" + kind
			}
			out.WriteString(`<div class="` + cls + `">`)
			if label != "" {
				out.WriteString(`<strong>` + html.EscapeString(label) + `:</strong> `)
			}
			out.WriteString(renderInline(text))
			out.WriteString("</div>\n")
			addPlain(stripInlineMarkdown(text))
			continue
		}

		// GFM table
		if strings.HasPrefix(trimmed, "|") && i+1 < len(lines) && isTableDivider(lines[i+1]) {
			headers := splitTableRow(lines[i])
			i += 2
			var rows [][]string
			for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|") {
				rows = append(rows, splitTableRow(lines[i]))
				i++
			}
			out.WriteString(`<div class="wiki-table-wrap"><table class="wiki-table"><thead><tr>`)
			for _, h := range headers {
				out.WriteString("<th>" + renderInline(h) + "</th>")
				addPlain(stripInlineMarkdown(h))
			}
			out.WriteString("</tr></thead><tbody>")
			for _, row := range rows {
				out.WriteString("<tr>")
				for c := 0; c < len(headers); c++ {
					cell := ""
					if c < len(row) {
						cell = row[c]
					}
					out.WriteString("<td>" + renderInline(cell) + "</td>")
					addPlain(stripInlineMarkdown(cell))
				}
				out.WriteString("</tr>")
			}
			out.WriteString("</tbody></table></div>\n")
			continue
		}

		// Unordered list
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			out.WriteString("<ul>\n")
			for i < len(lines) {
				t := strings.TrimSpace(lines[i])
				if !strings.HasPrefix(t, "- ") && !strings.HasPrefix(t, "* ") {
					break
				}
				item := strings.TrimSpace(t[2:])
				out.WriteString("<li>" + renderInline(item))
				addPlain(stripInlineMarkdown(item))
				i++
				// Optional single level of nested bullet items (indented by >= 2 spaces)
				var nested []string
				for i < len(lines) {
					raw := lines[i]
					if (strings.HasPrefix(raw, "  - ") || strings.HasPrefix(raw, "  * ") || strings.HasPrefix(raw, "    - ") || strings.HasPrefix(raw, "    * ")) {
						nt := strings.TrimSpace(raw)
						nested = append(nested, strings.TrimSpace(nt[2:]))
						i++
					} else {
						break
					}
				}
				if len(nested) > 0 {
					out.WriteString("<ul>")
					for _, n := range nested {
						out.WriteString("<li>" + renderInline(n) + "</li>")
						addPlain(stripInlineMarkdown(n))
					}
					out.WriteString("</ul>")
				}
				out.WriteString("</li>\n")
			}
			out.WriteString("</ul>\n")
			continue
		}

		// Ordered list
		if reOrderedItem.MatchString(trimmed) {
			out.WriteString("<ol>\n")
			for i < len(lines) {
				t := strings.TrimSpace(lines[i])
				m := reOrderedItem.FindStringSubmatch(t)
				if m == nil {
					break
				}
				item := strings.TrimSpace(m[1])
				out.WriteString("<li>" + renderInline(item) + "</li>\n")
				addPlain(stripInlineMarkdown(item))
				i++
			}
			out.WriteString("</ol>\n")
			continue
		}

		// Horizontal rule
		if trimmed == "---" || trimmed == "***" {
			out.WriteString("<hr>\n")
			i++
			continue
		}

		// Paragraph: collect contiguous non-special lines
		var para []string
		for i < len(lines) {
			t := strings.TrimSpace(lines[i])
			if t == "" ||
				strings.HasPrefix(t, "```") ||
				strings.HasPrefix(t, "## ") ||
				strings.HasPrefix(t, "### ") ||
				strings.HasPrefix(t, ">") ||
				strings.HasPrefix(t, "- ") ||
				strings.HasPrefix(t, "* ") ||
				reOrderedItem.MatchString(t) ||
				t == "---" ||
				(strings.HasPrefix(t, "|") && i+1 < len(lines) && isTableDivider(lines[i+1])) {
				break
			}
			para = append(para, t)
			i++
		}
		text := strings.Join(para, " ")
		out.WriteString("<p>" + renderInline(text) + "</p>\n")
		addPlain(stripInlineMarkdown(text))
	}

	return template.HTML(out.String()), toc, plain.String()
}

func isTableDivider(line string) bool {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "|") {
		return false
	}
	cells := splitTableRow(t)
	if len(cells) == 0 {
		return false
	}
	for _, c := range cells {
		c = strings.TrimSpace(c)
		c = strings.Trim(c, ":")
		if len(c) < 3 || strings.Trim(c, "-") != "" {
			return false
		}
	}
	return true
}

func splitTableRow(line string) []string {
	t := strings.TrimSpace(line)
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")
	parts := strings.Split(t, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func renderInline(s string) string {
	// Split by backtick inline code first so markdown inside `code` is never interpreted.
	parts := strings.Split(s, "`")
	var out strings.Builder
	for i, part := range parts {
		if i%2 == 1 {
			out.WriteString("<code>")
			out.WriteString(html.EscapeString(part))
			out.WriteString("</code>")
			continue
		}
		out.WriteString(renderInlineText(part))
	}
	return out.String()
}

func renderInlineText(s string) string {
	// Process links [text](url) first so URLs aren't mangled by italic/bold.
	var out strings.Builder
	last := 0
	for _, loc := range reLink.FindAllStringSubmatchIndex(s, -1) {
		out.WriteString(renderEmphasis(s[last:loc[0]]))
		label := s[loc[2]:loc[3]]
		href := strings.TrimSpace(s[loc[4]:loc[5]])
		if isSafeHref(href) {
			extra := ""
			if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
				extra = ` target="_blank" rel="noopener noreferrer"`
			}
			out.WriteString(`<a href="` + html.EscapeString(href) + `"` + extra + `>` + renderEmphasis(label) + `</a>`)
		} else {
			out.WriteString(renderEmphasis(label))
		}
		last = loc[1]
	}
	out.WriteString(renderEmphasis(s[last:]))
	return out.String()
}

func renderEmphasis(s string) string {
	escaped := html.EscapeString(s)
	escaped = reBold.ReplaceAllString(escaped, "<strong>$1</strong>")
	escaped = reItalic.ReplaceAllString(escaped, "<em>$1</em>")
	return escaped
}

func isSafeHref(href string) bool {
	if href == "" {
		return false
	}
	if strings.HasPrefix(href, "/") || strings.HasPrefix(href, "#") {
		return true
	}
	if strings.HasPrefix(href, "https://") || strings.HasPrefix(href, "http://") {
		return true
	}
	return false
}

func stripInlineMarkdown(s string) string {
	s = reLink.ReplaceAllString(s, "$1")
	s = strings.ReplaceAll(s, "`", "")
	s = reBold.ReplaceAllString(s, "$1")
	s = reItalic.ReplaceAllString(s, "$1")
	return strings.TrimSpace(s)
}

var ruTranslit = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e",
	'ж': "zh", 'з': "z", 'и': "i", 'й': "y", 'к': "k", 'л': "l", 'м': "m",
	'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u",
	'ф': "f", 'х': "h", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "sch", 'ъ': "",
	'ы': "y", 'ь': "", 'э': "e", 'ю': "yu", 'я': "ya",
}

func slugifyHeading(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
		} else if tr, ok := ruTranslit[r]; ok {
			b.WriteString(tr)
			prevDash = false
		} else if !prevDash && b.Len() > 0 {
			b.WriteByte('-')
			prevDash = true
		}
	}
	res := strings.Trim(b.String(), "-")
	if res == "" {
		return "section"
	}
	return res
}

func uniqueAnchorID(text string, used map[string]int) string {
	base := slugifyHeading(text)
	count := used[base]
	used[base] = count + 1
	if count == 0 {
		return base
	}
	return fmt.Sprintf("%s-%d", base, count+1)
}
