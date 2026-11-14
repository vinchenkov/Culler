//go:build !test_fake_routing

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func init() {
	setupPreviewCases["review-browser"] = func(t *testing.T) setupPreviewCase {
		w, _ := setupAgentFixture(t)
		result := setupPreviewCase{wizard: w, steps: []setupPreviewStep{}}
		result.run = func() {
			w.ui.Stage(3, setupStageTitles[2], setupStageIDs[:2])
			cfg, err := w.catalog()
			if err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			script := "#!/bin/sh\nprintf '%s\\n' 'https://auth.openai.com/oauth/authorize?response_type=code&client_id=fixture&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback&scope=openid+profile+email+offline_access&code_challenge=fixture&code_challenge_method=S256&id_token_add_organizations=true&codex_cli_simplified_flow=true&state=fixture&originator=codex_cli_rs'\nexit 1\n"
			if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin)
			err = setupConnect(w, setupConnection{Binding: setupBindingForKind(cfg, setupHarnessKind("codex")), Mode: "login"})
			if err == nil {
				t.Fatal("The fixture login must fail.")
			}
		}
		return result
	}
}
