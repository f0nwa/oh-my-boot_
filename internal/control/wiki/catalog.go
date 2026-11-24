package wiki

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"sort"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"
)

//go:embed ru/*.yaml ru/*.md en/*.yaml en/*.md
var embeddedFS embed.FS

// Section groups related wiki articles in the sidebar and on the /wiki landing page.
type Section struct {
	ID          string     `yaml:"id"`
	Order       int        `yaml:"order"`
	Title       string     `yaml:"title"`
	Description string     `yaml:"description"`
	Articles    []*Article `yaml:"-"`
}

// Article is one parsed and rendered wiki page.
type Article struct {
	Slug         string
	SectionID    string
	SectionTitle string
	Order        int
	Title        string
	Summary      string
	Keywords     []string
	HTML         template.HTML
	TOC          []TOCEntry
	Prev         *Article
	Next         *Article

	plainText   string
	lowerTitle  string
	lowerKeys   string
	lowerTOC    string
	lowerPlain  string
	rawMarkdown string
}

// SearchHit represents one ranked search match inside the embedded wiki.
type SearchHit struct {
	Article *Article
	Snippet string
	Score   int
}

// Catalog holds all loaded sections and articles in memory.
type Catalog struct {
	Sections []*Section
	Articles []*Article
	bySlug   map[string]*Article
}

type articleFrontmatter struct {
	Slug     string   `yaml:"slug"`
	Section  string   `yaml:"section"`
	Order    int      `yaml:"order"`
	Title    string   `yaml:"title"`
	Summary  string   `yaml:"summary"`
	Keywords []string `yaml:"keywords"`
}

var (
	ruOnce    sync.Once
	ruCatalog *Catalog
	ruErr     error

	enOnce    sync.Once
	enCatalog *Catalog
	enErr     error
)

// Default returns the singleton embedded Russian wiki catalog.
func Default() (*Catalog, error) {
	return ForLanguage("ru")
}

// ForLanguage returns the singleton embedded wiki catalog for "en" or "ru"
// (any unknown language falls back to "ru").
func ForLanguage(lang string) (*Catalog, error) {
	if strings.EqualFold(strings.TrimSpace(lang), "en") {
		enOnce.Do(func() {
			enCatalog, enErr = LoadFS(embeddedFS, "en")
		})
		return enCatalog, enErr
	}
	ruOnce.Do(func() {
		ruCatalog, ruErr = LoadFS(embeddedFS, "ru")
	})
	return ruCatalog, ruErr
}

// LoadFS parses sections.yaml and all *.md files in dir inside fsys.
func LoadFS(fsys fs.FS, dir string) (*Catalog, error) {
	secBytes, err := fs.ReadFile(fsys, dir+"/sections.yaml")
	if err != nil {
		return nil, fmt.Errorf("read sections.yaml: %w", err)
	}
	var sections []*Section
	if err := yaml.Unmarshal(secBytes, &sections); err != nil {
		return nil, fmt.Errorf("parse sections.yaml: %w", err)
	}
	sort.Slice(sections, func(i, j int) bool {
		if sections[i].Order != sections[j].Order {
			return sections[i].Order < sections[j].Order
		}
		return sections[i].ID < sections[j].ID
	})
	secByID := make(map[string]*Section, len(sections))
	for _, s := range sections {
		if s.ID == "" || s.Title == "" {
			return nil, fmt.Errorf("wiki section missing id or title")
		}
		if _, exists := secByID[s.ID]; exists {
			return nil, fmt.Errorf("duplicate wiki section id %q", s.ID)
		}
		secByID[s.ID] = s
	}

	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}

	bySlug := map[string]*Article{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		raw, err := fs.ReadFile(fsys, dir+"/"+entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", entry.Name(), err)
		}
		meta, body, err := splitFrontmatter(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name(), err)
		}
		if meta.Slug == "" || meta.Title == "" || meta.Section == "" {
			return nil, fmt.Errorf("%s: missing required frontmatter fields (slug, title, section)", entry.Name())
		}
		if _, dup := bySlug[meta.Slug]; dup {
			return nil, fmt.Errorf("duplicate wiki slug %q", meta.Slug)
		}
		sec, ok := secByID[meta.Section]
		if !ok {
			return nil, fmt.Errorf("%s: unknown section %q", entry.Name(), meta.Section)
		}
		lang := "ru"
		if dir == "en" || strings.HasSuffix(dir, "/en") {
			lang = "en"
		}
		rendered, toc, plain := RenderMarkdownWithLang(body, lang)
		var tocText strings.Builder
		for _, item := range toc {
			tocText.WriteString(item.Text)
			tocText.WriteByte(' ')
		}
		art := &Article{
			Slug:         meta.Slug,
			SectionID:    sec.ID,
			SectionTitle: sec.Title,
			Order:        meta.Order,
			Title:        meta.Title,
			Summary:      meta.Summary,
			Keywords:     meta.Keywords,
			HTML:         rendered,
			TOC:          toc,
			plainText:    plain,
			lowerTitle:   strings.ToLower(meta.Title),
			lowerKeys:    strings.ToLower(strings.Join(meta.Keywords, " ") + " " + meta.Summary),
			lowerTOC:     strings.ToLower(tocText.String()),
			lowerPlain:   strings.ToLower(plain),
			rawMarkdown:  body,
		}
		bySlug[art.Slug] = art
		sec.Articles = append(sec.Articles, art)
	}

	var ordered []*Article
	for _, sec := range sections {
		sort.Slice(sec.Articles, func(i, j int) bool {
			if sec.Articles[i].Order != sec.Articles[j].Order {
				return sec.Articles[i].Order < sec.Articles[j].Order
			}
			return sec.Articles[i].Slug < sec.Articles[j].Slug
		})
		ordered = append(ordered, sec.Articles...)
	}
	for i, art := range ordered {
		if i > 0 {
			art.Prev = ordered[i-1]
		}
		if i+1 < len(ordered) {
			art.Next = ordered[i+1]
		}
	}

	return &Catalog{
		Sections: sections,
		Articles: ordered,
		bySlug:   bySlug,
	}, nil
}

