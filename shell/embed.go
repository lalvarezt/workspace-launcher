package shellscripts

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed key-bindings.bash
var bashScript string

//go:embed bindings.bash
var bashBindings string

//go:embed key-bindings.zsh
var zshScript string

//go:embed bindings.zsh
var zshBindings string

//go:embed key-bindings.fish
var fishScript string

//go:embed bindings.fish
var fishBindings string

func Bash(binPath string, bindings bool) string {
	return renderShellScript(fmt.Sprintf("__workspace_launcher_bin=%s", shellQuote(binPath)), bashScript, bashBindings, bindings)
}

func Zsh(binPath string, bindings bool) string {
	return renderShellScript(fmt.Sprintf("__workspace_launcher_bin=%s", shellQuote(binPath)), zshScript, zshBindings, bindings)
}

func Fish(binPath string, bindings bool) string {
	return renderShellScript(fmt.Sprintf("set -g __workspace_launcher_bin %s", fishQuote(binPath)), fishScript, fishBindings, bindings)
}

func renderShellScript(prelude, script, bindingsScript string, includeBindings bool) string {
	parts := []string{prelude, script}
	if includeBindings {
		parts = append(parts, bindingsScript)
	}
	return strings.Join(parts, "\n\n")
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
