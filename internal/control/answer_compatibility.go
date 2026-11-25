package control

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"omb/internal/agent"
	"omb/internal/unattend"
)

// WindowsAnswerTarget описывает извлечённые требования Windows answer XML к образу.
type WindowsAnswerTarget struct {
	Architectures   []string `json:"architectures,omitempty"`
	ImageMode       string   `json:"image_mode,omitempty"` // "any" | "edition" | "index" | "name" | "product_key"
	EditionID       string   `json:"edition_id,omitempty"`
	EditionLabel    string   `json:"edition_label,omitempty"`
	ImageIndex      int      `json:"image_index,omitempty"`
	ImageName       string   `json:"image_name,omitempty"`
	ProductKeyMode  string   `json:"product_key_mode,omitempty"` // "firmware" | "custom"
	ImageLanguage   string   `json:"image_language,omitempty"`
	PartitionLayout string   `json:"partition_layout,omitempty"` // "GPT" | "MBR" | "Automatic"
	Win11Only       bool     `json:"win11_only,omitempty"`
	Unparsed        bool     `json:"unparsed,omitempty"`
}

// CompatibilityResult описывает результат проверки совместимости скрипта с образом WIM.
type CompatibilityResult struct {
	Status         string   `json:"status"` // "compatible" | "warning" | "incompatible"
	Errors         []string `json:"errors,omitempty"`
	Warnings       []string `json:"warnings,omitempty"`
	MatchedSummary string   `json:"matched_summary,omitempty"`
}

var unattendStaticData = sync.OnceValues(func() (*unattend.Data, error) {
	return unattend.LoadData()
})

// editionIDAliases сопоставляет идентификаторы редакций конструктора и WIM EditionID.
var editionIDAliases = map[string][]string{
	"home":               {"Core"},
	"home_n":             {"CoreN"},
	"home_single":        {"CoreSingleLanguage"},
	"education":          {"Education"},
	"education_n":        {"EducationN"},
	"pro":                {"Professional"},
	"pro_n":              {"ProfessionalN"},
	"pro_education":      {"ProfessionalEducation"},
	"pro_education_n":    {"ProfessionalEducationN"},
	"pro_workstations":   {"ProfessionalWorkstation"},
	"pro_workstations_n": {"ProfessionalWorkstationN"},
	"enterprise":         {"Enterprise"},
	"enterprise_n":       {"EnterpriseN"},
}

var editionShortLabels = map[string]string{
	"home":               "Home",
	"core":               "Home",
	"home_n":             "Home N",
	"coren":              "Home N",
	"home_single":        "Home Single",
	"coresinglelanguage": "Home Single",
	"education":          "Education",
	"education_n":        "Education N",
	"educationn":         "Education N",
	"pro":                "Pro",
	"professional":       "Pro",
	"pro_n":              "Pro N",
	"professionaln":      "Pro N",
	"pro_education":      "Pro Education",
	"pro_education_n":    "Pro Education N",
	"pro_workstations":   "Pro Workstations",
	"pro_workstations_n": "Pro Workstations N",
	"enterprise":         "Enterprise",
	"enterprises":        "Enterprise LTSC",
	"enterprise_n":       "Enterprise N",
	"enterprisen":        "Enterprise N",
}

// Chips возвращает короткие чипы сводки целевого назначения скрипта для интерфейса.
func (t WindowsAnswerTarget) Chips() []string {
	if t.Unparsed {
		return []string{"XML (не разобран)"}
	}
	var chips []string
	if len(t.Architectures) > 0 {
		chips = append(chips, strings.Join(t.Architectures, ", "))
	} else {
		chips = append(chips, "любая арх.")
	}
	if t.Win11Only {
		chips = append(chips, "Win 11")
	}
	switch t.ImageMode {
	case "edition":
		label := strings.TrimSpace(t.EditionLabel)
		if label == "" {
			label = shortEditionLabel(t.EditionID, "")
		}
		if label != "" {
			chips = append(chips, label)
		}
	case "index":
		if t.ImageIndex > 0 {
			chips = append(chips, fmt.Sprintf("индекс %d", t.ImageIndex))
		}
	case "name":
		if name := strings.TrimSpace(t.ImageName); name != "" {
			chips = append(chips, name)
		}
	case "product_key":
		// Режим выбора по ключу уточняется чипом ключа ниже.
	default:
		if t.ProductKeyMode == "" {
			chips = append(chips, "любая редакция")
		}
	}
	switch t.ProductKeyMode {
	case "firmware":
		chips = append(chips, "ключ BIOS")
	case "custom":
		chips = append(chips, "свой ключ")
	}
	if lang := strings.TrimSpace(t.ImageLanguage); lang != "" {
		chips = append(chips, lang)
	}
	return chips
}

