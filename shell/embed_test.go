package shellscripts

import (
	"strings"
	"testing"
)

func TestGeneratedScriptsInjectBinaryPreludeWithoutFallback(t *testing.T) {
	tests := []struct {
		name       string
		script     string
		wantPrefix string
		noFallback string
		noCompat   string
	}{
		{
			name:       "bash",
			script:     Bash(`/tmp/workspace-launcher$bin"test`),
			wantPrefix: "__workspace_launcher_bin='",
			noFallback: `: "${__workspace_launcher_bin:=workspace-launcher}"`,
			noCompat:   `BASH_VERSINFO`,
		},
		{
			name:       "zsh",
			script:     Zsh(`/tmp/workspace-launcher$bin"test`),
			wantPrefix: "__workspace_launcher_bin='",
			noFallback: `: "${__workspace_launcher_bin:=workspace-launcher}"`,
		},
		{
			name:       "fish",
			script:     Fish(`/tmp/workspace-launcher$bin"test`),
			wantPrefix: `set -g __workspace_launcher_bin "`,
			noFallback: `set -q __workspace_launcher_bin; or set -g __workspace_launcher_bin workspace-launcher`,
		},
	}

	for _, tt := range tests {
		if !strings.HasPrefix(tt.script, tt.wantPrefix) {
			t.Fatalf("%s script missing injected prelude: %q", tt.name, tt.script)
		}
		if strings.Contains(tt.script, tt.noFallback) {
			t.Fatalf("%s script still contains fallback logic: %q", tt.name, tt.script)
		}
		if tt.noCompat != "" && strings.Contains(tt.script, tt.noCompat) {
			t.Fatalf("%s script still contains compatibility fallback logic: %q", tt.name, tt.script)
		}
	}
}

func TestBashScriptUsesAcceptLineRefreshFlow(t *testing.T) {
	script := Bash("/tmp/workspace-launcher")
	for _, want := range []string{
		`__workspace_launcher_widget_key='\C-x\C-_W1\a'`,
		`__workspace_launcher_accept_key='\C-x\C-_W0\a'`,
		`accept-line`,
		`$__workspace_launcher_widget_key$__workspace_launcher_accept_key`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("bash script missing %q in %q", want, script)
		}
	}
}
