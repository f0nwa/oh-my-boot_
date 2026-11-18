package control

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// A MAC prefix rule names a batch of machines or a network card maker by the
// first 3–5 bytes of the MAC: «Стенд Proxmox» for bc:24:11, «Ноутбуки
// бухгалтерии» for a laptop series. A machine without a model is labelled by
// the longest rule that fits it; a rule's maker replaces the IEEE registry
// entry for its network card.

// MacRule is one prefix rule.
type MacRule struct {
	NetworkDriver string
	Prefix        string // aa:bb:cc … aa:bb:cc:dd:ee
	Title         string
	Manufacturer  string // the network card's maker, instead of the IEEE entry
	Note          string
	Machines      int // machines in the list that fit, for the page
}

// MacRuleBook stores prefix rules. It is optional, like MachineBook.
type MacRuleBook interface {
	MacRules(context.Context) ([]MacRule, error)
	AddMacRule(context.Context, MacRule) (bool, error)
	EditMacRule(context.Context, MacRule) error
	DeleteMacRule(context.Context, string) error
}

// Link opens the machine list narrowed to this rule.
func (r MacRule) Link() string { return "/machines?rule=" + url.QueryEscape(r.Prefix) }

type macRules []MacRule

// match returns the longest rule whose prefix starts the MAC.
func (rs macRules) match(mac string) *MacRule {
	var best *MacRule
	for i := range rs {
		if strings.HasPrefix(mac, rs[i].Prefix+":") && (best == nil || len(rs[i].Prefix) > len(best.Prefix)) {
			best = &rs[i]
		}
	}
	return best
}

// find returns the rule with exactly this prefix.
func (rs macRules) find(prefix string) *MacRule {
	for i := range rs {
		if rs[i].Prefix == prefix {
			return &rs[i]
		}
	}
	return nil
}

// macRules reads the rules, sorted by prefix; none when the store keeps none.
func (s *Server) macRules(ctx context.Context) macRules {
	book, ok := s.Store.(MacRuleBook)
	if !ok {
		return nil
	}
	rules, err := book.MacRules(ctx)
	if err != nil {
		return nil
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].Prefix < rules[j].Prefix })
	return rules
}

// attachRules gives each machine the rule that fits it.
func attachRules(machines []MachineInfo, rules macRules) {
	for i := range machines {
		machines[i].Rule = rules.match(machines[i].MAC)
	}
}

// parseMACPrefix reads the first 3, 4 or 5 bytes of a MAC typed in any common
// form. A whole MAC is not a prefix; a multicast first byte is not a card.
func parseMACPrefix(v string) (string, bool) {
	var digits strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(v)) {
		switch {
		case r >= '0' && r <= '9' || r >= 'a' && r <= 'f':
			digits.WriteRune(r)
		case r == ':' || r == '-' || r == '.' || r == ' ':
		default:
			return "", false
		}
	}
	d := digits.String()
	if n := len(d); n != 6 && n != 8 && n != 10 {
		return "", false
	}
	if strings.IndexByte("0123456789abcdef", d[1])&1 == 1 {
		return "", false
	}
	parts := make([]string, len(d)/2)
	for i := range parts {
		parts[i] = d[2*i : 2*i+2]
	}
	return strings.Join(parts, ":"), true
}

// parseMacRule reads the title, maker and note of a rule form.
func parseMacRule(r *http.Request) (MacRule, string) {
	var rule MacRule
	rule.NetworkDriver = r.PostFormValue("network_driver")
	if rule.NetworkDriver != "" && rule.NetworkDriver != "ipxe" && rule.NetworkDriver != "snp" {
		return rule, "Недопустимый сетевой драйвер"
	}
	var ok [3]bool
	rule.Title, ok[0] = cleanMachineText(r.PostFormValue("title"), 80)
	rule.Manufacturer, ok[1] = cleanMachineText(r.PostFormValue("manufacturer"), 120)
	rule.Note, ok[2] = cleanMachineText(r.PostFormValue("note"), 500)
	switch {
	case !ok[0] || !ok[1] || !ok[2]:
		return rule, "Слишком длинный текст или недопустимые символы"
	case rule.Title == "":
		return rule, "Укажите название правила"
	}
	return rule, ""
}

func rulesReturn(message, prefix string) string {
	out := "/machines?message=" + url.QueryEscape(message)
	if prefix != "" {
		out += "#rule-" + prefix
	}
	return out
}

