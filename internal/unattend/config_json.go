package unattend

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// JSON of Config, the format tools/unattend-golden reads as well:
//   - the root object changes Default(): fields not given keep their default;
//   - a setting with several forms is {"$type": "<C# class>", …fields};
//   - field names are the C# names; lookups are IDs; enums are names.

// variantTypes are all forms of all settings; the Go type name is the C#
// class name.
var variantTypes = []reflect.Type{
	reflect.TypeOf(InteractiveLanguageSettings{}), reflect.TypeOf(UnattendedLanguageSettings{}),
	reflect.TypeOf(InteractiveMicrosoftAccountSettings{}), reflect.TypeOf(InteractiveLocalAccountSettings{}), reflect.TypeOf(UnattendedAccountSettings{}),
	reflect.TypeOf(NoneAutoLogonSettings{}), reflect.TypeOf(BuiltinAutoLogonSettings{}), reflect.TypeOf(OwnAutoLogonSettings{}),
	reflect.TypeOf(DefaultLockoutSettings{}), reflect.TypeOf(DisableLockoutSettings{}), reflect.TypeOf(CustomLockoutSettings{}),
	reflect.TypeOf(DefaultPasswordExpirationSettings{}), reflect.TypeOf(UnlimitedPasswordExpirationSettings{}), reflect.TypeOf(CustomPasswordExpirationSettings{}),
	reflect.TypeOf(DisabledProcessAuditSettings{}), reflect.TypeOf(EnabledProcessAuditSettings{}),
	reflect.TypeOf(RandomComputerNameSettings{}), reflect.TypeOf(CustomComputerNameSettings{}), reflect.TypeOf(ScriptComputerNameSettings{}), reflect.TypeOf(PromptComputerNameSettings{}),
	reflect.TypeOf(ImplicitTimeZoneSettings{}), reflect.TypeOf(ExplicitTimeZoneSettings{}),
	reflect.TypeOf(SkipWifiSettings{}), reflect.TypeOf(InteractiveWifiSettings{}), reflect.TypeOf(XmlWifiSettings{}), reflect.TypeOf(ParameterizedWifiSettings{}),
	reflect.TypeOf(SkipAppLockerSettings{}), reflect.TypeOf(ConfigureAppLockerSettings{}),
	reflect.TypeOf(SkipLockKeySettings{}), reflect.TypeOf(ConfigureLockKeySettings{}),
	reflect.TypeOf(DefaultWallpaperSettings{}), reflect.TypeOf(SolidWallpaperSettings{}), reflect.TypeOf(ScriptWallpaperSettings{}),
	reflect.TypeOf(DefaultLockScreenSettings{}), reflect.TypeOf(ScriptLockScreenSettings{}),
	reflect.TypeOf(DefaultColorSettings{}), reflect.TypeOf(CustomColorSettings{}),
	reflect.TypeOf(DefaultPESettings{}), reflect.TypeOf(GeneratePESettings{}), reflect.TypeOf(ScriptPESetttings{}),
	reflect.TypeOf(InteractiveEditionSettings{}), reflect.TypeOf(FirmwareEditionSettings{}), reflect.TypeOf(UnattendedEditionSettings{}), reflect.TypeOf(CustomEditionSettings{}),
	reflect.TypeOf(InteractiveTargetDiskSettings{}), reflect.TypeOf(ScriptTargetDiskSettings{}), reflect.TypeOf(FixedTargetDiskSettings{}), reflect.TypeOf(GeneratedTargetDiskSettings{}),
	reflect.TypeOf(InteractivePartitionSettings{}), reflect.TypeOf(CustomPartitionSettings{}), reflect.TypeOf(UnattendedPartitionSettings{}),
	reflect.TypeOf(InteractiveInstallFromSettings{}), reflect.TypeOf(EditionInstallFromSettings{}), reflect.TypeOf(IndexInstallFromSettings{}), reflect.TypeOf(NameInstallFromSettings{}),
	reflect.TypeOf(AutomaticPagingFileSettings{}), reflect.TypeOf(CustomPagingFileSettings{}), reflect.TypeOf(NoPagingFileSettings{}),
	reflect.TypeOf(DefaultStartPinsSettings{}), reflect.TypeOf(EmptyStartPinsSettings{}), reflect.TypeOf(CustomStartPinsSettings{}),
	reflect.TypeOf(DefaultStartTilesSettings{}), reflect.TypeOf(EmptyStartTilesSettings{}), reflect.TypeOf(CustomStartTilesSettings{}),
	reflect.TypeOf(DefaultTaskbarIcons{}), reflect.TypeOf(EmptyTaskbarIcons{}), reflect.TypeOf(CustomTaskbarIcons{}),
	reflect.TypeOf(DefaultEffects{}), reflect.TypeOf(BestPerformanceEffects{}), reflect.TypeOf(BestAppearanceEffects{}), reflect.TypeOf(CustomEffects{}),
	reflect.TypeOf(DefaultDesktopIconSettings{}), reflect.TypeOf(CustomDesktopIconSettings{}),
	reflect.TypeOf(DefaultStartFolderSettings{}), reflect.TypeOf(CustomStartFolderSettings{}),
	reflect.TypeOf(DefaultStickyKeysSettings{}), reflect.TypeOf(DisabledStickyKeysSettings{}), reflect.TypeOf(CustomStickyKeysSettings{}),
}

