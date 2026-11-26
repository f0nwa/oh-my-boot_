package control

import (
	"fmt"
	"strings"

	"omb/internal/agent"
)

// WIMPairCompat describes the compatibility between a boot.wim environment and
// an install.wim/esd image (with or without a base Windows ISO).
type WIMPairCompat struct {
	Status   string   `json:"status"` // "compatible" | "warning" | "incompatible"
	Warnings []string `json:"warnings,omitempty"`
	Errors   []string `json:"errors,omitempty"`
}

// CheckBootInstallCompat validates whether boot.wim can deploy install.wim/esd.
//
// Blocking errors ("incompatible"):
//   - boot.wim lacks setup.exe (pure WinPE/ADK) and there is no base Windows ISO;
//   - boot.wim and install.wim architectures do not overlap.
//
// Non-blocking warning ("warning"):
//   - boot.wim OS build is lower than install.wim OS build.
func CheckBootInstallCompat(boot WindowsBootImage, install agent.WindowsWIMInfo, hasBaseISO bool) WIMPairCompat {
	res := WIMPairCompat{Status: "compatible"}

	if !boot.HasSetupExe && !hasBaseISO {
		res.Errors = append(res.Errors, "Выбранный boot.wim не содержит setup.exe (только WinPE) и требует базовый Windows ISO")
	}

	bootArchs := uniqueNormalizedWIMArchs(boot.WIMInfo.Images)
	installArchs := uniqueNormalizedWIMArchs(install.Images)
	if len(bootArchs) > 0 && len(installArchs) > 0 && !hasArchOverlap(bootArchs, installArchs) {
		res.Errors = append(res.Errors, fmt.Sprintf(
			"Несовпадение архитектуры: boot.wim (%s) и install.wim (%s)",
			strings.Join(bootArchs, ", "),
			strings.Join(installArchs, ", "),
		))
	}

	bootBuild := maxKnownWIMBuild(boot.WIMInfo.Images)
	installBuild := maxKnownWIMBuild(install.Images)
	if bootBuild > 0 && installBuild > 0 && bootBuild < installBuild {
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"Версия boot.wim (build %d) ниже версии install.wim (build %d); возможны ошибки установки",
			bootBuild, installBuild,
		))
	}

	switch {
	case len(res.Errors) > 0:
		res.Status = "incompatible"
	case len(res.Warnings) > 0:
		res.Status = "warning"
	}
	return res
}

func uniqueNormalizedWIMArchs(images []agent.WindowsWIMImage) []string {
	var out []string
	for _, img := range images {
		if norm := normalizeAnswerArch(img.Architecture); norm != "" && !containsString(out, norm) {
			out = append(out, norm)
		}
	}
	return out
}

func hasArchOverlap(a, b []string) bool {
	for _, x := range a {
		if containsString(b, x) {
			return true
		}
	}
	return false
}

func maxKnownWIMBuild(images []agent.WindowsWIMImage) int {
	best := 0
	for _, img := range images {
		if img.Build > best {
			best = img.Build
		}
	}
	return best
}
