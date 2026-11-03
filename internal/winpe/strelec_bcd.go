package winpe

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"unicode/utf16"
)

// RenderStrelecBCD adapts the media's BCD without introducing a foreign Windows
// template. Only existing value data is changed; registry cells are not moved.
func RenderStrelecBCD(original []byte, version string) ([]byte, error) {
	if version != "10" && version != "11" {
		return nil, fmt.Errorf("unsupported Strelec BCD version")
	}
	b := append([]byte(nil), original...)
	if len(b) < 4096 || len(b) > 4*1024*1024 || string(b[:4]) != "regf" {
		return nil, fmt.Errorf("invalid BCD hive")
	}
	h := bcdHive(b)
	objects, err := h.child(binary.LittleEndian.Uint32(b[36:40]), "Objects")
	if err != nil {
		return nil, err
	}
	keys, err := h.children(objects)
	if err != nil {
		return nil, err
	}
	old := bcdUTF16(`\SSTR\strelec` + version + `x64.wim`)
	var selected uint32
	var selectedName string
	for _, k := range keys {
		v, e := h.element(k, "11000001")
		if e != nil {
			continue
		}
		if bytes.Contains(v.data, old) {
			if selectedName != "" {
				return nil, fmt.Errorf("ambiguous Strelec BCD loader")
			}
			selected = k
			selectedName, err = h.name(k)
			if err != nil {
				return nil, err
			}
		}
	}
	if selectedName == "" {
		return nil, fmt.Errorf("Strelec BCD loader is missing")
	}
	for _, id := range []string{"11000001", "21000001"} {
		v, e := h.element(selected, id)
		if e != nil {
			return nil, e
		}
		if bytes.Count(v.data, old) != 1 {
			return nil, fmt.Errorf("invalid Strelec BCD device")
		}
		// Device records contain a NUL-terminated path. Keep their allocation and
		// enclosing device lengths, replacing the suffix with a shorter padded path.
		patched := append([]byte(nil), v.data...)
		i := bytes.Index(patched, old)
		clear(patched[i : i+len(old)])
		copy(patched[i:], bcdUTF16(`\sources\boot.wim`))
		if err := v.set(patched); err != nil {
			return nil, err
		}
	}
	loader, err := h.element(selected, "12000002")
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(bcdString(loader.data), `\windows\system32\boot\winload.exe`) {
		return nil, fmt.Errorf("unsupported Strelec BCD winload path")
	}
	manager, err := h.child(objects, "{9dea862c-5cdd-4e70-acc1-f32b344d4795}")
	if err != nil {
		return nil, err
	}
	for _, id := range []string{"23000003", "24000001"} {
		v, e := h.element(manager, id)
		if e != nil {
			return nil, e
		}
		data := bcdUTF16(selectedName)
		if id == "24000001" {
			data = append(data, 0, 0)
		}
		if err := v.set(data); err != nil {
			return nil, err
		}
	}
	timeout, err := h.element(manager, "25000004")
	if err != nil {
		return nil, err
	}
	if len(timeout.data) != 8 {
		return nil, fmt.Errorf("invalid BCD timeout")
	}
	if err := timeout.set(make([]byte, 8)); err != nil {
		return nil, err
	}
	// The RAM-disk options object is referenced by the native device record.
	// Patch each SDI path present, preserving those references and device data.
	found := false
	for _, k := range keys {
		if fonts, e := h.element(k, "1200004a"); e == nil {
			if !strings.EqualFold(bcdString(fonts.data), `\SSTR\Fonts`) {
				return nil, fmt.Errorf("unsupported Strelec BCD font path")
			}
			if err := fonts.set(bcdUTF16(`\boot\fonts`)); err != nil {
				return nil, err
			}
		}
		v, e := h.element(k, "32000004")
		if e != nil {
			continue
		}
		if !strings.EqualFold(bcdString(v.data), `\SSTR\boot.sdi`) {
			return nil, fmt.Errorf("unsupported Strelec BCD SDI path")
		}
		if err := v.set(bcdUTF16(`\boot.sdi`)); err != nil {
			return nil, err
		}
		found = true
	}
	if !found {
		return nil, fmt.Errorf("Strelec BCD SDI options are missing")
	}
	return b, nil
}

func bcdUTF16(s string) []byte {
	units := utf16.Encode([]rune(s))
	b := make([]byte, 2*(len(units)+1))
	for i, u := range units {
		binary.LittleEndian.PutUint16(b[2*i:], u)
	}
	return b
}
func bcdString(b []byte) string {
	var u []uint16
	for len(b) >= 2 {
		v := binary.LittleEndian.Uint16(b)
		if v == 0 {
			break
		}
		u = append(u, v)
		b = b[2:]
	}
	return string(utf16.Decode(u))
}

type bcdHive []byte

