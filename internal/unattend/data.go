package unattend

import (
	"bytes"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

//go:embed resource
var resources embed.FS

// resourceString reads a resource as Util.StringFromResource does: UTF-8,
// without a BOM.
func resourceString(name string) string {
	data, err := resources.ReadFile("resource/" + name)
	if err != nil {
		panic(fmt.Sprintf("Resource '%s' not found.", name))
	}
	return string(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")))
}

func resourceXML(name string) *Document {
	return mustParseXML([]byte(resourceString(name)))
}

// Bloatware is an app or feature that can be removed.
type Bloatware struct {
	ID          string
	DisplayName string
	Steps       []BloatwareStep
}

// BloatwareStep is one way to remove a bloatware: a package, capability or
// optional feature by selector, or custom code in the generator.
type BloatwareStep struct {
	Kind      string // Package, Capability, OptionalFeature, Custom
	Selector  string
	AppliesTo []string
}

// Component is a Windows Setup component and the passes it can appear in.
type Component struct {
	ID     string
	Passes []string
}

// WindowsEdition is an edition with its generic product key.
type WindowsEdition struct {
	ID, DisplayName, ProductKey string
	Visible                     bool
}

// ImageLanguage is a language of the Windows image.
type ImageLanguage struct{ ID, DisplayName string }

// KeyboardIdentifier is a keyboard layout or IME.
type KeyboardIdentifier struct {
	ID, DisplayName string
	Type            string // Keyboard or IME
}

// UserLocale is a locale with its default keyboard and location.
type UserLocale struct {
	ID, DisplayName, LCID string
	KeyboardLayout       *KeyboardIdentifier
	GeoLocation          *GeoLocation
}

// TimeOffset is a Windows time zone.
type TimeOffset struct{ ID, DisplayName string }

// DesktopIcon is an icon that can be shown on the desktop.
type DesktopIcon struct{ ID, DisplayName, GUID string }

// StartFolder is a folder that can be pinned next to the power button.
type StartFolder struct {
	ID, DisplayName string
	Bytes           []byte
}

// GeoLocation is a Windows GeoID.
type GeoLocation struct{ ID, DisplayName string }

// Data holds the lookup tables of the generator, keyed case-insensitively.
type Data struct {
	Bloatwares          map[string]*Bloatware
	Components          map[string]*Component
	ImageLanguages      map[string]*ImageLanguage
	KeyboardIdentifiers map[string]*KeyboardIdentifier
	GeoLocations        map[string]*GeoLocation
	UserLocales         map[string]*UserLocale
	WindowsEditions     map[string]*WindowsEdition
	TimeOffsets         map[string]*TimeOffset
	DesktopIcons        map[string]*DesktopIcon
	StartFolders        map[string]*StartFolder
	// Ordered lists, as in the resource files, for the editor.
	BloatwareList, ComponentList, ImageLanguageList, KeyboardList, GeoList, LocaleList, EditionList, TimeOffsetList, DesktopIconList, StartFolderList []string
}

var (
	dataOnce   sync.Once
	sharedData *Data
	dataErr    error
)

// LoadData reads the lookup tables once.
func LoadData() (*Data, error) {
	dataOnce.Do(func() { sharedData, dataErr = loadData() })
	return sharedData, dataErr
}

func key(id string) string { return strings.ToLower(id) }

func loadJSON(name string, v any) error {
	if err := json.Unmarshal([]byte(resourceString(name)), v); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func loadData() (*Data, error) {
	d := &Data{
		Bloatwares: map[string]*Bloatware{}, Components: map[string]*Component{}, ImageLanguages: map[string]*ImageLanguage{},
		KeyboardIdentifiers: map[string]*KeyboardIdentifier{}, GeoLocations: map[string]*GeoLocation{}, UserLocales: map[string]*UserLocale{},
		WindowsEditions: map[string]*WindowsEdition{}, TimeOffsets: map[string]*TimeOffset{}, DesktopIcons: map[string]*DesktopIcon{}, StartFolders: map[string]*StartFolder{},
	}
	unique := func(kind, id string, seen map[string]bool) error {
		if seen[key(id)] {
			return fmt.Errorf("%s: '%s' occurs more than once", kind, id)
		}
		seen[key(id)] = true
		return nil
	}

	var bloat []struct {
		DisplayName string
		Token       *string
		Steps       []struct {
			Type      string `json:"$type"`
			Selector  string
			AppliesTo []string
		}
	}
	if err := loadJSON("Bloatware.json", &bloat); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, b := range bloat {
		id := "Remove" + strings.ReplaceAll(b.DisplayName, " ", "")
		if b.Token != nil {
			id = "Remove" + *b.Token
		}
		x := &Bloatware{ID: id, DisplayName: b.DisplayName}
		for _, s := range b.Steps {
			name, _, _ := strings.Cut(strings.TrimPrefix(s.Type, "Schneegans.Unattend."), ",")
			kind := strings.TrimSuffix(name, "BloatwareStep")
			switch kind {
			case "Package", "Capability", "OptionalFeature", "Custom":
			default:
				return nil, fmt.Errorf("Bloatware.json: unknown step %q", s.Type)
			}
			x.Steps = append(x.Steps, BloatwareStep{Kind: kind, Selector: s.Selector, AppliesTo: sortedStrings(s.AppliesTo)})
		}
		if err := unique("Bloatware", id, seen); err != nil {
			return nil, err
		}
		d.Bloatwares[key(id)] = x
		d.BloatwareList = append(d.BloatwareList, id)
	}

	var comps []Component
	if err := loadJSON("Component.json", &comps); err != nil {
		return nil, err
	}
	seen = map[string]bool{}
	for i := range comps {
		c := &comps[i]
		for _, r := range c.ID {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-') {
				return nil, fmt.Errorf("Component.json: ID '%s' contains illegal characters", c.ID)
			}
		}
		c.Passes = sortPasses(c.Passes)
		if err := unique("Component", c.ID, seen); err != nil {
			return nil, err
		}
		d.Components[key(c.ID)] = c
		d.ComponentList = append(d.ComponentList, c.ID)
	}

	var langs []ImageLanguage
	if err := loadJSON("ImageLanguage.json", &langs); err != nil {
		return nil, err
	}
	seen = map[string]bool{}
	for i := range langs {
		if err := unique("ImageLanguage", langs[i].ID, seen); err != nil {
			return nil, err
		}
		d.ImageLanguages[key(langs[i].ID)] = &langs[i]
		d.ImageLanguageList = append(d.ImageLanguageList, langs[i].ID)
	}

	var keyboards []KeyboardIdentifier
	if err := loadJSON("KeyboardIdentifier.json", &keyboards); err != nil {
		return nil, err
	}
	seen = map[string]bool{}
	for i := range keyboards {
		if err := unique("KeyboardIdentifier", keyboards[i].ID, seen); err != nil {
			return nil, err
		}
		d.KeyboardIdentifiers[key(keyboards[i].ID)] = &keyboards[i]
		d.KeyboardList = append(d.KeyboardList, keyboards[i].ID)
	}

	var geos []GeoLocation
	if err := loadJSON("GeoId.json", &geos); err != nil {
		return nil, err
	}
	seen = map[string]bool{}
	for i := range geos {
		if err := unique("GeoLocation", geos[i].ID, seen); err != nil {
			return nil, err
		}
		d.GeoLocations[key(geos[i].ID)] = &geos[i]
		d.GeoList = append(d.GeoList, geos[i].ID)
	}

	var locales []struct {
		ID, DisplayName, LCID string
		KeyboardLayout       *string
		GeoLocation          *string
	}
	if err := loadJSON("UserLocale.json", &locales); err != nil {
		return nil, err
	}
	seen = map[string]bool{}
	for _, l := range locales {
		x := &UserLocale{ID: l.ID, DisplayName: l.DisplayName, LCID: l.LCID}
		if l.KeyboardLayout != nil {
			k, ok := d.KeyboardIdentifiers[key(*l.KeyboardLayout)]
			if !ok {
				return nil, fmt.Errorf("UserLocale.json: %s: unknown keyboard %s", l.ID, *l.KeyboardLayout)
			}
			x.KeyboardLayout = k
		}
		if l.GeoLocation != nil {
			g, ok := d.GeoLocations[key(*l.GeoLocation)]
			if !ok {
				return nil, fmt.Errorf("UserLocale.json: %s: unknown location %s", l.ID, *l.GeoLocation)
			}
			x.GeoLocation = g
		}
		if err := unique("UserLocale", l.ID, seen); err != nil {
			return nil, err
		}
		d.UserLocales[key(l.ID)] = x
		d.LocaleList = append(d.LocaleList, l.ID)
	}

	var editions []WindowsEdition
	if err := loadJSON("WindowsEdition.json", &editions); err != nil {
		return nil, err
	}
	seen = map[string]bool{}
	for i := range editions {
		if err := unique("WindowsEdition", editions[i].ID, seen); err != nil {
			return nil, err
		}
		d.WindowsEditions[key(editions[i].ID)] = &editions[i]
		d.EditionList = append(d.EditionList, editions[i].ID)
	}

	var offsets []TimeOffset
	if err := loadJSON("TimeOffset.json", &offsets); err != nil {
		return nil, err
	}
	seen = map[string]bool{}
	for i := range offsets {
		if err := unique("TimeOffset", offsets[i].ID, seen); err != nil {
			return nil, err
		}
		d.TimeOffsets[key(offsets[i].ID)] = &offsets[i]
		d.TimeOffsetList = append(d.TimeOffsetList, offsets[i].ID)
	}

	var icons []struct{ ID, DisplayName, GUID string }
	if err := loadJSON("DesktopIcon.json", &icons); err != nil {
		return nil, err
	}
	seen = map[string]bool{}
	for _, ic := range icons {
		if err := unique("DesktopIcon", ic.ID, seen); err != nil {
			return nil, err
		}
		d.DesktopIcons[key(ic.ID)] = &DesktopIcon{ID: ic.ID, DisplayName: ic.DisplayName, GUID: ic.GUID}
		d.DesktopIconList = append(d.DesktopIconList, ic.ID)
	}

	var folders []struct{ DisplayName, Bytes string }
	if err := loadJSON("StartFolder.json", &folders); err != nil {
		return nil, err
	}
	seen = map[string]bool{}
	for _, f := range folders {
		raw, err := base64.StdEncoding.DecodeString(f.Bytes)
		if err != nil {
			return nil, fmt.Errorf("StartFolder.json: %s: %w", f.DisplayName, err)
		}
		id := strings.ReplaceAll(f.DisplayName, " ", "")
		if err := unique("StartFolder", id, seen); err != nil {
			return nil, err
		}
		d.StartFolders[key(id)] = &StartFolder{ID: id, DisplayName: f.DisplayName, Bytes: raw}
		d.StartFolderList = append(d.StartFolderList, id)
	}
	return d, nil
}

// lookupError is the message of UnattendGenerator.Lookup for a missing key.
// The original prints the type parameter's name, which is always "T".
func lookupError(id string) error {
	return &ConfigurationError{Message: fmt.Sprintf("Could not find an element of type 'T' with key '%s'.", id)}
}

// The lookups used by the modifiers.

func (d *Data) Bloatware(id string) (*Bloatware, error) {
	if x, ok := d.Bloatwares[key(id)]; ok {
		return x, nil
	}
	return nil, lookupError(id)
}

func (d *Data) Edition(id string) (*WindowsEdition, error) {
	if x, ok := d.WindowsEditions[key(id)]; ok {
		return x, nil
	}
	return nil, lookupError(id)
}

func (d *Data) ImageLanguage(id string) (*ImageLanguage, error) {
	if x, ok := d.ImageLanguages[key(id)]; ok {
		return x, nil
	}
	return nil, lookupError(id)
}

func (d *Data) Keyboard(id string) (*KeyboardIdentifier, error) {
	if x, ok := d.KeyboardIdentifiers[key(id)]; ok {
		return x, nil
	}
	return nil, lookupError(id)
}

func (d *Data) Locale(id string) (*UserLocale, error) {
	if x, ok := d.UserLocales[key(id)]; ok {
		return x, nil
	}
	return nil, lookupError(id)
}

func (d *Data) Geo(id string) (*GeoLocation, error) {
	if x, ok := d.GeoLocations[key(id)]; ok {
		return x, nil
	}
	return nil, lookupError(id)
}

func (d *Data) TimeOffset(id string) (*TimeOffset, error) {
	if x, ok := d.TimeOffsets[key(id)]; ok {
		return x, nil
	}
	return nil, lookupError(id)
}

func (d *Data) DesktopIcon(id string) (*DesktopIcon, error) {
	if x, ok := d.DesktopIcons[key(id)]; ok {
		return x, nil
	}
	return nil, lookupError(id)
}

func (d *Data) StartFolder(id string) (*StartFolder, error) {
	if x, ok := d.StartFolders[key(id)]; ok {
		return x, nil
	}
	return nil, lookupError(id)
}

func (d *Data) Component(id string) (*Component, error) {
	if x, ok := d.Components[key(id)]; ok {
		return x, nil
	}
	return nil, lookupError(id)
}

// passOrder is the order of the Pass enum.
var passOrder = []string{"offlineServicing", "windowsPE", "generalize", "specialize", "auditSystem", "auditUser", "oobeSystem"}

func passIndex(p string) int {
	for i, x := range passOrder {
		if x == p {
			return i
		}
	}
	return -1
}

func sortPasses(in []string) []string {
	out := append([]string(nil), in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && passIndex(out[j]) < passIndex(out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// sortedStrings orders strings as ImmutableSortedSet<string> does with the
// default comparer.
func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && compareCulture(out[j], out[j-1]) < 0; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	// A sorted set keeps one of equal strings.
	var uniq []string
	for _, s := range out {
		if len(uniq) == 0 || compareCulture(uniq[len(uniq)-1], s) != 0 {
			uniq = append(uniq, s)
		}
	}
	return uniq
}
