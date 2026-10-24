package unattend

import (
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// HTML form encoding of Config, for the editor. A field's name is its path
// of Go field names joined by dots:
//
//	ComputerNameSettings.$type                 the chosen form of a setting
//	ComputerNameSettings.CustomComputerNameSettings.Name
//	AccountSettings.UnattendedAccountSettings.Accounts.0.Name   a list row
//	Bloatwares                                  a set: one value per item
//
// Every form of every setting has its fields in the form, so switching a
// form keeps what was typed into the others. A bool is "true" when set and
// absent otherwise, as a checkbox sends it.

// FormError is a field the form could not read.
type FormError struct {
	Path    string
	Problem string // "number", "choice"
}

func (e *FormError) Error() string { return fmt.Sprintf("form field %s: %s", e.Path, e.Problem) }

// rowKeys name the field that makes a list row count: rows with it empty are
// the blank rows of the form.
var rowKeys = map[reflect.Type]string{
	reflect.TypeOf(Account{}):            "Name",
	reflect.TypeOf(Script{}):             "Content",
	reflect.TypeOf(ComponentSetting{}):   "Value",
	reflect.TypeOf(EffectSetting{}):      "Key",
	reflect.TypeOf(DesktopIconSetting{}): "Key",
	reflect.TypeOf(StartFolderSetting{}): "Key",
	reflect.TypeOf(ExtraXML{}):           "XML",
	reflect.TypeOf(ImportedScript{}):     "Content",
}

func intPtr(i int) *int { return &i }

// FormDefault is the value a form of a setting starts with when chosen.
func FormDefault(t reflect.Type) reflect.Value {
	var v any
	switch t {
	case reflect.TypeOf(UnattendedLanguageSettings{}):
		v = UnattendedLanguageSettings{ImageLanguage: "en-US", LocaleAndKeyboard: LocaleAndKeyboard{Locale: "en-US", Keyboard: "00000409"}, GeoLocation: "244"}
	case reflect.TypeOf(UnattendedAccountSettings{}):
		v = UnattendedAccountSettings{Accounts: []Account{}, AutoLogonSettings: OwnAutoLogonSettings{}, ObscurePasswords: true}
	case reflect.TypeOf(CustomLockoutSettings{}):
		v = CustomLockoutSettings{LockoutThreshold: intPtr(10), LockoutDuration: intPtr(10), LockoutWindow: intPtr(10)}
	case reflect.TypeOf(CustomPasswordExpirationSettings{}):
		v = CustomPasswordExpirationSettings{MaxAge: intPtr(42)}
	case reflect.TypeOf(EnabledProcessAuditSettings{}):
		v = EnabledProcessAuditSettings{IncludeCommandLine: true}
	case reflect.TypeOf(ExplicitTimeZoneSettings{}):
		v = ExplicitTimeZoneSettings{TimeZone: "UTC"}
	case reflect.TypeOf(ParameterizedWifiSettings{}):
		v = ParameterizedWifiSettings{Authentication: "WPA2PSK", ConnectAutomatically: true}
	case reflect.TypeOf(ConfigureLockKeySettings{}):
		v = ConfigureLockKeySettings{CapsLock: LockKeySetting{"Off", "Toggle"}, NumLock: LockKeySetting{"On", "Toggle"}, ScrollLock: LockKeySetting{"Off", "Toggle"}}
	case reflect.TypeOf(SolidWallpaperSettings{}):
		v = SolidWallpaperSettings{Color: "#008080"}
	case reflect.TypeOf(CustomColorSettings{}):
		v = CustomColorSettings{SystemTheme: "Dark", AppsTheme: "Light", EnableTransparency: true, AccentColor: "#0078D4"}
	case reflect.TypeOf(DefaultPESettings{}):
		v = DefaultPESettings{EditionSettings: InteractiveEditionSettings{}, BypassRequirementsCheck: true}
	case reflect.TypeOf(GeneratePESettings{}):
		v = GeneratePESettings{
			PartitionSettings:   FormDefault(reflect.TypeOf(UnattendedPartitionSettings{})).Interface().(UnattendedPartitionSettings),
			InstallFromSettings: InteractiveInstallFromSettings{},
			PagingFileSettings:  AutomaticPagingFileSettings{},
		}
	case reflect.TypeOf(UnattendedPartitionSettings{}):
		v = UnattendedPartitionSettings{TargetDiskSettings: FixedTargetDiskSettings{Index: 0}, PartitionLayout: "Automatic", RecoveryMode: "Partition", SystemSize: 300, RecoverySize: 1000}
	case reflect.TypeOf(UnattendedEditionSettings{}):
		v = UnattendedEditionSettings{Edition: "pro"}
	case reflect.TypeOf(EditionInstallFromSettings{}):
		v = EditionInstallFromSettings{Edition: "pro"}
	case reflect.TypeOf(IndexInstallFromSettings{}):
		v = IndexInstallFromSettings{Index: 1}
	case reflect.TypeOf(CustomPagingFileSettings{}):
		v = CustomPagingFileSettings{InitialSizeMiB: 1024, MaxSizeMiB: 4096}
	case reflect.TypeOf(CustomEffects{}):
		settings := make([]EffectSetting, len(effectOrder))
		for i, e := range effectOrder {
			settings[i] = EffectSetting{Key: e, Value: true}
		}
		v = CustomEffects{Settings: settings}
	case reflect.TypeOf(CustomStickyKeysSettings{}):
		v = CustomStickyKeysSettings{Flags: []StickyKeys{}}
	case reflect.TypeOf(CustomDesktopIconSettings{}):
		v = CustomDesktopIconSettings{Settings: []DesktopIconSetting{}}
	case reflect.TypeOf(CustomStartFolderSettings{}):
		v = CustomStartFolderSettings{Settings: []StartFolderSetting{}}
	default:
		return constructorDefaults(t)
	}
	return reflect.ValueOf(v)
}

// FormValues writes settings as form values.
func FormValues(c Config) url.Values {
	out := url.Values{}
	encodeForm(out, "", reflect.ValueOf(c))
	return out
}

func encodeForm(out url.Values, path string, v reflect.Value) {
	switch v.Kind() {
	case reflect.Interface:
		chosen := ""
		if !v.IsNil() {
			chosen = v.Elem().Type().Name()
		}
		out.Set(path+".$type", chosen)
		for _, vt := range variantTypes {
			if !vt.Implements(v.Type()) {
				continue
			}
			value := FormDefault(vt)
			if vt.Name() == chosen {
				value = v.Elem()
			}
			encodeForm(out, path+"."+vt.Name(), value)
		}
	case reflect.Pointer:
		if v.IsNil() {
			if v.Type().Elem().Kind() == reflect.Struct {
				encodeForm(out, path, reflect.New(v.Type().Elem()).Elem())
			} else {
				out.Set(path, "")
			}
			return
		}
		encodeForm(out, path, v.Elem())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			encodeForm(out, join(path, v.Type().Field(i).Name), v.Field(i))
		}
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Struct {
			for i := 0; i < v.Len(); i++ {
				encodeForm(out, join(path, strconv.Itoa(i)), v.Index(i))
			}
			return
		}
		for i := 0; i < v.Len(); i++ {
			out.Add(path, v.Index(i).String())
		}
	case reflect.Bool:
		if v.Bool() {
			out.Set(path, "true")
		}
	case reflect.Int:
		out.Set(path, strconv.FormatInt(v.Int(), 10))
	case reflect.String:
		out.Set(path, v.String())
	default:
		panic("unattend: no form encoding for " + v.Type().String())
	}
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// ParseFormValues reads settings from form values; it is the inverse of
// FormValues, with blank list rows left out.
func ParseFormValues(form url.Values) (Config, error) {
	out := Default()
	v := reflect.ValueOf(&out).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Type().Field(i)
		val, err := decodeForm(form, f.Name, f.Type, v.Field(i))
		if err != nil {
			return Config{}, err
		}
		v.Field(i).Set(val)
	}
	return out, nil
}