// constructorDefaults are default values of C# constructor parameters,
// applied when JSON leaves them out.
func constructorDefaults(t reflect.Type) reflect.Value {
	v := reflect.New(t).Elem()
	switch t {
	case reflect.TypeOf(GeneratedTargetDiskSettings{}):
		minSize, maxSize, index := 100, 4000, 0
		v.Set(reflect.ValueOf(GeneratedTargetDiskSettings{MinSizeGiB: &minSize, MaxSizeGiB: &maxSize, Index: &index, AssertNoPartitions: true}))
	case reflect.TypeOf(UnattendedPartitionSettings{}):
		v.Set(reflect.ValueOf(UnattendedPartitionSettings{SystemSize: 300, RecoverySize: 1000}))
	}
	return v
}

// MarshalJSON writes all settings.
func (c Config) MarshalJSON() ([]byte, error) {
	v, err := encodeValue(reflect.ValueOf(c))
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// UnmarshalJSON reads settings over the defaults.
func (c *Config) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	out := Default()
	rv := reflect.ValueOf(&out).Elem()
	for name, raw := range fields {
		f := rv.FieldByName(name)
		if !f.IsValid() {
			return fmt.Errorf("unknown setting '%s'", name)
		}
		val, err := decodeValue(f.Type(), raw)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		f.Set(val)
	}
	*c = out
	return nil
}

// ordered keeps JSON object keys in field order.
type ordered []struct {
	k string
	v any
}

func (o ordered) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, kv := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(kv.k)
		v, err := json.Marshal(kv.v)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func encodeValue(v reflect.Value) (any, error) {
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return nil, nil
		}
		inner := v.Elem()
		obj, err := encodeStruct(inner)
		if err != nil {
			return nil, err
		}
		return append(ordered{{"$type", inner.Type().Name()}}, obj...), nil
	case reflect.Pointer:
		if v.IsNil() {
			return nil, nil
		}
		return encodeValue(v.Elem())
	case reflect.Struct:
		return encodeStruct(v)
	case reflect.Slice:
		out := make([]any, v.Len())
		for i := range out {
			x, err := encodeValue(v.Index(i))
			if err != nil {
				return nil, err
			}
			out[i] = x
		}
		return out, nil
	default:
		return v.Interface(), nil
	}
}

func encodeStruct(v reflect.Value) (ordered, error) {
	var out ordered
	for i := 0; i < v.NumField(); i++ {
		f := v.Type().Field(i)
		if !f.IsExported() {
			continue
		}
		x, err := encodeValue(v.Field(i))
		if err != nil {
			return nil, err
		}
		out = append(out, struct {
			k string
			v any
		}{f.Name, x})
	}
	return out, nil
}

func decodeValue(t reflect.Type, raw json.RawMessage) (reflect.Value, error) {
	if string(bytes.TrimSpace(raw)) == "null" {
		switch t.Kind() {
		case reflect.Pointer, reflect.Interface, reflect.Slice:
			return reflect.Zero(t), nil
		}
		return reflect.Value{}, fmt.Errorf("null for %s", t)
	}
	switch t.Kind() {
	case reflect.Interface:
		var head struct {
			Type string `json:"$type"`
		}
		if err := json.Unmarshal(raw, &head); err != nil {
			return reflect.Value{}, err
		}
		for _, vt := range variantTypes {
			if vt.Name() == head.Type && vt.Implements(t) {
				val, err := decodeStruct(vt, raw)
				if err != nil {
					return reflect.Value{}, fmt.Errorf("%s: %w", head.Type, err)
				}
				out := reflect.New(t).Elem()
				out.Set(val)
				return out, nil
			}
		}
		return reflect.Value{}, fmt.Errorf("no type '%s' for %s", head.Type, t.Name())
	case reflect.Pointer:
		inner, err := decodeValue(t.Elem(), raw)
		if err != nil {
			return reflect.Value{}, err
		}
		p := reflect.New(t.Elem())
		p.Elem().Set(inner)
		return p, nil
	case reflect.Struct:
		return decodeStruct(t, raw)
	case reflect.Slice:
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return reflect.Value{}, err
		}
		out := reflect.MakeSlice(t, len(items), len(items))
		for i, item := range items {
			val, err := decodeValue(t.Elem(), item)
			if err != nil {
				return reflect.Value{}, fmt.Errorf("[%d]: %w", i, err)
			}
			out.Index(i).Set(val)
		}
		return out, nil
	default:
		p := reflect.New(t)
		if err := json.Unmarshal(raw, p.Interface()); err != nil {
			return reflect.Value{}, err
		}
		return p.Elem(), nil
	}
}

func decodeStruct(t reflect.Type, raw json.RawMessage) (reflect.Value, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return reflect.Value{}, err
	}
	delete(fields, "$type")
	out := constructorDefaults(t)
	// Names are matched without regard to case, as the C# tool does.
	byName := map[string]int{}
	for i := 0; i < t.NumField(); i++ {
		byName[strings.ToLower(t.Field(i).Name)] = i
	}
	names := make([]string, 0, len(fields))
	for n := range fields {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		i, ok := byName[strings.ToLower(n)]
		if !ok {
			return reflect.Value{}, fmt.Errorf("%s: unknown '%s'", t.Name(), n)
		}
		val, err := decodeValue(t.Field(i).Type, fields[n])
		if err != nil {
			return reflect.Value{}, fmt.Errorf("%s: %w", n, err)
		}
		out.Field(i).Set(val)
	}
	return out, nil
}
