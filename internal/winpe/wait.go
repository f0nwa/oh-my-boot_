package winpe

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
)

// Limits of the generated WinPE wait loop. Control validates the operator's
// seconds; these bounds only protect the script from nonsensical numbers.
const (
	maxWaitAttempts = 600
	maxWaitPause    = 30
)

var (
	setupWaitLimit = regexp.MustCompile(`\r\nif %OMB_ATTEMPT% GEQ ([0-9]+) goto connect_final\r\n`)
	setupWaitEcho  = regexp.MustCompile(`retrying %OMB_ATTEMPT%/([0-9]+)\.\.\.\r\n`)
	setupWaitPause = regexp.MustCompile(`\r\nping -n ([0-9]+) 127\.0\.0\.1 >nul\r\ngoto connect_media\r\n`)
	strelecWait    = regexp.MustCompile(`\r\nif %OMB_STRELEC_ATTEMPT% GEQ ([0-9]+) goto omb_strelec_failed\r\nping -n ([0-9]+) 127\.0\.0\.1 >nul\r\ngoto omb_strelec_connect\r\n`)
)

// UpdateStartupWait sets the number of installation-share attempts and the
// pause between them in a prepared WinPE startup script. Windows Setup and
// Strelec scripts are recognised by their loop labels; a script without such
// a loop is returned unchanged. A loop that cannot be matched exactly once is
// an error, so a hand-edited script is never half-updated.
func UpdateStartupWait(script []byte, attempts, pause int) ([]byte, bool, error) {
	if attempts < 1 || attempts > maxWaitAttempts || pause < 1 || pause > maxWaitPause {
		return nil, false, fmt.Errorf("invalid WinPE wait: %d attempts, %d s pause", attempts, pause)
	}
	count := []byte(strconv.Itoa(attempts))
	ping := []byte(strconv.Itoa(pause + 1)) // ping -n N waits N-1 seconds
	out := script
	var err error
	if bytes.Contains(script, []byte("\r\n:connect_media\r\n")) {
		for _, step := range []struct {
			pattern *regexp.Regexp
			value   []byte
		}{{setupWaitLimit, count}, {setupWaitEcho, count}, {setupWaitPause, ping}} {
			if out, err = replaceGroups(out, step.pattern, step.value); err != nil {
				return nil, false, err
			}
		}
	}
	if bytes.Contains(script, []byte("\r\n:omb_strelec_connect\r\n")) {
		if out, err = replaceGroups(out, strelecWait, count, ping); err != nil {
			return nil, false, err
		}
	}
	return out, !bytes.Equal(out, script), nil
}

// WaitFromScript reads the Windows Setup wait loop, so newly prepared scripts
// (for example Strelec) can follow the values of the current template.
func WaitFromScript(script []byte) (attempts, pause int, ok bool) {
	limit := setupWaitLimit.FindAllSubmatch(script, -1)
	delay := setupWaitPause.FindAllSubmatch(script, -1)
	if len(limit) != 1 || len(delay) != 1 {
		return 0, 0, false
	}
	attempts, err1 := strconv.Atoi(string(limit[0][1]))
	ping, err2 := strconv.Atoi(string(delay[0][1]))
	if err1 != nil || err2 != nil || attempts < 1 || attempts > maxWaitAttempts || ping < 2 || ping > maxWaitPause+1 {
		return 0, 0, false
	}
	return attempts, ping - 1, true
}

// replaceGroups replaces the numbered groups of the only match of pattern.
func replaceGroups(script []byte, pattern *regexp.Regexp, values ...[]byte) ([]byte, error) {
	matches := pattern.FindAllSubmatchIndex(script, -1)
	if len(matches) != 1 || len(matches[0]) != 2+2*len(values) {
		return nil, fmt.Errorf("WinPE wait loop is missing or ambiguous")
	}
	match := matches[0]
	var out bytes.Buffer
	last := 0
	for i, value := range values {
		start, end := match[2+2*i], match[3+2*i]
		out.Write(script[last:start])
		out.Write(value)
		last = end
	}
	out.Write(script[last:])
	return out.Bytes(), nil
}