// SummarizeWindowsAnswerTarget извлекает требования к Windows-образу из autounattend XML.
func SummarizeWindowsAnswerTarget(rawXML []byte) WindowsAnswerTarget {
	utf8XML := normalizeUTF16ToUTF8(rawXML)
	if len(bytes.TrimSpace(utf8XML)) == 0 {
		return WindowsAnswerTarget{Unparsed: true}
	}

	target, ok := summarizeXMLFallback(utf8XML)
	if !ok {
		return WindowsAnswerTarget{Unparsed: true}
	}
	target.Win11Only = target.Win11Only || xmlContainsWin11Bypass(utf8XML)

	if data, err := unattendStaticData(); err == nil && data != nil {
		if cfg, report, err := unattend.Open(data, rawXML); err == nil && report.Exact {
			applyUnattendConfig(&target, cfg)
		}
	}
	if target.ImageMode == "" {
		target.ImageMode = "any"
	}
	return target
}

func applyUnattendConfig(target *WindowsAnswerTarget, cfg unattend.Config) {
	if len(cfg.ProcessorArchitectures) > 0 {
		var archs []string
		for _, arch := range cfg.ProcessorArchitectures {
			if norm := normalizeAnswerArch(string(arch)); norm != "" && !containsString(archs, norm) {
				archs = append(archs, norm)
			}
		}
		if len(archs) > 0 {
			target.Architectures = archs
		}
	}
	if ls, ok := cfg.LanguageSettings.(unattend.UnattendedLanguageSettings); ok {
		if lang := strings.TrimSpace(ls.ImageLanguage); lang != "" {
			target.ImageLanguage = lang
		}
	}
	switch pe := cfg.PESettings.(type) {
	case unattend.DefaultPESettings:
		if pe.BypassRequirementsCheck {
			target.Win11Only = true
		}
		applyEditionSettings(target, pe.EditionSettings)
	case unattend.GeneratePESettings:
		if p, ok := pe.PartitionSettings.(unattend.UnattendedPartitionSettings); ok && p.PartitionLayout != "" {
			target.PartitionLayout = string(p.PartitionLayout)
		}
		applyInstallFromSettings(target, pe.InstallFromSettings)
	}
}

func applyEditionSettings(target *WindowsAnswerTarget, es unattend.EditionSettings) {
	switch s := es.(type) {
	case unattend.UnattendedEditionSettings:
		target.ImageMode = "edition"
		target.EditionID = s.Edition
		target.EditionLabel = shortEditionLabel(s.Edition, "")
		target.ProductKeyMode = ""
	case unattend.FirmwareEditionSettings:
		target.ImageMode = "any"
		target.ProductKeyMode = "firmware"
	case unattend.CustomEditionSettings:
		target.ImageMode = "any"
		target.ProductKeyMode = "custom"
	case unattend.InteractiveEditionSettings:
		target.ImageMode = "any"
		target.ProductKeyMode = ""
	}
}

func applyInstallFromSettings(target *WindowsAnswerTarget, ifs unattend.InstallFromSettings) {
	switch s := ifs.(type) {
	case unattend.IndexInstallFromSettings:
		target.ImageMode = "index"
		target.ImageIndex = s.Index
	case unattend.NameInstallFromSettings:
		target.ImageMode = "name"
		target.ImageName = s.Name
	case unattend.EditionInstallFromSettings:
		target.ImageMode = "edition"
		target.EditionID = s.Edition
		target.EditionLabel = shortEditionLabel(s.Edition, "")
	}
}

