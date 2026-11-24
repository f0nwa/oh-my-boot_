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
	catRU, err := ForLanguage("ru")
	if err != nil {
		t.Fatalf("ForLanguage(ru) error: %v", err)
	}
	catEN, err := ForLanguage("en")
	if err != nil {
		t.Fatalf("ForLanguage(en) error: %v", err)
	}

	wikiHref := regexp.MustCompile(`href="/wiki/([^"#]+)(?:#([^"]+))?"`)
	cyrillic := regexp.MustCompile(`\p{Cyrillic}`)

	for _, tc := range []struct {
		lang string
		cat  *Catalog
	}{
		{"ru", catRU},
		{"en", catEN},
	} {
		t.Run(tc.lang, func(t *testing.T) {
			cat := tc.cat
			if len(cat.Sections) != 6 {
				t.Errorf("got %d sections, want 6", len(cat.Sections))
			}
			if len(cat.Articles) != 17 {
				t.Errorf("got %d articles, want 17", len(cat.Articles))
			}
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
				if tc.lang == "en" {
					if cyrillic.MatchString(art.Title) || cyrillic.MatchString(art.Summary) || cyrillic.MatchString(art.SectionTitle) || cyrillic.MatchString(body) {
						t.Errorf("English article %q contains Cyrillic text", art.Slug)
					}
					if strings.Contains(body, ">Скопировать<") {
						t.Errorf("English article %q contains Russian copy button label", art.Slug)
					}
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
		})
	}

	// Verify 1:1 structural parity between RU and EN catalogs.
	for i := range catRU.Sections {
		if catRU.Sections[i].ID != catEN.Sections[i].ID || catRU.Sections[i].Order != catEN.Sections[i].Order {
			t.Errorf("section %d mismatch: ru=(%s,%d) en=(%s,%d)", i, catRU.Sections[i].ID, catRU.Sections[i].Order, catEN.Sections[i].ID, catEN.Sections[i].Order)
		}
	}
	for i := range catRU.Articles {
		ruArt, enArt := catRU.Articles[i], catEN.Articles[i]
		if ruArt.Slug != enArt.Slug || ruArt.SectionID != enArt.SectionID || ruArt.Order != enArt.Order {
			t.Errorf("article %d mismatch: ru=(%s,%s,%d) en=(%s,%s,%d)", i, ruArt.Slug, ruArt.SectionID, ruArt.Order, enArt.Slug, enArt.SectionID, enArt.Order)
		}
	}
}

func TestRenderMarkdownEnglishLabels(t *testing.T) {
	md := strings.Join([]string{
		"> [!NOTE]",
		"> English note.",
		"",
		"> [!IMPORTANT]",
		"> English important.",
		"",
		"> [!WARNING]",
		"> English warning.",
		"",
		"```bash",
		"echo hi",
		"```",
	}, "\n")
	rendered, _, _ := RenderMarkdownWithLang(md, "en")
	htmlStr := string(rendered)
	for _, want := range []string{
		`<strong>Note:</strong>`,
		`<strong>Important:</strong>`,
		`<strong>Warning:</strong>`,
		`data-wiki-copy>Copy</button>`,
	} {
		if !strings.Contains(htmlStr, want) {
			t.Errorf("RenderMarkdownWithLang(en) missing %q in %s", want, htmlStr)
		}
	}
}

func TestCatalogSearch(t *testing.T) {
	for _, lang := range []string{"ru", "en"} {
		cat, err := ForLanguage(lang)
		if err != nil {
			t.Fatalf("ForLanguage(%s) error: %v", lang, err)
		}
		hits := cat.Search("Secure Boot", 5)
		if len(hits) == 0 {
			t.Fatalf("[%s] expected hits for 'Secure Boot'", lang)
		}
		if hits[0].Article.Slug != "secure-boot" {
			t.Errorf("[%s] top hit for 'Secure Boot' = %q, want secure-boot", lang, hits[0].Article.Slug)
		}
		if hits[0].Snippet == "" {
			t.Errorf("[%s] expected non-empty snippet", lang)
		}

		hits2 := cat.Search("omb-controlctl reset-2fa", 5)
		if len(hits2) == 0 || hits2[0].Article.Slug != "users-auth-2fa" {
			t.Errorf("[%s] unexpected hits for 'omb-controlctl reset-2fa': %+v", lang, hits2)
		}
	}

	catEN, _ := ForLanguage("en")
	hitsEN := catEN.Search("offline update rollback", 5)
	if len(hitsEN) == 0 || hitsEN[0].Article.Slug != "offline-updates-and-recovery" {
		t.Errorf("EN search 'offline update rollback' = %+v, want offline-updates-and-recovery", hitsEN)
	}
}

func TestOfflineUpdatesArticleCLISignatures(t *testing.T) {
	for _, lang := range []string{"ru", "en"} {
		cat, err := ForLanguage(lang)
		if err != nil {
			t.Fatalf("ForLanguage(%s) error: %v", lang, err)
		}
		art, ok := cat.Article("offline-updates-and-recovery")
		if !ok {
			t.Fatalf("[%s] article offline-updates-and-recovery not found", lang)
		}
		body := string(art.HTML)
		for _, forbidden := range []string{
			"--repo-dir",
			"offline-release.py",
			"update-deb-repo",
			"install-deb-repo",
			"from_commit",
		} {
			if strings.Contains(body, forbidden) {
				t.Errorf("[%s] offline-updates-and-recovery must not reference obsolete %q", lang, forbidden)
			}
		}
		for _, want := range []string{
			"offline-deb.py verify-repo",
			"offline-deploy.sh install",
			"offline-deploy.sh update",
			"--allow-channel-switch",
			"offline-deploy.sh rollback MANAGEMENT_IP",
			"schema_migrations",
			"/var/lib/omb-deployment/current.deb",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("[%s] offline-updates-and-recovery missing %q", lang, want)
			}
		}
	}
}