func (h bcdHive) cell(offset uint32) ([]byte, error) {
	p := uint64(offset) + 4096
	if offset%8 != 0 || p+4 > uint64(len(h)) {
		return nil, fmt.Errorf("invalid BCD cell offset")
	}
	n := -int64(int32(binary.LittleEndian.Uint32(h[p : p+4])))
	if n < 8 || n%8 != 0 || uint64(n) > uint64(len(h))-p {
		return nil, fmt.Errorf("invalid BCD cell size")
	}
	return h[p+4 : p+uint64(n)], nil
}
func (h bcdHive) key(offset uint32) ([]byte, error) {
	b, e := h.cell(offset)
	if e != nil {
		return nil, e
	}
	if len(b) < 76 || string(b[:2]) != "nk" {
		return nil, fmt.Errorf("invalid BCD key")
	}
	return b, nil
}
func (h bcdHive) name(offset uint32) (string, error) {
	b, e := h.key(offset)
	if e != nil {
		return "", e
	}
	n := int(binary.LittleEndian.Uint16(b[72:74]))
	if n > len(b)-76 {
		return "", fmt.Errorf("invalid BCD key name")
	}
	if binary.LittleEndian.Uint16(b[2:4])&32 != 0 {
		return string(b[76 : 76+n]), nil
	}
	return bcdString(b[76 : 76+n]), nil
}
func (h bcdHive) children(offset uint32) ([]uint32, error) {
	b, e := h.key(offset)
	if e != nil {
		return nil, e
	}
	n := binary.LittleEndian.Uint32(b[20:24])
	if n == 0 {
		return nil, nil
	}
	if n > 4096 {
		return nil, fmt.Errorf("too many BCD keys")
	}
	var out []uint32
	budget := 4096
	var list func(uint32, int) error
	list = func(o uint32, depth int) error {
		budget--
		if depth > 8 || budget < 0 || len(out) > 4096 {
			return fmt.Errorf("invalid BCD index tree")
		}
		b, e := h.cell(o)
		if e != nil {
			return e
		}
		if len(b) < 4 {
			return fmt.Errorf("invalid BCD index")
		}
		tag := string(b[:2])
		count := int(binary.LittleEndian.Uint16(b[2:4]))
		stride := 4
		if tag == "lf" || tag == "lh" {
			stride = 8
		} else if tag != "li" && tag != "ri" {
			return fmt.Errorf("unsupported BCD index")
		}
		if count > (len(b)-4)/stride {
			return fmt.Errorf("invalid BCD index length")
		}
		for i := 0; i < count; i++ {
			v := binary.LittleEndian.Uint32(b[4+i*stride:])
			if tag == "ri" {
				if e := list(v, depth+1); e != nil {
					return e
				}
			} else {
				out = append(out, v)
			}
		}
		return nil
	}
	if e := list(binary.LittleEndian.Uint32(b[28:32]), 0); e != nil {
		return nil, e
	}
	if len(out) != int(n) {
		return nil, fmt.Errorf("invalid BCD child count")
	}
	return out, nil
}
func (h bcdHive) child(offset uint32, name string) (uint32, error) {
	keys, e := h.children(offset)
	if e != nil {
		return 0, e
	}
	for _, k := range keys {
		s, e := h.name(k)
		if e != nil {
			return 0, e
		}
		if strings.EqualFold(s, name) {
			return k, nil
		}
	}
	return 0, fmt.Errorf("BCD key missing: %s", name)
}

type bcdValue struct {
	record, data []byte
	inline       bool
}

func (v bcdValue) set(data []byte) error {
	if len(data) > len(v.data) {
		return fmt.Errorf("BCD replacement exceeds existing value")
	}
	clear(v.data)
	copy(v.data, data)
	n := uint32(len(data))
	if v.inline {
		n |= 0x80000000
	}
	binary.LittleEndian.PutUint32(v.record[4:8], n)
	return nil
}
func (h bcdHive) element(object uint32, id string) (bcdValue, error) {
	e, err := h.child(object, "Elements")
	if err != nil {
		return bcdValue{}, err
	}
	e, err = h.child(e, id)
	if err != nil {
		return bcdValue{}, err
	}
	k, err := h.key(e)
	if err != nil {
		return bcdValue{}, err
	}
	n := binary.LittleEndian.Uint32(k[36:40])
	if n == 0 || n > 64 {
		return bcdValue{}, fmt.Errorf("invalid BCD value count")
	}
	list, err := h.cell(binary.LittleEndian.Uint32(k[40:44]))
	if err != nil {
		return bcdValue{}, err
	}
	if int(n) > len(list)/4 {
		return bcdValue{}, fmt.Errorf("invalid BCD values")
	}
	for i := uint32(0); i < n; i++ {
		v, err := h.cell(binary.LittleEndian.Uint32(list[i*4:]))
		if err != nil {
			return bcdValue{}, err
		}
		if len(v) < 20 || string(v[:2]) != "vk" {
			return bcdValue{}, fmt.Errorf("invalid BCD value")
		}
		size := int(binary.LittleEndian.Uint16(v[2:4]))
		if size > len(v)-20 {
			return bcdValue{}, fmt.Errorf("invalid BCD value name")
		}
		name := bcdString(v[20 : 20+size])
		if binary.LittleEndian.Uint16(v[16:18])&1 != 0 {
			name = string(v[20 : 20+size])
		}
		if !strings.EqualFold(name, "Element") {
			continue
		}
		expectedType := uint32(3)
		if len(id) == 8 && (id[1] == '2' || id[1] == '3') {
			expectedType = 1
		}
		if len(id) == 8 && id[1] == '4' {
			expectedType = 7
		}
		if binary.LittleEndian.Uint32(v[12:16]) != expectedType {
			return bcdValue{}, fmt.Errorf("invalid BCD element type")
		}
		length := binary.LittleEndian.Uint32(v[4:8])
		inline := length&0x80000000 != 0
		length &= 0x7fffffff
		var data []byte
		if inline {
			data = v[8:12]
		} else {
			data, err = h.cell(binary.LittleEndian.Uint32(v[8:12]))
			if err != nil {
				return bcdValue{}, err
			}
		}
		if uint64(length) > uint64(len(data)) {
			return bcdValue{}, fmt.Errorf("invalid BCD element length")
		}
		return bcdValue{v, data[:length], inline}, nil
	}
	return bcdValue{}, fmt.Errorf("BCD element value missing")
}
