// Package ueficausb builds the "enable the Microsoft UEFI CA key" WinPE USB
// stick: the scripts that run on it and the archive made from a profile ISO.
package ueficausb

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
)

//go:embed files
var embedded embed.FS

// File is one file added to the stick.
type File struct {
	Name string
	Data []byte
}

// wimFileNames are placed into Windows/System32 of the stick's boot.wim.
var wimFileNames = []string{"winpeshl.ini", "uefica.cmd", "uefica.vbs"}

func mustRead(name string) []byte {
	data, err := embedded.ReadFile("files/" + name)
	if err != nil {
		panic(err)
	}
	return data
}

// WIMFiles returns the files for Windows/System32 of boot.wim, winpeshl.ini first.
func WIMFiles() []File {
	files := make([]File, 0, len(wimFileNames))
	for _, name := range wimFileNames {
		files = append(files, File{Name: name, Data: mustRead(name)})
	}
	return files
}

// ReadMe returns README.txt for the root of the stick.
func ReadMe() []byte { return mustRead("README.txt") }

// ScriptsVersion identifies the embedded files; it changes whenever any of them does.
func ScriptsVersion() string {
	h := sha256.New()
	for _, f := range append(WIMFiles(), File{Name: "README.txt", Data: ReadMe()}) {
		h.Write([]byte(f.Name))
		h.Write([]byte{0})
		h.Write(f.Data)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
