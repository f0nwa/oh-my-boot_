package control

import (
	_ "embed"
	"net/http"
	"strconv"
	"strings"
)

// editDraft applies change to the current draft under the draft lock, saves it
// and returns to the menu page.
func (s *Server) editDraft(w http.ResponseWriter, r *http.Request, action, message string, change func(*MenuDraft) error) {
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	draft, _, err := s.currentDraft(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать черновик меню", http.StatusServiceUnavailable)
		return
	}
	if err := change(&draft); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.Store.SaveMenuDraft(r.Context(), draft); err != nil {
		http.Error(w, "Не удалось сохранить черновик меню", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, action, "ok", r.PathValue("key"))
	target := "/menu?message=" + strings.ReplaceAll(message, " ", "+")
	if r.FormValue("return") == "/installations" {
		target = "/installations?message=" + strings.ReplaceAll(message, " ", "+")
	} else if key := r.PathValue("key"); menuItemKey.MatchString(key) && action != "menu.draft.remove" {
		// Keep the edited item selected on the menu page.
		target += "&item=" + key
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

type draftError string

func (e draftError) Error() string { return string(e) }

const errItemNotInMenu = draftError("Пункт не найден в меню")

func draftItemIndex(d *MenuDraft, key string) int {
	for i, item := range d.Items {
		if item.Key == key {
			return i
		}
	}
	return -1
}

func (s *Server) menuItemMove(w http.ResponseWriter, r *http.Request) {
	s.editDraft(w, r, "menu.draft.move", "Порядок изменён", func(d *MenuDraft) error {
		i := draftItemIndex(d, r.PathValue("key"))
		if i < 0 {
			return errItemNotInMenu
		}
		j := i + 1
		if r.FormValue("dir") == "up" {
			j = i - 1
		}
		if j >= 0 && j < len(d.Items) {
			d.Items[i], d.Items[j] = d.Items[j], d.Items[i]
		}
		return nil
	})
}

func (s *Server) menuItemRemove(w http.ResponseWriter, r *http.Request) {
	s.editDraft(w, r, "menu.draft.remove", "Пункт убран из меню", func(d *MenuDraft) error {
		i := draftItemIndex(d, r.PathValue("key"))
		if i < 0 {
			return errItemNotInMenu
		}
		d.Items = append(d.Items[:i], d.Items[i+1:]...)
		return nil
	})
}

func (s *Server) menuItemRename(w http.ResponseWriter, r *http.Request) {
	s.editDraft(w, r, "menu.draft.title", "Пункт изменён", func(d *MenuDraft) error {
		i := draftItemIndex(d, r.PathValue("key"))
		if i < 0 {
			return errItemNotInMenu
		}
		title, folder := strings.TrimSpace(r.FormValue("title")), strings.TrimSpace(r.FormValue("folder"))
		if title != "" && (!safeMenuText(title, 80) || !isASCII(title)) {
			return draftError("Название в меню — только латиница, цифры и простые знаки, до 80 символов")
		}
		if validateMenuFolder(folder) != nil || (!strings.HasPrefix(d.Items[i].Key, "winiso_") && !isASCII(folder)) {
			return draftError("Недопустимый каталог в меню")
		}
		d.Items[i].Title, d.Items[i].Folder = title, folder
		return nil
	})
}

func (s *Server) menuSettings(w http.ResponseWriter, r *http.Request) {
	s.editDraft(w, r, "menu.draft.settings", "Настройки меню сохранены", func(d *MenuDraft) error {
		// The menu title is the product name; a "title" field is ignored.
		label, message := r.FormValue("label"), r.FormValue("message")
		if !safeMenuText(label, 80) || !safeMenuText(message, 120) {
			return draftError("Недопустимый текст меню")
		}
		folder := r.FormValue("folder_ubuntu")
		if validateMenuFolder(folder) != nil {
			return draftError("Недопустимый каталог в меню")
		}
		timerOn := r.FormValue("local_timer") == "on"
		seconds, err := strconv.Atoi(strings.TrimSpace(r.FormValue("local_timeout")))
		if timerOn && (err != nil || seconds < 1 || seconds > maxLocalTimeout) {
			return draftError("Время до загрузки с диска — от 1 до 3600 секунд")
		}
		if err == nil && seconds >= 1 && seconds <= maxLocalTimeout {
			d.LocalTimeout = seconds
		}
		d.NoLocalTimer = !timerOn
		d.Title, d.Label, d.Message, d.Ubuntu, d.UbuntuFolder = productMenuTitle, label, message, r.FormValue("ubuntu") == "on", folder
		return nil
	})
}

// menuOrder sets the draft's item order from the keys sent in their new order.
// The keys must be exactly the draft's items.
func (s *Server) menuOrder(w http.ResponseWriter, r *http.Request) {
	s.editDraft(w, r, "menu.draft.order", "Порядок изменён", func(d *MenuDraft) error {
		keys := r.Form["key"]
		if len(keys) != len(d.Items) {
			return draftError("Порядок не совпадает с пунктами меню")
		}
		byKey := map[string]MenuDraftItem{}
		for _, item := range d.Items {
			byKey[item.Key] = item
		}
		ordered := make([]MenuDraftItem, 0, len(keys))
		for _, key := range keys {
			item, ok := byKey[key]
			if !ok {
				return draftError("Порядок не совпадает с пунктами меню")
			}
			delete(byKey, key)
			ordered = append(ordered, item)
		}
		d.Items = ordered
		return nil
	})
}

//go:embed menu_order.js
var menuOrderJS []byte

func menuOrderScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(menuOrderJS)
}