func summarizeXMLFallback(rawXML []byte) (WindowsAnswerTarget, bool) {
	var target WindowsAnswerTarget
	dec := xml.NewDecoder(bytes.NewReader(rawXML))
	var path []string
	var currentPass string
	var setupArchs []string
	var winPEArchs []string
	var allArchs []string
	var sawRoot bool

	// Временное состояние для текущего MetaData, ProductKey и DiskConfiguration.
	inMetaData := false
	metaKey := ""
	metaVal := ""
	inUserDataProductKey := false
	hasProductKeyElem := false
	hasProductKeyValue := false
	hasEFI := false
	hasActive := false
	inCreatePartition := false
	inModifyPartition := false

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return WindowsAnswerTarget{}, false
		}
		switch v := tok.(type) {
		case xml.StartElement:
			local := v.Name.Local
			path = append(path, local)
			if len(path) == 1 && local == "unattend" {
				sawRoot = true
			}
			if local == "settings" {
				currentPass = attrValue(v.Attr, "pass")
			}
			if local == "component" {
				if arch := normalizeAnswerArch(attrValue(v.Attr, "processorArchitecture")); arch != "" {
					if !containsString(allArchs, arch) {
						allArchs = append(allArchs, arch)
					}
					if strings.EqualFold(currentPass, "windowsPE") && !containsString(winPEArchs, arch) {
						winPEArchs = append(winPEArchs, arch)
					}
					if strings.EqualFold(currentPass, "windowsPE") && strings.EqualFold(attrValue(v.Attr, "name"), "Microsoft-Windows-Setup") && !containsString(setupArchs, arch) {
						setupArchs = append(setupArchs, arch)
					}
				}
			}
			if strings.EqualFold(currentPass, "windowsPE") {
				switch {
				case endsWithPath(path, "InstallFrom", "MetaData"):
					inMetaData = true
					metaKey = ""
					metaVal = ""
				case endsWithPath(path, "UserData", "ProductKey"):
					inUserDataProductKey = true
					hasProductKeyElem = true
				case local == "CreatePartition":
					inCreatePartition = true
				case local == "ModifyPartition":
					inModifyPartition = true
				}
			}
		case xml.EndElement:
			local := v.Name.Local
			if strings.EqualFold(currentPass, "windowsPE") {
				if inMetaData && local == "MetaData" {
					applyMetaData(&target, metaKey, metaVal)
					inMetaData = false
				}
				if inUserDataProductKey && local == "ProductKey" {
					inUserDataProductKey = false
				}
				if local == "CreatePartition" {
					inCreatePartition = false
				}
				if local == "ModifyPartition" {
					inModifyPartition = false
				}
			}
			if local == "settings" {
				currentPass = ""
			}
			if len(path) > 0 {
				path = path[:len(path)-1]
			}
		case xml.CharData:
			text := strings.TrimSpace(string(v))
			if text == "" || !strings.EqualFold(currentPass, "windowsPE") {
				continue
			}
			if inMetaData {
				if endsWithPath(path, "MetaData", "Key") {
					metaKey = text
				} else if endsWithPath(path, "MetaData", "Value") {
					metaVal = text
				}
			}
			if inUserDataProductKey && endsWithPath(path, "ProductKey", "Key") {
				hasProductKeyValue = true
			}
			if endsWithPath(path, "SetupUILanguage", "UILanguage") || (target.ImageLanguage == "" && endsWithPath(path, "component", "UILanguage")) {
				target.ImageLanguage = text
			}
			if inCreatePartition && endsWithPath(path, "CreatePartition", "Type") && strings.EqualFold(text, "EFI") {
				hasEFI = true
			}
			if inModifyPartition && endsWithPath(path, "ModifyPartition", "Active") && strings.EqualFold(text, "true") {
				hasActive = true
			}
		}
	}

	if !sawRoot {
		return WindowsAnswerTarget{}, false
	}
	switch {
	case len(setupArchs) > 0:
		target.Architectures = setupArchs
	case len(winPEArchs) > 0:
		target.Architectures = winPEArchs
	default:
		target.Architectures = allArchs
	}
	if hasProductKeyValue {
		target.ProductKeyMode = "custom"
	} else if hasProductKeyElem {
		target.ProductKeyMode = "firmware"
	}
	if hasEFI {
		target.PartitionLayout = "GPT"
	} else if hasActive {
		target.PartitionLayout = "MBR"
	}
	return target, true
}

func applyMetaData(target *WindowsAnswerTarget, key, value string) {
	val := strings.TrimSpace(value)
	if val == "" {
		return
	}
	switch strings.ToUpper(strings.TrimSpace(key)) {
	case "/IMAGE/INDEX":
		if idx, err := strconv.Atoi(val); err == nil && idx > 0 {
			target.ImageMode = "index"
			target.ImageIndex = idx
		}
	case "/IMAGE/NAME":
		target.ImageMode = "name"
		target.ImageName = val
	case "/IMAGE/EDITIONID":
		target.ImageMode = "edition"
		target.EditionID = val
		target.EditionLabel = shortEditionLabel(val, "")
	}
}

