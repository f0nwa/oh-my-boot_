// Package autoinstall builds the supported Ubuntu Desktop NoCloud configuration.
package autoinstall

import (
	"errors"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata" // Keep timezone validation available on minimal Control hosts.
	"unicode"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// Form is transient input. Password must never be persisted or returned to a client.
type Form struct {
	Locale, Keyboard, TimeZone, Hostname, Username, Password, DiskMode, DiskSerial string

	RealName, KeyboardVariant, KeyboardToggle, SourceID, PostInstallScript, Shutdown string
	Drivers, Codecs                                                                  bool
	Packages                                                                         []string
	Network                                                                          NetworkValues
	SSH                                                                              SSHValues
	MirrorMode, MirrorURL                                                            string
}

// Values contains normalized configuration suitable for immutable storage.
// PasswordHash contains a salted yescrypt crypt string, never a plaintext password.
type Values struct {
	Locale, Keyboard, TimeZone, Hostname, Username, PasswordHash, DiskMode, DiskSerial string

	RealName, KeyboardVariant, KeyboardToggle, SourceID, PostInstallScript, Shutdown string
	Drivers, Codecs                                                                  bool
	Packages                                                                         []string
	Network                                                                          NetworkValues
	SSH                                                                              SSHValues
	MirrorMode, MirrorURL                                                            string
}

var (
	localePattern   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.@-]{0,63}$`)
	keyboardPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	hostnamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	usernamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	timezonePattern = regexp.MustCompile(`^[A-Za-z0-9_+/-]{1,64}$`)
)

// Compile validates the form, hashes its password and renders the normalized values.
func Compile(form Form) (Values, []byte, error) {
	values, err := Normalize(form)
	if err != nil {
		return Values{}, nil, err
	}
	if len(form.Password) == 0 || len(form.Password) > 1024 || !validText(form.Password) {
		return Values{}, nil, errors.New("password: required, at most 1024 bytes, without control characters")
	}
	hash, err := hashPassword(form.Password)
	if err != nil {
		return Values{}, nil, err
	}
	values.PasswordHash = hash
	data, err := Render(values)
	if err != nil {
		return Values{}, nil, err
	}
	return values, data, nil
}

// Normalize validates constructor input without hashing or retaining its password.
func Normalize(form Form) (Values, error) {
	for _, field := range []struct{ name, value string }{
		{"locale", form.Locale}, {"keyboard", form.Keyboard}, {"timezone", form.TimeZone},
		{"hostname", form.Hostname}, {"username", form.Username}, {"disk mode", form.DiskMode},
	} {
		if !validText(field.value) {
			return Values{}, errors.New(field.name + ": control characters or invalid UTF-8 are not allowed")
		}
	}
	values := Values{
		Locale: strings.TrimSpace(form.Locale), Keyboard: strings.TrimSpace(form.Keyboard),
		TimeZone: strings.TrimSpace(form.TimeZone), Hostname: strings.TrimSpace(form.Hostname),
		Username: strings.TrimSpace(form.Username), DiskMode: strings.TrimSpace(form.DiskMode), DiskSerial: form.DiskSerial,
	}
	values.RealName = strings.TrimSpace(form.RealName)
	values.KeyboardVariant = strings.TrimSpace(form.KeyboardVariant)
	values.KeyboardToggle = strings.TrimSpace(form.KeyboardToggle)
	values.SourceID = strings.TrimSpace(form.SourceID)
	values.PostInstallScript = form.PostInstallScript
	values.Shutdown = strings.TrimSpace(form.Shutdown)
	values.Drivers, values.Codecs = form.Drivers, form.Codecs
	values.Packages = uniqueStrings(form.Packages)
	values.Network = normalizeNetwork(form.Network)
	values.SSH = normalizeSSH(form.SSH)
	values.MirrorMode, values.MirrorURL = strings.TrimSpace(form.MirrorMode), strings.TrimSpace(form.MirrorURL)
	if values.MirrorMode != "custom" {
		values.MirrorURL = ""
	}
	if err := validateFields(values); err != nil {
		return Values{}, err
	}
	return values, nil
}

// Validate checks stored values without accepting or requiring a plaintext password.
func Validate(values Values) error {
	if err := validateFields(values); err != nil {
		return err
	}
	if !passwordHashPattern.MatchString(values.PasswordHash) {
		return errors.New("password: invalid yescrypt hash")
	}
	return nil
}

func validateFields(v Values) error {
	if !localePattern.MatchString(v.Locale) {
		return errors.New("locale: required, at most 64 bytes, using letters, digits, _, ., @ or -")
	}
	if err := validateDesktop(v); err != nil {
		return err
	}
	if err := validateNetwork(v.Network); err != nil {
		return err
	}
	if err := validateRepository(v); err != nil {
		return err
	}
	if err := validateSSH(v.SSH); err != nil {
		return err
	}
	if !hostnamePattern.MatchString(v.Hostname) {
		return errors.New("hostname: required DNS label of at most 63 bytes")
	}
	if !usernamePattern.MatchString(v.Username) {
		return errors.New("username: required Linux account name of at most 32 bytes")
	}
	if !timezonePattern.MatchString(v.TimeZone) || v.TimeZone == "Local" {
		return errors.New("timezone: required IANA time zone of at most 64 bytes")
	}
	if _, err := time.LoadLocation(v.TimeZone); err != nil {
		return errors.New("timezone: unknown IANA time zone")
	}
	switch v.DiskMode {
	case "interactive":
		if v.DiskSerial != "" {
			return errors.New("disk serial: must be empty in interactive mode")
		}
	case "serial":
		if len(v.DiskSerial) == 0 || len(v.DiskSerial) > 128 || strings.TrimSpace(v.DiskSerial) != v.DiskSerial || !validText(v.DiskSerial) || strings.ContainsAny(v.DiskSerial, "*?[]") {
			return errors.New("disk serial: exact value of 1 to 128 bytes required, without glob or control characters or surrounding whitespace")
		}
	default:
		return errors.New("disk mode: must be interactive or serial")
	}
	return nil
}

func validText(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

type keyboard struct {
	Layout  string `yaml:"layout"`
	Variant string `yaml:"variant,omitempty"`
	Toggle  string `yaml:"toggle,omitempty"`
}
type identity struct {
	Hostname string `yaml:"hostname"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	RealName string `yaml:"realname,omitempty"`
}
type diskMatch struct {
	Serial string `yaml:"serial"`
}
type layout struct {
	Name  string    `yaml:"name"`
	Match diskMatch `yaml:"match"`
}
type storage struct {
	Layout layout `yaml:"layout"`
}
type config struct {
	Version          int             `yaml:"version"`
	Interactive      []string        `yaml:"interactive-sections,omitempty"`
	Storage          *storage        `yaml:"storage,omitempty"`
	Identity         identity        `yaml:"identity"`
	Locale           string          `yaml:"locale"`
	Keyboard         keyboard        `yaml:"keyboard"`
	TimeZone         string          `yaml:"timezone"`
	Source           *desktopSource  `yaml:"source,omitempty"`
	Drivers          *desktopInstall `yaml:"drivers,omitempty"`
	Codecs           *desktopInstall `yaml:"codecs,omitempty"`
	Packages         []string        `yaml:"packages,omitempty"`
	LateCommands     [][]string      `yaml:"late-commands,omitempty"`
	Shutdown         string          `yaml:"shutdown,omitempty"`
	Network          map[string]any  `yaml:"network,omitempty"`
	APT              map[string]any  `yaml:"apt,omitempty"`
	SSH              *SSHValues      `yaml:"ssh,omitempty"`
	RefreshInstaller *struct {
		Update bool `yaml:"update"`
	} `yaml:"refresh-installer,omitempty"`
}
type document struct {
	Autoinstall config `yaml:"autoinstall"`
}

