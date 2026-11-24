package wiki

import (
	"regexp"
	"strings"
	"testing"
)

func TestRenderMarkdownEscapesHTMLAndSupportsConstructs(t *testing.T) {
	md := strings.Join([]string{
		"## Первый раздел",
		"",
		"Абзац с **жирным**, *курсивом*, `кодом <script>` и [ссылкой](/wiki/overview).",
		"",
		"> [!IMPORTANT]",
		"> Проверка <img src=x onerror=alert(1)> текста.",
		"",
		"| Поле | Значение |",
		"| --- | --- |",
		"| Порт | `8443` |",
		"",
		"```bash",
		"echo \"hello <world>\"",
		"```",
		"",
		"- Пункт 1",
		"  - Подпункт 1.1",
		"- Пункт 2",
		"",
		"### Подраздел",
		"",
		"1. Шаг один",
		"2. Шаг два",
	}, "\n")

	rendered, toc, plain := RenderMarkdown(md)
	htmlStr := string(rendered)

	if strings.Contains(htmlStr, "<script>") || strings.Contains(htmlStr, "<img ") {
		t.Fatalf("raw HTML was not escaped: %s", htmlStr)
	}
	if !strings.Contains(htmlStr, `<h2 id="pervyy-razdel">Первый раздел</h2>`) {
		t.Errorf("missing expected h2 anchor in %s", htmlStr)
	}
	if !strings.Contains(htmlStr, `<div class="wiki-callout wiki-callout-warn"><strong>Важно:</strong>`) {
		t.Errorf("missing callout in %s", htmlStr)
	}
	if !strings.Contains(htmlStr, `<table class="wiki-table">`) {
		t.Errorf("missing table in %s", htmlStr)
	}
	if !strings.Contains(htmlStr, `data-wiki-copy`) {
		t.Errorf("missing code copy button in %s", htmlStr)
	}
	if !strings.Contains(htmlStr, `<ul><li>Подпункт 1.1</li></ul>`) {
		t.Errorf("missing nested list in %s", htmlStr)
	}
	if !strings.Contains(htmlStr, `<ol>`) {
		t.Errorf("missing ordered list in %s", htmlStr)
	}
	if len(toc) != 2 || toc[0].Level != 2 || toc[1].Level != 3 {
		t.Errorf("unexpected TOC: %+v", toc)
	}
	if !strings.Contains(plain, "Первый раздел") || !strings.Contains(plain, "Шаг два") {
		t.Errorf("unexpected plain text: %q", plain)
	}
}

func TestDefaultCatalogIntegrity(t *testing.T) {
	cat, err := Default()
	if err != nil {
		t.Fatalf("Default() error: %v", err)
	}
	if len(cat.Sections) != 6 {
		t.Errorf("got %d sections, want 6", len(cat.Sections))
	}
	if len(cat.Articles) != 17 {
		t.Errorf("got %d articles, want 17", len(cat.Articles))
	}

	wikiHref := regexp.MustCompile(`href="/wiki/([^"#]+)(?:#([^"]+))?"`)
	for i, art := range cat.Articles {
		if art.Title == "" || art.Summary == "" {
			t.Errorf("article %q missing title or summary", art.Slug)
		}
		if len(art.TOC) == 0 {
			t.Errorf("article %q has empty TOC", art.Slug)
		}
		if i > 0 && art.Prev != cat.Articles[i-1] {
			t.Errorf("article %q has wrong Prev", art.Slug)
		}
		if i+1 < len(cat.Articles) && art.Next != cat.Articles[i+1] {
			t.Errorf("article %q has wrong Next", art.Slug)
		}
		body := string(art.HTML)
		if strings.Contains(body, "<details") || strings.Contains(body, "<summary") {
			t.Errorf("article %q contains forbidden <details>/<summary>", art.Slug)
		}
		for _, m := range wikiHref.FindAllStringSubmatch(body, -1) {
			targetSlug, anchor := m[1], m[2]
			target, ok := cat.Article(targetSlug)
			if !ok {
				t.Errorf("article %q has broken link to /wiki/%s", art.Slug, targetSlug)
				continue
			}
			if anchor != "" {
				found := false
				for _, entry := range target.TOC {
					if entry.ID == anchor {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("article %q has broken anchor /wiki/%s#%s (TOC: %+v)", art.Slug, targetSlug, anchor, target.TOC)
				}
			}
		}
	}
}

func TestCatalogSearch(t *testing.T) {
	cat, err := Default()
	if err != nil {
		t.Fatalf("Default() error: %v", err)
	}
	hits := cat.Search("Secure Boot", 5)
	if len(hits) == 0 {
		t.Fatal("expected hits for 'Secure Boot'")
	}
	if hits[0].Article.Slug != "secure-boot" {
		t.Errorf("top hit for 'Secure Boot' = %q, want secure-boot", hits[0].Article.Slug)
	}
	if hits[0].Snippet == "" {
		t.Errorf("expected non-empty snippet")
	}

	hits2 := cat.Search("omb-controlctl reset-2fa", 5)
	if len(hits2) == 0 || hits2[0].Article.Slug != "users-auth-2fa" {
		t.Errorf("unexpected hits for 'omb-controlctl reset-2fa': %+v", hits2)
	}
}

func TestOfflineUpdatesArticleCLISignatures(t *testing.T) {
	cat, err := Default()
	if err != nil {
		t.Fatalf("Default() error: %v", err)
	}
	art, ok := cat.Article("offline-updates-and-recovery")
	if !ok {
		t.Fatal("article offline-updates-and-recovery not found")
	}
	body := string(art.HTML)
	if strings.Contains(body, "--repo-dir") {
		t.Errorf("offline-updates-and-recovery must not reference nonexistent --repo-dir flag")
	}
	for _, want := range []string{
		"offline-deb.py verify-repo",
		"offline-deploy.sh update-deb-repo",
		"offline-deploy.sh rollback MANAGEMENT_IP",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("offline-updates-and-recovery missing %q", want)
		}
	}
}
