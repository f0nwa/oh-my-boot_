package control

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// SFTPv3 protocol constants (draft-ietf-secsh-filexfer-02).
const (
	sftpFXPInit     byte = 1
	sftpFXPVersion  byte = 2
	sftpFXPOpen     byte = 3
	sftpFXPClose    byte = 4
	sftpFXPRead     byte = 5
	sftpFXPWrite    byte = 6
	sftpFXPLstat    byte = 7
	sftpFXPFstat    byte = 8
	sftpFXPSetstat  byte = 9
	sftpFXPFsetstat byte = 10
	sftpFXPOpendir  byte = 11
	sftpFXPReaddir  byte = 12
	sftpFXPRemove   byte = 13
	sftpFXPMkdir    byte = 14
	sftpFXPRmdir    byte = 15
	sftpFXPRealpath byte = 16
	sftpFXPStat     byte = 17
	sftpFXPRename   byte = 18
	sftpFXPReadlink byte = 19
	sftpFXPSymlink  byte = 20

	sftpFXPStatus byte = 101
	sftpFXPHandle byte = 102
	sftpFXPData   byte = 103
	sftpFXPName   byte = 104
	sftpFXPAttrs  byte = 105

	sftpStatusOK               uint32 = 0
	sftpStatusEOF              uint32 = 1
	sftpStatusNoSuchFile       uint32 = 2
	sftpStatusPermissionDenied uint32 = 3
	sftpStatusFailure          uint32 = 4
	sftpStatusBadMessage       uint32 = 5
	sftpStatusNoConnection     uint32 = 6
	sftpStatusConnectionLost   uint32 = 7
	sftpStatusOpUnsupported    uint32 = 8

	sftpFXFRead   uint32 = 0x00000001
	sftpFXFWrite  uint32 = 0x00000002
	sftpFXFAppend uint32 = 0x00000004
	sftpFXFCreat  uint32 = 0x00000008
	sftpFXFTrunc  uint32 = 0x00000010
	sftpFXFExcl   uint32 = 0x00000020

	sftpAttrSize        uint32 = 0x00000001
	sftpAttrUIDGID      uint32 = 0x00000002
	sftpAttrPermissions uint32 = 0x00000004
	sftpAttrAcmodTime   uint32 = 0x00000008
	sftpAttrExtended    uint32 = 0x80000000
)

type sftpAttrs struct {
	flags       uint32
	size        uint64
	uid         uint32
	gid         uint32
	permissions uint32
	atime       uint32
	mtime       uint32
}

type sftpNameEntry struct {
	filename string
	longname string
	attrs    sftpAttrs
}

func attrsFromFileInfo(fi os.FileInfo) sftpAttrs {
	var a sftpAttrs
	a.flags = sftpAttrSize | sftpAttrPermissions | sftpAttrAcmodTime
	a.size = uint64(fi.Size())
	mode := uint32(fi.Mode().Perm())
	if fi.IsDir() {
		mode |= 0040000
	} else {
		mode |= 0100000
	}
	a.permissions = mode
	modTime := uint32(fi.ModTime().Unix())
	a.mtime = modTime
	a.atime = modTime
	return a
}

func posixModeString(m os.FileMode) string {
	var buf [10]byte
	if m.IsDir() {
		buf[0] = 'd'
	} else if m&os.ModeSymlink != 0 {
		buf[0] = 'l'
	} else if m&os.ModeNamedPipe != 0 {
		buf[0] = 'p'
	} else if m&os.ModeSocket != 0 {
		buf[0] = 's'
	} else if m&os.ModeDevice != 0 {
		if m&os.ModeCharDevice != 0 {
			buf[0] = 'c'
		} else {
			buf[0] = 'b'
		}
	} else {
		buf[0] = '-'
	}

	const rwx = "rwxrwxrwx"
	perm := m.Perm()
	for i := 0; i < 9; i++ {
		if perm&(1<<uint(8-i)) != 0 {
			buf[1+i] = rwx[i]
		} else {
			buf[1+i] = '-'
		}
	}

	if m&os.ModeSetuid != 0 {
		if buf[3] == 'x' {
			buf[3] = 's'
		} else {
			buf[3] = 'S'
		}
	}

	if m&os.ModeSetgid != 0 {
		if buf[6] == 'x' {
			buf[6] = 's'
		} else {
			buf[6] = 'S'
		}
	}

	if m&os.ModeSticky != 0 {
		if buf[9] == 'x' {
			buf[9] = 't'
		} else {
			buf[9] = 'T'
		}
	}

	return string(buf[:])
}

func formatLongname(name string, fi os.FileInfo) string {
	modeStr := posixModeString(fi.Mode())
	size := fi.Size()
	now := time.Now()
	mtime := fi.ModTime()
	var timeStr string
	if mtime.Before(now.AddDate(0, -6, 0)) || mtime.After(now.AddDate(0, 6, 0)) {
		timeStr = mtime.Format("Jan _2  2006")
	} else {
		timeStr = mtime.Format("Jan _2 15:04")
	}
	return fmt.Sprintf("%s   1 omb      omb      %8d %s %s", modeStr, size, timeStr, name)
}

func (a sftpAttrs) marshal(buf *bytes.Buffer) {
	putUint32(buf, a.flags)
	if a.flags&sftpAttrSize != 0 {
		putUint64(buf, a.size)
	}
	if a.flags&sftpAttrUIDGID != 0 {
		putUint32(buf, a.uid)
		putUint32(buf, a.gid)
	}
	if a.flags&sftpAttrPermissions != 0 {
		putUint32(buf, a.permissions)
	}
	if a.flags&sftpAttrAcmodTime != 0 {
		putUint32(buf, a.atime)
		putUint32(buf, a.mtime)
	}
}

