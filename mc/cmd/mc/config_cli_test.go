package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	operatorconfig "mc/config"
)

func TestConfigCommandsRequireHostScope(t *testing.T) {
	home, spine, _ := agentSurfaceHome(t)
	env := append(runJSONEnv(t, spine, "config-agent", "pipeline", "worker"), "MC_HOME="+home)
	for _, args := range [][]string{{"config", "show"}, {"config", "save"}, {"__config-settings"},
		{"discord", "show"}, {"discord", "connect"}, {"__discord-config"}, {"__discord-state", "apply"}} {
		result := runMC(t, env, "{}", args...)
		if result.code != 1 || !strings.Contains(result.stderr, "host scope only") {
			t.Fatalf("Scope check: %v %d %s", args, result.code, result.stderr)
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("An agent request created the home")
	}
}

func TestConfigSaveCommandPersistsAndRejectsMalformedRequests(t *testing.T) {
	spine := initSpine(t)
	home := filepath.Dir(spine)
	path := filepath.Join(home, "config.toml")
	if _, err := operatorconfig.WriteAtomic(path, operatorconfig.Default()); err != nil {
		t.Fatal(err)
	}
	env := spineEnv(spine)
	request := `{"changes":[{"key":"timing.backup_retain","expected":12,"value":8}]}`
	result := runMC(t, env, request, "config", "save")
	if result.code != 0 {
		t.Fatalf("Save: %s", result.stderr)
	}
	cfg, err := operatorconfig.Load(path)
	if err != nil || cfg.Timing.BackupRetain != 8 {
		t.Fatalf("Saved configuration: %v %v", cfg.Timing.BackupRetain, err)
	}
	for _, input := range []string{request + ` {}`, `{"unknown":true}`, `{"changes":[{"key":"timing.backup_retain","value":15}]}`} {
		result := runMC(t, env, input, "config", "save")
		if result.code != 2 {
			t.Fatalf("Invalid request: %d %s", result.code, result.stderr)
		}
	}
}