func decodeForm(form url.Values, path string, t reflect.Type, start reflect.Value) (reflect.Value, error) {
	switch t.Kind() {
	case reflect.Interface:
		chosen := form.Get(path + ".$type")
		for _, vt := range variantTypes {
			if vt.Name() == chosen && vt.Implements(t) {
				val, err := decodeForm(form, path+"."+vt.Name(), vt, FormDefault(vt))
				if err != nil {
					return reflect.Value{}, err
				}
				out := reflect.New(t).Elem()
				out.Set(val)
				return out, nil
			}
		}
		if chosen == "" && start.IsValid() && !start.IsNil() {
			return start, nil
		}
		return reflect.Value{}, &FormError{Path: path, Problem: "choice"}
	case reflect.Pointer:
		if t.Elem().Kind() == reflect.Struct {
			inner, err := decodeForm(form, path, t.Elem(), reflect.New(t.Elem()).Elem())
			if err != nil {
				return reflect.Value{}, err
			}
			if inner.IsZero() {
				return reflect.Zero(t), nil
			}
			p := reflect.New(t.Elem())
			p.Elem().Set(inner)
			return p, nil
		}
		raw, ok := form[path]
		if !ok {
			return start, nil
		}
		if len(raw) == 0 || strings.TrimSpace(raw[0]) == "" {
			return reflect.Zero(t), nil
		}
		inner, err := decodeForm(form, path, t.Elem(), reflect.Value{})
		if err != nil {
			return reflect.Value{}, err
		}
		p := reflect.New(t.Elem())
		p.Elem().Set(inner)
		return p, nil
	case reflect.Struct:
		out := reflect.New(t).Elem()
		if start.IsValid() {
			out.Set(start)
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			var fieldStart reflect.Value
			if start.IsValid() {
				fieldStart = start.Field(i)
			}
			val, err := decodeForm(form, join(path, f.Name), f.Type, fieldStart)
			if err != nil {
				return reflect.Value{}, err
			}
			out.Field(i).Set(val)
		}
		return out, nil
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Struct {
			rows := formRows(form, path)
			out := reflect.MakeSlice(t, 0, len(rows))
			key := rowKeys[t.Elem()]
			for _, i := range rows {
				rowPath := join(path, strconv.Itoa(i))
				if key != "" && strings.TrimSpace(form.Get(join(rowPath, key))) == "" {
					continue
				}
				val, err := decodeForm(form, rowPath, t.Elem(), reflect.Value{})
				if err != nil {
					return reflect.Value{}, err
				}
				out = reflect.Append(out, val)
			}
			return out, nil
		}
		out := reflect.MakeSlice(t, 0, len(form[path]))
		for _, s := range form[path] {
			if s == "" {
				continue
			}
			out = reflect.Append(out, reflect.ValueOf(s).Convert(t.Elem()))
		}
		return out, nil
	case reflect.Bool:
		s := form.Get(path)
		return reflect.ValueOf(s == "true" || s == "on"), nil
	case reflect.Int:
		raw, ok := form[path]
		if !ok && start.IsValid() {
			return start, nil
		}
		s := ""
		if len(raw) > 0 {
			s = strings.TrimSpace(raw[0])
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return reflect.Value{}, &FormError{Path: path, Problem: "number"}
		}
		return reflect.ValueOf(n), nil
	case reflect.String:
		raw, ok := form[path]
		if !ok {
			if start.IsValid() {
				return start, nil
			}
			return reflect.Zero(t), nil
		}
		s := ""
		if len(raw) > 0 {
			s = raw[0]
		}
		return reflect.ValueOf(s).Convert(t), nil
	}
	panic("unattend: no form decoding for " + t.String())
}

// formRows lists the row numbers of a list in the form, in order.
func formRows(form url.Values, path string) []int {
	prefix := path + "."
	seen := map[int]bool{}
	for k := range form {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		rest := k[len(prefix):]
		if i := strings.IndexByte(rest, '.'); i > 0 {
			if n, err := strconv.Atoi(rest[:i]); err == nil && n >= 0 && n < 10000 {
				seen[n] = true
			}
		}
	}
	rows := make([]int, 0, len(seen))
	for n := range seen {
		rows = append(rows, n)
	}
	sort.Ints(rows)
	return rows
}
