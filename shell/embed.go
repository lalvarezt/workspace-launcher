package shellscripts

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed key-bindings.bash
var bashScript string

//go:embed key-bindings.zsh
var zshScript string

//go:embed key-bindings.fish
var fishScript string

func Bash(binPath string) string {
	return fmt.Sprintf("__workspace_launcher_bin=%s\n\n%s", shellQuote(binPath), bashScript)
}

func Zsh(binPath string) string {
	return fmt.Sprintf("__workspace_launcher_bin=%s\n\n%s", shellQuote(binPath), zshScript)
}

func Fish(binPath string) string {
	return fmt.Sprintf("set -g __workspace_launcher_bin %s\n\n%s", fishQuote(binPath), fishScript)
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func fishQuote(value string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		`$`, `\$`,
	)
	return `"` + replacer.Replace(value) + `"`
}
