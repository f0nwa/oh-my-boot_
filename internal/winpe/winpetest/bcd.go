// Package winpetest provides synthetic media data without redistributing Windows files.
package winpetest

import (
	"encoding/binary"
	"unicode/utf16"
)

const Loader11 = "{aed5414f-d6f8-4ed2-a60f-ff97fec55ee0}"
const Loader10 = "{57f4ba3c-98d2-11e5-9cde-00241dc7aef5}"

func Wide(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2*(len(u)+1))
	for i, v := range u {
		binary.LittleEndian.PutUint16(b[i*2:], v)
	}
	return b
}

type hiveBuilder struct{ b []byte }

func (h *hiveBuilder) cell(b []byte) uint32 {
	n := (len(b) + 4 + 7) &^ 7
	o := len(h.b)
	h.b = append(h.b, make([]byte, n)...)
	binary.LittleEndian.PutUint32(h.b[o:], uint32(-int32(n)))
	copy(h.b[o+4:], b)
	return uint32(o - 4096)
}
func (h *hiveBuilder) key(name string, children []uint32, value []byte) uint32 {
	b := make([]byte, 76+len(name))
	copy(b, "nk")
	binary.LittleEndian.PutUint16(b[2:], 32)
	binary.LittleEndian.PutUint16(b[72:], uint16(len(name)))
	copy(b[76:], name)
	if len(children) > 0 {
		list := make([]byte, 4+4*len(children))
		copy(list, "li")
		binary.LittleEndian.PutUint16(list[2:], uint16(len(children)))
		for i, c := range children {
			binary.LittleEndian.PutUint32(list[4+i*4:], c)
		}
		binary.LittleEndian.PutUint32(b[20:], uint32(len(children)))
		binary.LittleEndian.PutUint32(b[28:], h.cell(list))
	}
	if value != nil {
		v := make([]byte, 27)
		copy(v, "vk")
		binary.LittleEndian.PutUint16(v[2:], 7)
		binary.LittleEndian.PutUint32(v[4:], uint32(len(value)))
		binary.LittleEndian.PutUint32(v[8:], h.cell(value))
		typ := uint32(3)
		if len(name) == 8 && (name[1] == '2' || name[1] == '3') {
			typ = 1
		}
		if len(name) == 8 && name[1] == '4' {
			typ = 7
		}
		binary.LittleEndian.PutUint32(v[12:], typ)
		binary.LittleEndian.PutUint16(v[16:], 1)
		copy(v[20:], "Element")
		o := h.cell(v)
		list := make([]byte, 4)
		binary.LittleEndian.PutUint32(list, o)
		binary.LittleEndian.PutUint32(b[36:], 1)
		binary.LittleEndian.PutUint32(b[40:], h.cell(list))
	}
	return h.cell(b)
}

// StrelecBCD models native boot-manager entries, device paths, and RAM-disk options.
func StrelecBCD() []byte {
	h := hiveBuilder{make([]byte, 4128)}
	copy(h.b, "regf")
	var objects []uint32
	for _, v := range []struct{ version, guid string }{{"10", Loader10}, {"11", Loader11}} {
		device := append(make([]byte, 80), Wide(`\SSTR\strelec`+v.version+`x64.wim`)...)
		elements := []uint32{h.key("11000001", nil, device), h.key("21000001", nil, device), h.key("12000002", nil, Wide(`\windows\system32\boot\winload.exe`))}
		objects = append(objects, h.key(v.guid, []uint32{h.key("Elements", elements, nil)}, nil))
	}
	manager := []uint32{h.key("23000003", nil, Wide(Loader10)), h.key("24000001", nil, Wide(Loader10+"\x00"+Loader11+"\x00")), h.key("25000004", nil, []byte{10, 0, 0, 0, 0, 0, 0, 0})}
	objects = append(objects, h.key("{9dea862c-5cdd-4e70-acc1-f32b344d4795}", []uint32{h.key("Elements", manager, nil)}, nil), h.key("{ae5534e0-a924-466c-b836-758539a3ee3a}", []uint32{h.key("Elements", []uint32{h.key("32000004", nil, Wide(`\SSTR\boot.sdi`)), h.key("1200004a", nil, Wide(`\SSTR\Fonts`))}, nil)}, nil))
	root := h.key("BCD", []uint32{h.key("Objects", objects, nil)}, nil)
	binary.LittleEndian.PutUint32(h.b[36:], root)
	return h.b
}
