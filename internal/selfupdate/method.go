package selfupdate

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Method is how the running binary was installed, which decides how it can be
// updated.
type Method int

const (
	// MethodUnknown means the install could not be classified; the caller
	// falls back to `go install`.
	MethodUnknown Method = iota
	// MethodBrew means Homebrew manages the binary (`brew upgrade`).
	MethodBrew
	// MethodGoInstall means the binary lives in the Go bin dir and can be
	// refreshed with `go install`.
	MethodGoInstall
	// MethodScript means the binary was placed by install.sh (curl | sh).
	MethodScript
)

// String renders the method for reporting.
func (m Method) String() string {
	switch m {
	case MethodBrew:
		return "brew"
	case MethodGoInstall:
		return "go install"
	case MethodScript:
		return "install script"
	default:
		return "unknown"
	}
}

// DetectMethod classifies the running executable's install path:
//
//   - under a Homebrew Cellar → brew;
//   - inside GOBIN/GOPATH/bin → go install;
//   - otherwise → script (install.sh places it in /usr/local/bin or
//     ~/.local/bin, neither of which `go install` writes to).
//
// Resolution errors classify as MethodUnknown so the caller can still fall
// back to `go install`.
func DetectMethod() Method {
	exe, err := os.Executable()
	if err != nil {
		return MethodUnknown
	}

	// Resolve symlinks: Homebrew links `bin/<name>` to `../Cellar/<name>/...`,
	// and the unresolved path would not show the Cellar component.
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}

	if underCellar(exe) {
		return MethodBrew
	}

	for _, dir := range goBinDirs() {
		if dir == "" {
			continue
		}

		if rel, rerr := filepath.Rel(dir, exe); rerr == nil && !strings.HasPrefix(rel, "..") {
			return MethodGoInstall
		}
	}

	return MethodScript
}

// underCellar reports whether path has a Homebrew install component. Formulae
// land under Cellar, casks under Caskroom; the prefix varies (/opt/homebrew on
// Apple Silicon, /usr/local on Intel), so match on the component, not a fixed
// path.
func underCellar(path string) bool {
	parts := strings.Split(filepath.ToSlash(path), "/")

	return slices.Contains(parts, "Cellar") || slices.Contains(parts, "Caskroom")
}

// goBinDirs returns the directories `go install` writes to: GOBIN when set,
// else GOPATH/bin. Values come from the environment so no `go env` subprocess
// is needed on the hot path.
func goBinDirs() []string {
	if bin := os.Getenv("GOBIN"); bin != "" {
		return []string{bin}
	}

	var dirs []string

	if gp := os.Getenv("GOPATH"); gp != "" {
		for _, p := range filepath.SplitList(gp) {
			dirs = append(dirs, filepath.Join(p, "bin"))
		}
	} else if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "go", "bin"))
	}

	return dirs
}
