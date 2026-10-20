package control

import (
	"net/http"
	"strings"
)

type navItem struct {
	Label  string
	Path   string
	Active bool
}

type navGroup struct {
	Title string
	Items []navItem
}

type navEntry struct {
	label, path, action string
}

var navLayout = []struct {
	title   string
	entries []navEntry
}{
	{"", []navEntry{{"Установки", "/", "view"}, {"Меню загрузки", "/menu", "view"}}},
	{"Библиотека", []navEntry{{"Образы ISO", "/images", "view"}, {"Драйверы", "/drivers", "view"}, {"Файлы ответов", "/answers", "view"}}},
	{"Система", []navEntry{{"Хранилища", "/storages", "view"}, {"Сеть", "/network", "view"}, {"Пользователи", "/users", "users"}, {"Журнал", "/events", "view"}, {"Аудит", "/audit", "users"}}},
}

// navigation lists the sidebar sections the role may open, marking the one
// that holds the current page.
func navigation(role, path string) []navGroup {
	var groups []navGroup
	for _, layout := range navLayout {
		group := navGroup{Title: layout.title}
		for _, entry := range layout.entries {
			if !Allowed(role, entry.action) {
				continue
			}
			active := path == entry.path || (entry.path != "/" && strings.HasPrefix(path, entry.path+"/"))
			group.Items = append(group.Items, navItem{Label: entry.label, Path: entry.path, Active: active})
		}
		if len(group.Items) > 0 {
			groups = append(groups, group)
		}
	}
	return groups
}

func stylesheet(w http.ResponseWriter, _ *http.Request) {
	data, err := templateFS.ReadFile("templates/style.css")
	if err != nil {
		http.Error(w, "stylesheet unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}
