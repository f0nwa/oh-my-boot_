package unattend

import "fmt"

// ConfigurationError is a setting the generator cannot use, the Go
// counterpart of ConfigurationException; its message is the same text.
type ConfigurationError struct{ Message string }

func (e *ConfigurationError) Error() string { return e.Message }

func configError(format string, args ...any) error {
	if len(args) == 0 {
		return &ConfigurationError{Message: format}
	}
	return &ConfigurationError{Message: fmt.Sprintf(format, args...)}
}
