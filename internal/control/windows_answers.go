package control

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"unicode/utf8"
)

const maxWindowsAnswerBytes = 1 << 20
const windowsAnswerNamespace = "urn:schemas-microsoft-com:unattend"

type WindowsAnswerVariant struct {
	ID        int64
	ProfileID int64
	Title     string
	Folder    string
	SHA256    string
	Size      int64
	State     string
	Error     string
}

// ValidateWindowsAnswer checks transport-level XML safety, not Windows Setup semantics.
func ValidateWindowsAnswer(data []byte) error {
	if len(data) == 0 || len(data) > maxWindowsAnswerBytes || !utf8.Valid(data) {
		return fmt.Errorf("invalid answer file size or encoding")
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	depth, roots := 0, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("invalid answer file XML")
		}
		switch t := token.(type) {
		case xml.Directive:
			return fmt.Errorf("XML directives are not allowed")
		case xml.StartElement:
			if depth == 0 {
				roots++
				if roots != 1 || t.Name.Local != "unattend" || t.Name.Space != windowsAnswerNamespace {
					return fmt.Errorf("expected Windows unattend root element")
				}
			}
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && len(bytes.TrimSpace(t)) > 0 {
				return fmt.Errorf("text outside answer file root")
			}
		}
	}
	if roots != 1 || depth != 0 {
		return fmt.Errorf("answer file root is missing")
	}
	return nil
}
