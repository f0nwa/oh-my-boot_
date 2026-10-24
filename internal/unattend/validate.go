package unattend

import (
	"fmt"
	"strings"
)

// validateConfig repeats the checks the original makes in constructors of
// settings records, with the same messages, so a bad setting fails the same
// way.
func validateConfig(d *Data, c Config) error {
	var err error
	try := func(e error) {
		if err == nil {
			err = e
		}
	}
	if c.ActivationKey != nil {
		try(checkProductKey(c.ActivationKey.Value))
	}
	if pe, ok := c.PESettings.(DefaultPESettings); ok {
		if ce, ok := pe.EditionSettings.(CustomEditionSettings); ok {
			try(checkProductKey(ce.ProductKey.Value))
		}
	}
	if pe, ok := c.PESettings.(GeneratePESettings); ok {
		if ps, ok := pe.PartitionSettings.(UnattendedPartitionSettings); ok {
			if g, ok := ps.TargetDiskSettings.(GeneratedTargetDiskSettings); ok && g.MinSizeGiB != nil && g.MaxSizeGiB != nil && *g.MinSizeGiB > *g.MaxSizeGiB {
				try(configError("Value of 'minSizeGiB' (%d) must be less than or equal to value of 'maxSizeGiB' (%d).", *g.MinSizeGiB, *g.MaxSizeGiB))
			}
		}
	}
	if a, ok := c.AccountSettings.(UnattendedAccountSettings); ok {
		for _, acc := range a.Accounts {
			try(checkUsername(acc.Name))
		}
		try(checkAccounts(a))
	}
	if cn, ok := c.ComputerNameSettings.(CustomComputerNameSettings); ok {
		try(checkComputerName(cn.Name))
	}
	if l, ok := c.LockoutSettings.(CustomLockoutSettings); ok {
		threshold, e1 := inRange(l.LockoutThreshold, 0, 999, "lockoutThreshold")
		duration, e2 := inRange(l.LockoutDuration, 1, 99_999, "lockoutDuration")
		window, e3 := inRange(l.LockoutWindow, 1, 99_999, "lockoutWindow")
		try(e1)
		try(e2)
		try(e3)
		if e1 == nil && e2 == nil && e3 == nil && window > duration {
			try(configError("Value of 'LockoutWindow' (%d) must be less than or equal to value of 'LockoutDuration' (%d).", window, duration))
		}
		_ = threshold
	}
	for _, s := range c.ScriptSettings.Scripts {
		if s.Phase == "DefaultUser" && s.Type == "Vbs" {
			try(configError("Scripts in phase '%s' must not have type '%s'.", s.Phase, s.Type))
		}
	}
	return err
}

func checkProductKey(key string) error {
	if !productKeyPattern.MatchString(key) {
		return configError("Product key '%s' is ill-formed.", key)
	}
	return nil
}

// inRange is Validation.InRange.
func inRange(v *int, min, max int, field string) (int, error) {
	if v == nil {
		return 0, configError("Parameter '%s' must be set.", field)
	}
	if *v < min {
		return 0, configError("Value of parameter '%s' must not be less than %d, but was %d.", field, min, *v)
	}
	if *v > max {
		return 0, configError("Value of parameter '%s' must not be greater than %d, but was %d.", field, max, *v)
	}
	return *v, nil
}

// checkUsername is Account.ValidateUsername.
func checkUsername(name string) error {
	bad := strings.TrimSpace(name) == "" || name != strings.TrimFunc(name, isSpace) || len([]rune(name)) > 20 ||
		strings.ContainsAny(name, `/\[]:;|=,+*?<>"%`) || strings.HasSuffix(name, ".")
	for _, reserved := range []string{"administrator", "guest", "defaultaccount", "system", "network service", "local service", "none", "wdagutilityaccount"} {
		bad = bad || strings.EqualFold(name, reserved)
	}
	if bad {
		return configError("Username '%s' is invalid.", name)
	}
	return nil
}

// checkAccounts is the constructor of UnattendedAccountSettings.
func checkAccounts(a UnattendedAccountSettings) error {
	if _, builtin := a.AutoLogonSettings.(BuiltinAutoLogonSettings); !builtin {
		admin := false
		for _, acc := range a.Accounts {
			admin = admin || acc.Group == "Administrators"
		}
		if !admin {
			return configError("Must have at least one administrator account.")
		}
	}
	// Names that occur more than once, in order of first occurrence.
	var order []string
	count := map[string]int{}
	first := map[string]string{}
	for _, acc := range a.Accounts {
		k := strings.ToLower(acc.Name)
		if count[k] == 0 {
			order = append(order, k)
			first[k] = acc.Name
		}
		count[k]++
	}
	var dup []string
	for _, k := range order {
		if count[k] > 1 {
			dup = append(dup, fmt.Sprintf("'%s'", first[k]))
		}
	}
	if len(dup) > 0 {
		return configError("Account name(s) %s specified more than once.", strings.Join(dup, ", "))
	}
	return nil
}

// checkComputerName is the constructor of CustomComputerNameSettings.
func checkComputerName(name string) error {
	bad := strings.TrimFunc(name, isSpace) == "" || len([]rune(name)) > 15 || strings.IndexFunc(name, isSpace) >= 0 ||
		strings.ContainsAny(name, "{|}~[\\]^':;<=>?@!\"#$%`()+/.,*&")
	allDigits := name != ""
	for _, r := range name {
		allDigits = allDigits && r >= '0' && r <= '9'
	}
	if bad || allDigits {
		return configError("Computer name '%s' is invalid.", name)
	}
	return nil
}
