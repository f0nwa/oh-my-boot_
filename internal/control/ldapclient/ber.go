package ldapclient

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	tagBoolean     byte = 0x01
	tagInteger     byte = 0x02
	tagOctetString byte = 0x04
	tagNull        byte = 0x05
	tagEnumerated  byte = 0x0A
	tagSequence    byte = 0x30
	tagSet         byte = 0x31

	// LDAPv3 Application tags (RFC 4511).
	appBindRequest       byte = 0x60
	appBindResponse      byte = 0x61
	appUnbindRequest     byte = 0x42
	appSearchRequest     byte = 0x63
	appSearchResultEntry byte = 0x64
	appSearchResultDone  byte = 0x65
	appSearchResultRef   byte = 0x73
	appExtendedRequest   byte = 0x77
	appExtendedResponse  byte = 0x78
)

const maxBERPacketSize = 4 << 20 // 4 MiB safety cap per LDAP message

type berNode struct {
	Tag      byte
	Data     []byte
	Children []*berNode
}

func encodeLength(n int) []byte {
	if n < 0x80 {
		return []byte{byte(n)}
	}
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], uint32(n))
	i := 0
	for i < 3 && buf[i] == 0 {
		i++
	}
	count := 4 - i
	out := make([]byte, 1+count)
	out[0] = byte(0x80 | count)
	copy(out[1:], buf[i:])
	return out
}

func encodeTag(tag byte, content []byte) []byte {
	lb := encodeLength(len(content))
	out := make([]byte, 0, 1+len(lb)+len(content))
	out = append(out, tag)
	out = append(out, lb...)
	out = append(out, content...)
	return out
}

func encodeInt(tag byte, v int64) []byte {
	if v == 0 {
		return encodeTag(tag, []byte{0})
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(v))
	start := 0
	if v > 0 {
		for start < 7 && buf[start] == 0 && (buf[start+1]&0x80) == 0 {
			start++
		}
	} else {
		for start < 7 && buf[start] == 0xff && (buf[start+1]&0x80) != 0 {
			start++
		}
	}
	return encodeTag(tag, buf[start:])
}

func encodeBool(v bool) []byte {
	if v {
		return encodeTag(tagBoolean, []byte{0xff})
	}
	return encodeTag(tagBoolean, []byte{0x00})
}

func encodeString(s string) []byte {
	return encodeTag(tagOctetString, []byte(s))
}

func encodeSequence(tag byte, items ...[]byte) []byte {
	var total int
	for _, it := range items {
		total += len(it)
	}
	buf := make([]byte, 0, total)
	for _, it := range items {
		buf = append(buf, it...)
	}
	return encodeTag(tag, buf)
}

func readBERPacket(r io.Reader) (*berNode, error) {
	var head [2]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return nil, err
	}
	tag := head[0]
	length, err := readBERLength(r, head[1])
	if err != nil {
		return nil, err
	}
	if length < 0 || length > maxBERPacketSize {
		return nil, fmt.Errorf("ber packet size %d out of bounds", length)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}
	return parseBER(tag, data)
}

func readBERLength(r io.Reader, first byte) (int, error) {
	if first&0x80 == 0 {
		return int(first), nil
	}
	numBytes := int(first & 0x7f)
	if numBytes == 0 || numBytes > 4 {
		return 0, errors.New("unsupported BER length encoding")
	}
	var buf [4]byte
	if _, err := io.ReadFull(r, buf[4-numBytes:]); err != nil {
		return 0, err
	}
	return int(binary.BigEndian.Uint32(buf[:])), nil
}

func parseBER(tag byte, data []byte) (*berNode, error) {
	node := &berNode{Tag: tag, Data: data}
	if tag&0x20 == 0 { // primitive
		return node, nil
	}
	rd := bytes.NewReader(data)
	for rd.Len() > 0 {
		child, err := readBERPacket(rd)
		if err != nil {
			return nil, err
		}
		node.Children = append(node.Children, child)
	}
	return node, nil
}

func (n *berNode) intVal() (int64, error) {
	if len(n.Data) == 0 || len(n.Data) > 8 {
		return 0, errors.New("invalid BER integer length")
	}
	var v int64
	if n.Data[0]&0x80 != 0 {
		v = -1
	}
	for _, b := range n.Data {
		v = (v << 8) | int64(b)
	}
	return v, nil
}