// Render deterministically generates user-data from validated stored values.
func Render(values Values) ([]byte, error) {
	if err := Validate(values); err != nil {
		return nil, err
	}
	ai := config{
		Version: 1, Identity: identity{Hostname: values.Hostname, Username: values.Username, Password: values.PasswordHash, RealName: values.RealName},
		Locale: values.Locale, Keyboard: keyboard{Layout: values.Keyboard, Variant: values.KeyboardVariant, Toggle: values.KeyboardToggle}, TimeZone: values.TimeZone,
	}
	if values.DiskMode == "interactive" {
		ai.Interactive = []string{"storage"}
	} else {
		ai.Storage = &storage{Layout: layout{Name: "direct", Match: diskMatch{Serial: values.DiskSerial}}}
	}
	applyDesktop(&ai, values)
	ai.Network = networkConfig(values.Network)
	ai.APT = repositoryConfig(values)
	if values.SSH.InstallServer {
		ssh := values.SSH
		ai.SSH = &ssh
	}
	if values.MirrorMode == "offline" {
		ai.RefreshInstaller = &struct {
			Update bool `yaml:"update"`
		}{false}
	}
	encoded, err := yaml.Marshal(document{Autoinstall: ai})
	if err != nil {
		return nil, errors.New("user-data: could not encode configuration")
	}
	data := append([]byte("#cloud-config\n"), encoded...)
	if len(data) >= 64*1024 {
		return nil, errors.New("user-data: must be smaller than 64 KiB")
	}
	return data, nil
}