// macRuleAdd adds a prefix rule.
func (s *Server) macRuleAdd(w http.ResponseWriter, r *http.Request) {
	book, ok := s.Store.(MacRuleBook)
	if !ok {
		http.Error(w, "Правила по префиксу недоступны", http.StatusServiceUnavailable)
		return
	}
	prefix, valid := parseMACPrefix(r.PostFormValue("prefix"))
	if !valid {
		http.Error(w, "Неверный префикс: нужны первые 3, 4 или 5 байт MAC, например bc:24:11", http.StatusBadRequest)
		return
	}
	rule, problem := parseMacRule(r)
	if problem != "" {
		http.Error(w, problem, http.StatusBadRequest)
		return
	}
	rule.Prefix = prefix
	if !validNetworkDriverSelection(prefix, rule.NetworkDriver) {
		http.Error(w, "Недопустимый сетевой драйвер", http.StatusBadRequest)
		return
	}
	var added bool
	saved, err := s.mutateBootDrivers(r.Context(), func() error {
		var err error
		added, err = book.AddMacRule(r.Context(), rule)
		if err == nil && !added {
			return errMacRuleExists
		}
		return err
	})
	result, message := driverMutationResult(saved, err, fmt.Sprintf("Правило для префикса %s добавлено", prefix))
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "mac_rule.add", result, prefix)
	if errors.Is(err, errMacRuleExists) {
		http.Error(w, "Правило для этого префикса уже есть", http.StatusConflict)
		return
	}
	if !saved {
		http.Error(w, "Не удалось сохранить правило", http.StatusServiceUnavailable)
		return
	}
	if !added {
		http.Error(w, "Правило для этого префикса уже есть", http.StatusConflict)
		return
	}
	http.Redirect(w, r, rulesReturnContext(r, message, prefix), http.StatusSeeOther)
}

// macRuleEdit changes a rule's title, maker and note; the prefix stays.
func (s *Server) macRuleEdit(w http.ResponseWriter, r *http.Request) {
	book, ok := s.Store.(MacRuleBook)
	prefix, valid := parseMACPrefix(r.PathValue("prefix"))
	if !ok || !valid || prefix != strings.ToLower(r.PathValue("prefix")) {
		http.Error(w, "Неверный префикс", http.StatusBadRequest)
		return
	}
	rule, problem := parseMacRule(r)
	if problem != "" {
		http.Error(w, problem, http.StatusBadRequest)
		return
	}
	rule.Prefix = prefix
	if !validNetworkDriverSelection(prefix, rule.NetworkDriver) {
		http.Error(w, "Недопустимый сетевой драйвер", http.StatusBadRequest)
		return
	}
	saved, err := s.mutateBootDrivers(r.Context(), func() error { return book.EditMacRule(r.Context(), rule) })
	result, message := driverMutationResult(saved, err, fmt.Sprintf("Правило для префикса %s сохранено", prefix))
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "mac_rule.edit", result, prefix)
	if !saved {
		http.Error(w, "Не удалось сохранить правило", http.StatusServiceUnavailable)
		return
	}
	http.Redirect(w, r, rulesReturnContext(r, message, prefix), http.StatusSeeOther)
}

// macRuleDelete removes a rule; machines go back to the IEEE name.
func (s *Server) macRuleDelete(w http.ResponseWriter, r *http.Request) {
	book, ok := s.Store.(MacRuleBook)
	prefix, valid := parseMACPrefix(r.PathValue("prefix"))
	if !ok || !valid || prefix != strings.ToLower(r.PathValue("prefix")) {
		http.Error(w, "Неверный префикс", http.StatusBadRequest)
		return
	}
	saved, err := s.mutateBootDrivers(r.Context(), func() error { return book.DeleteMacRule(r.Context(), prefix) })
	result, message := driverMutationResult(saved, err, fmt.Sprintf("Правило для префикса %s удалено", prefix))
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "mac_rule.delete", result, prefix)
	if !saved {
		http.Error(w, "Не удалось удалить правило", http.StatusServiceUnavailable)
		return
	}
	http.Redirect(w, r, rulesReturnContext(r, message, ""), http.StatusSeeOther)
}

var errMacRuleExists = errors.New("MAC prefix rule already exists")

func rulesReturnContext(r *http.Request, message, prefix string) string {
	target, _ := url.Parse(rulesReturn(message, prefix))
	q := target.Query()
	for _, key := range []string{"q", "sort"} {
		if value := r.PostFormValue(key); value != "" {
			q.Set(key, value)
		}
	}
	target.RawQuery = q.Encode()
	return target.String()
}