func unmarshalAttrs(data []byte) (sftpAttrs, []byte, error) {
	var a sftpAttrs
	if len(data) < 4 {
		return a, nil, errors.New("short attrs flags")
	}
	a.flags = binary.BigEndian.Uint32(data[:4])
	data = data[4:]
	if a.flags&sftpAttrSize != 0 {
		if len(data) < 8 {
			return a, nil, errors.New("short attrs size")
		}
		a.size = binary.BigEndian.Uint64(data[:8])
		data = data[8:]
	}
	if a.flags&sftpAttrUIDGID != 0 {
		if len(data) < 8 {
			return a, nil, errors.New("short attrs uid/gid")
		}
		a.uid = binary.BigEndian.Uint32(data[:4])
		a.gid = binary.BigEndian.Uint32(data[4:8])
		data = data[8:]
	}
	if a.flags&sftpAttrPermissions != 0 {
		if len(data) < 4 {
			return a, nil, errors.New("short attrs permissions")
		}
		a.permissions = binary.BigEndian.Uint32(data[:4])
		data = data[4:]
	}
	if a.flags&sftpAttrAcmodTime != 0 {
		if len(data) < 8 {
			return a, nil, errors.New("short attrs acmodtime")
		}
		a.atime = binary.BigEndian.Uint32(data[:4])
		a.mtime = binary.BigEndian.Uint32(data[4:8])
		data = data[8:]
	}
	if a.flags&sftpAttrExtended != 0 {
		if len(data) < 4 {
			return a, nil, errors.New("short attrs ext count")
		}
		count := binary.BigEndian.Uint32(data[:4])
		data = data[4:]
		for i := uint32(0); i < count; i++ {
			_, rest, err := getSFTPString(data)
			if err != nil {
				return a, nil, err
			}
			_, rest, err = getSFTPString(rest)
			if err != nil {
				return a, nil, err
			}
			data = rest
		}
	}
	return a, data, nil
}

func putUint32(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}

func putUint64(buf *bytes.Buffer, v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	buf.Write(b[:])
}

func putSFTPString(buf *bytes.Buffer, s string) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(len(s)))
	buf.Write(b[:])
	buf.WriteString(s)
}

func getSFTPString(data []byte) (string, []byte, error) {
	if len(data) < 4 {
		return "", nil, errors.New("short string len")
	}
	l := binary.BigEndian.Uint32(data[:4])
	data = data[4:]
	if uint32(len(data)) < l {
		return "", nil, errors.New("short string data")
	}
	return string(data[:l]), data[l:], nil
}

func getUint32(data []byte) (uint32, []byte, error) {
	if len(data) < 4 {
		return 0, nil, errors.New("short uint32")
	}
	return binary.BigEndian.Uint32(data[:4]), data[4:], nil
}

func getUint64(data []byte) (uint64, []byte, error) {
	if len(data) < 8 {
		return 0, nil, errors.New("short uint64")
	}
	return binary.BigEndian.Uint64(data[:8]), data[8:], nil
}

func readSFTPPacket(r io.Reader) (byte, []byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return 0, nil, err
	}
	length := binary.BigEndian.Uint32(lenBuf[:])
	if length == 0 || length > 1024*1024 { // max 1MB packet limit
		return 0, nil, errors.New("invalid sftp packet length")
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, nil, err
	}
	return buf[0], buf[1:], nil
}

func sendSFTPPacket(w io.Writer, typ byte, payload []byte) error {
	length := uint32(1 + len(payload))
	var hdr [5]byte
	binary.BigEndian.PutUint32(hdr[0:4], length)
	hdr[4] = typ
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

func sendSFTPStatus(w io.Writer, id, code uint32, msg string) error {
	var buf bytes.Buffer
	putUint32(&buf, id)
	putUint32(&buf, code)
	putSFTPString(&buf, msg)
	putSFTPString(&buf, "")
	return sendSFTPPacket(w, sftpFXPStatus, buf.Bytes())
}

func sendSFTPHandle(w io.Writer, id uint32, handle string) error {
	var buf bytes.Buffer
	putUint32(&buf, id)
	putSFTPString(&buf, handle)
	return sendSFTPPacket(w, sftpFXPHandle, buf.Bytes())
}

func sendSFTPData(w io.Writer, id uint32, data []byte) error {
	var buf bytes.Buffer
	putUint32(&buf, id)
	putSFTPString(&buf, string(data))
	return sendSFTPPacket(w, sftpFXPData, buf.Bytes())
}

func sendSFTPName(w io.Writer, id uint32, entries []sftpNameEntry) error {
	var buf bytes.Buffer
	putUint32(&buf, id)
	putUint32(&buf, uint32(len(entries)))
	for _, e := range entries {
		putSFTPString(&buf, e.filename)
		putSFTPString(&buf, e.longname)
		e.attrs.marshal(&buf)
	}
	return sendSFTPPacket(w, sftpFXPName, buf.Bytes())
}

func sendSFTPAttrs(w io.Writer, id uint32, attrs sftpAttrs) error {
	var buf bytes.Buffer
	putUint32(&buf, id)
	attrs.marshal(&buf)
	return sendSFTPPacket(w, sftpFXPAttrs, buf.Bytes())
}