// Article returns the article by slug, if found.
func (c *Catalog) Article(slug string) (*Article, bool) {
	if c == nil {
		return nil, false
	}
	a, ok := c.bySlug[slug]
	return a, ok
}

// Search performs case-insensitive multi-term search across titles, keywords,
// headings, and article bodies, returning ranked matches up to limit.
func (c *Catalog) Search(query string, limit int) []SearchHit {
	if c == nil {
		return nil
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	terms := strings.Fields(q)
	if len(terms) == 0 {
		return nil
	}
	var hits []SearchHit
	for _, art := range c.Articles {
		score := 0
		matchedAll := true
		for _, term := range terms {
			termScore := 0
			if strings.Contains(art.lowerTitle, term) {
				termScore += 10
			}
			if strings.Contains(art.lowerKeys, term) {
				termScore += 6
			}
			if strings.Contains(art.lowerTOC, term) {
				termScore += 4
			}
			if strings.Contains(art.lowerPlain, term) {
				termScore += 2
			}
			if termScore == 0 {
				matchedAll = false
				break
			}
			score += termScore
		}
		if !matchedAll {
			continue
		}
		if strings.Contains(art.lowerTitle, q) {
			score += 8
		}
		hits = append(hits, SearchHit{
			Article: art,
			Snippet: extractSnippet(art, terms[0]),
			Score:   score,
		})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Article.Title < hits[j].Article.Title
	})
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

func extractSnippet(art *Article, term string) string {
	lines := strings.Split(art.plainText, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(strings.ToLower(line), term) {
			return truncateRunes(line, 180)
		}
	}
	if art.Summary != "" {
		return truncateRunes(art.Summary, 180)
	}
	return truncateRunes(strings.ReplaceAll(art.plainText, "\n", " "), 180)
}

func truncateRunes(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	return strings.TrimSpace(string(r[:max])) + "…"
}

func splitFrontmatter(raw []byte) (articleFrontmatter, string, error) {
	var meta articleFrontmatter
	data := bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(data, []byte("---\n")) {
		return meta, "", fmt.Errorf("missing YAML frontmatter")
	}
	rest := data[4:]
	end := bytes.Index(rest, []byte("\n---\n"))
	if end < 0 {
		return meta, "", fmt.Errorf("unterminated YAML frontmatter")
	}
	if err := yaml.Unmarshal(rest[:end], &meta); err != nil {
		return meta, "", fmt.Errorf("parse YAML frontmatter: %w", err)
	}
	body := strings.TrimSpace(string(rest[end+5:]))
	return meta, body, nil
}
