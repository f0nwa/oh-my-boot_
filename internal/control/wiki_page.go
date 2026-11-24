package control

import (
	_ "embed"
	"net/http"
	"strings"

	"omb/internal/control/wiki"
)

type wikiView struct {
	Sections []*wiki.Section
	Article  *wiki.Article
	Query    string
	Hits     []wiki.SearchHit
	NotFound bool
}

func (s *Server) wikiCatalog(lang string) (*wiki.Catalog, error) {
	return wiki.ForLanguage(lang)
}

func (s *Server) wikiHome(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	cat, err := s.wikiCatalog(normalizeLanguage(info.User.Language))
	if err != nil {
		http.Error(w, "wiki unavailable", http.StatusInternalServerError)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	view := &wikiView{
		Sections: cat.Sections,
		Query:    q,
	}
	if q != "" {
		view.Hits = cat.Search(q, 30)
	}
	render(w, "wiki", pageData{
		Info: info,
		Wiki: view,
	})
}

func (s *Server) wikiArticle(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	cat, err := s.wikiCatalog(normalizeLanguage(info.User.Language))
	if err != nil {
		http.Error(w, "wiki unavailable", http.StatusInternalServerError)
		return
	}
	slug := strings.TrimSpace(r.PathValue("slug"))
	art, ok := cat.Article(slug)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		render(w, "wiki", pageData{
			Info: info,
			Wiki: &wikiView{
				Sections: cat.Sections,
				NotFound: true,
			},
		})
		return
	}
	render(w, "wiki", pageData{
		Info: info,
		Wiki: &wikiView{
			Sections: cat.Sections,
			Article:  art,
		},
	})
}

//go:embed wiki.js
var wikiJS []byte

func wikiScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(wikiJS)
}