// EvaluateWindowsAnswerCompatibility проверяет совместимость требований скрипта с метаданными install.wim/esd.
func EvaluateWindowsAnswerCompatibility(target WindowsAnswerTarget, wim agent.WindowsWIMInfo) CompatibilityResult {
	res := CompatibilityResult{Status: "compatible"}
	if strings.EqualFold(target.PartitionLayout, "MBR") {
		res.Warnings = append(res.Warnings, "В скрипте выбрана разметка MBR; при сетевой загрузке в режиме UEFI установка завершится ошибкой")
	}
	if !wim.Known || len(wim.Images) == 0 {
		res.MatchedSummary = "Индексы WIM не просканированы"
		if len(res.Warnings) > 0 {
			res.Status = "warning"
		}
		return res
	}

	candidates := wim.Images
	if len(target.Architectures) > 0 {
		var archMatched []agent.WindowsWIMImage
		for _, img := range candidates {
			if containsFold(target.Architectures, img.Architecture) {
				archMatched = append(archMatched, img)
			}
		}
		if len(archMatched) == 0 {
			res.Status = "incompatible"
			res.Errors = append(res.Errors, fmt.Sprintf(
				"Скрипт рассчитан на %s, а в образе — %s",
				strings.Join(target.Architectures, ", "),
				joinUniqueImageArchs(wim.Images),
			))
			return res
		}
		candidates = archMatched
	}

	var matched []agent.WindowsWIMImage
	switch target.ImageMode {
	case "index":
		for _, img := range candidates {
			if img.Index == target.ImageIndex {
				matched = append(matched, img)
			}
		}
		if len(matched) == 0 {
			res.Status = "incompatible"
			res.Errors = append(res.Errors, fmt.Sprintf("В образе нет индекса %d (всего редакций: %d)", target.ImageIndex, len(wim.Images)))
			return res
		}
	case "name":
		want := strings.TrimSpace(target.ImageName)
		for _, img := range candidates {
			if strings.EqualFold(strings.TrimSpace(img.Name), want) || strings.EqualFold(strings.TrimSpace(img.DisplayName), want) {
				matched = append(matched, img)
			}
		}
		if len(matched) == 0 {
			res.Status = "incompatible"
			res.Errors = append(res.Errors, fmt.Sprintf("В образе нет редакции «%s»", target.ImageName))
			return res
		}
	case "edition":
		for _, img := range candidates {
			if editionMatches(target.EditionID, img.EditionID) {
				matched = append(matched, img)
			}
		}
		if len(matched) == 0 {
			label := target.EditionLabel
			if label == "" {
				label = target.EditionID
			}
			res.Status = "incompatible"
			res.Errors = append(res.Errors, fmt.Sprintf(
				"В образе нет редакции %s (доступно: %s)",
				label,
				joinUniqueEditionIDs(candidates),
			))
			return res
		}
	default:
		matched = candidates
	}

	if target.Win11Only && allKnownBuildsBelowWin11(matched) {
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"Скрипт содержит обход требований Windows 11 (TPM/Secure Boot/RAM), а выбранный образ имеет build %d (Windows 10)",
			firstKnownBuild(matched),
		))
	}
	if lang := strings.TrimSpace(target.ImageLanguage); lang != "" && hasLanguageMetadata(matched) && !anyImageHasLanguage(matched, lang) {
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"Язык скрипта %s не найден в образе (доступны: %s)",
			lang,
			joinUniqueLanguages(matched),
		))
	}

	res.MatchedSummary = formatMatchedSummary(matched)
	if len(res.Warnings) > 0 {
		res.Status = "warning"
	}
	return res
}

func editionMatches(targetEdition, imgEdition string) bool {
	want := strings.TrimSpace(targetEdition)
	have := strings.TrimSpace(imgEdition)
	if want == "" || have == "" {
		return false
	}
	if strings.EqualFold(want, have) {
		return true
	}
	for _, alias := range editionIDAliases[strings.ToLower(want)] {
		if strings.EqualFold(alias, have) {
			return true
		}
	}
	return false
}

func allKnownBuildsBelowWin11(images []agent.WindowsWIMImage) bool {
	known := 0
	for _, img := range images {
		if img.Build <= 0 {
			continue
		}
		known++
		if img.Build >= 22000 {
			return false
		}
	}
	return known > 0
}

func firstKnownBuild(images []agent.WindowsWIMImage) int {
	for _, img := range images {
		if img.Build > 0 {
			return img.Build
		}
	}
	return 0
}

