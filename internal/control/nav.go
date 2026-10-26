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

// navGroup is one top-level section of the header. Items are its pages;
// a section with more than one item shows them as tabs under the header.
type navGroup struct {
	Title  string
	Path   string
	Active bool
	Items  []navItem
}

type navEntry struct {
	label, path, action string
	// also lists extra path prefixes that belong to this page.
	also []string
}

var navLayout = []struct {
	title   string
	entries []navEntry
}{
	{"Сейчас", []navEntry{{label: "Сейчас", path: "/", action: "view"}}},
	{"Установки", []navEntry{{label: "Установки", path: "/installations", action: "view"}}},
	{"Меню", []navEntry{{label: "Меню загрузки", path: "/menu", action: "view"}}},
	{"Библиотека", []navEntry{{label: "Образы ISO", path: "/images", action: "view"}, {label: "Драйверы", path: "/drivers", action: "view"}, {label: "Файлы ответов", path: "/answers", action: "view"}, {label: "USB-носители", path: "/usb", action: "view"}}},
	{"Журнал", []navEntry{{label: "Журнал", path: "/events", action: "view"}}},
	{"Система", []navEntry{{label: "Обзор", path: "/system", action: "view"}, {label: "Машины", path: "/machines", action: "view"}, {label: "Хранилища", path: "/storages", action: "view"}, {label: "Сеть", path: "/network", action: "view"}, {label: "Пользователи", path: "/users", action: "users"}, {label: "Аудит", path: "/audit", action: "users"}, {label: "Личный кабинет", path: "/account", action: "view"}}},
}

func navMatches(path, prefix string) bool {
	return path == prefix || (prefix != "/" && strings.HasPrefix(path, prefix+"/"))
}

// navigation lists the header sections the role may open, marking the
// section and the page that hold the current path. A section links to its
// first allowed page.
func navigation(role, path string) []navGroup {
	var groups []navGroup
	for _, layout := range navLayout {
		group := navGroup{Title: layout.title}
		for _, entry := range layout.entries {
			if !Allowed(role, entry.action) {
				continue
			}
			active := navMatches(path, entry.path)
			for _, prefix := range entry.also {
				active = active || navMatches(path, prefix)
			}
			group.Items = append(group.Items, navItem{Label: entry.label, Path: entry.path, Active: active})
			group.Active = group.Active || active
		}
		if len(group.Items) > 0 {
			group.Path = group.Items[0].Path
			groups = append(groups, group)
		}
	}
	return groups
}

// subnav returns the pages of the active section when it has several.
func subnav(groups []navGroup) []navItem {
	for _, group := range groups {
		if group.Active && len(group.Items) > 1 {
			return group.Items
		}
	}
	return nil
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

// fontFile serves the embedded interface fonts. The server runs in an
// isolated network, so the admin pages never load fonts from the internet.
func fontFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name != "Geist-Variable.woff2" && name != "GeistMono-Variable.woff2" {
		http.NotFound(w, r)
		return
	}
	data, err := templateFS.ReadFile("templates/fonts/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "font/woff2")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(data)
}
