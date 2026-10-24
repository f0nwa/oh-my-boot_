package unattend

import "unicode"

// compareCulture orders strings like String.CompareTo in .NET with ICU (the
// default, culture-sensitive comparison) for the ASCII text the generator
// sorts: punctuation and spaces before digits, digits before letters,
// letters regardless of case; on a tie lowercase comes before uppercase, and
// a shorter string before a longer one.
func compareCulture(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	for i := 0; i < len(ra) && i < len(rb); i++ {
		if c := primary(ra[i]) - primary(rb[i]); c != 0 {
			return sign(c)
		}
	}
	if len(ra) != len(rb) {
		return sign(len(ra) - len(rb))
	}
	for i := range ra {
		if ra[i] != rb[i] {
			// Same letter in another case: lowercase first.
			if unicode.IsLower(ra[i]) {
				return -1
			}
			return 1
		}
	}
	return 0
}

func primary(r rune) int {
	switch {
	case unicode.IsLetter(r):
		return 2000 + int(unicode.ToLower(r))
	case unicode.IsDigit(r):
		return 1000 + int(r)
	}
	return int(r)
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}