func hasLanguageMetadata(images []agent.WindowsWIMImage) bool {
	for _, img := range images {
		if len(img.Languages) > 0 {
			return true
		}
	}
	return false
}

func anyImageHasLanguage(images []agent.WindowsWIMImage, targetLang string) bool {
	for _, img := range images {
		if containsFold(img.Languages, targetLang) || strings.EqualFold(strings.TrimSpace(img.DefaultLanguage), targetLang) {
			return true
		}
	}
	return false
}

func formatMatchedSummary(matched []agent.WindowsWIMImage) string {
	if len(matched) == 1 {
		img := matched[0]
		name := strings.TrimSpace(img.DisplayName)
		if name == "" {
			name = strings.TrimSpace(img.Name)
		}
		if name == "" {
			name = img.EditionID
		}
		return fmt.Sprintf("Индекс %d · %s · %s", img.Index, name, img.Architecture)
	}
	return fmt.Sprintf("Подходит редакций в образе: %d", len(matched))
}

func shortEditionLabel(id, displayName string) string {
	if label, ok := editionShortLabels[strings.ToLower(strings.TrimSpace(id))]; ok {
		return label
	}
	if strings.TrimSpace(displayName) != "" {
		return strings.TrimSpace(strings.TrimPrefix(displayName, "Windows 11 "))
	}
	return strings.TrimSpace(id)
}

func xmlContainsWin11Bypass(raw []byte) bool {
	lower := strings.ToLower(string(raw))
	for _, marker := range []string{"bypasstpmcheck", "bypasssecurebootcheck", "bypassramcheck", "labconfig"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func normalizeAnswerArch(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "amd64", "x64", "x86_64":
		return "amd64"
	case "arm64", "aarch64":
		return "arm64"
	case "x86", "i386", "i686":
		return "x86"
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

func normalizeUTF16ToUTF8(data []byte) []byte {
	if len(data) < 2 {
		return data
	}
	var big bool
	switch {
	case data[0] == 0xFF && data[1] == 0xFE:
		big = false
	case data[0] == 0xFE && data[1] == 0xFF:
		big = true
	default:
		return bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	}
	body := data[2:]
	if len(body)%2 != 0 {
		return data
	}
	var b strings.Builder
	for i := 0; i < len(body); i += 2 {
		var r rune
		if big {
			r = rune(uint16(body[i])<<8 | uint16(body[i+1]))
		} else {
			r = rune(uint16(body[i+1])<<8 | uint16(body[i]))
		}
		if r == 0 {
			continue
		}
		b.WriteRune(r)
	}
	return []byte(b.String())
}

func attrValue(attrs []xml.Attr, name string) string {
	for _, attr := range attrs {
		if strings.EqualFold(attr.Name.Local, name) {
			return attr.Value
		}
	}
	return ""
}

func endsWithPath(path []string, suffix ...string) bool {
	if len(path) < len(suffix) {
		return false
	}
	offset := len(path) - len(suffix)
	for i, s := range suffix {
		if path[offset+i] != s {
			return false
		}
	}
	return true
}

func containsString(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}

func containsFold(items []string, needle string) bool {
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(needle)) {
			return true
		}
	}
	return false
}

func joinUniqueImageArchs(images []agent.WindowsWIMImage) string {
	var seen []string
	for _, img := range images {
		if img.Architecture != "" && !containsString(seen, img.Architecture) {
			seen = append(seen, img.Architecture)
		}
	}
	if len(seen) == 0 {
		return "неизвестная архитектура"
	}
	return strings.Join(seen, ", ")
}

func joinUniqueEditionIDs(images []agent.WindowsWIMImage) string {
	var seen []string
	for _, img := range images {
		label := strings.TrimSpace(img.EditionID)
		if label == "" {
			label = strings.TrimSpace(img.Name)
		}
		if label != "" && !containsString(seen, label) {
			seen = append(seen, label)
		}
	}
	if len(seen) == 0 {
		return "—"
	}
	return strings.Join(seen, ", ")
}

func joinUniqueLanguages(images []agent.WindowsWIMImage) string {
	var seen []string
	for _, img := range images {
		for _, lang := range img.Languages {
			if lang != "" && !containsString(seen, lang) {
				seen = append(seen, lang)
			}
		}
	}
	if len(seen) == 0 {
		return "—"
	}
	return strings.Join(seen, ", ")
}
