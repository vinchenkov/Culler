//go:build !test_fake_routing && (darwin || linux)

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"mc/onboarding"
)

func TestTerminalAgentSkillOfferScreens(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	for _, columns := range []int{80, 40} {
		for _, color := range []bool{true, false} {
			for _, variant := range []string{"add", "conflict", "skip"} {
				t.Run(fmt.Sprintf("%s-%d-color-%t", variant, columns, color), func(t *testing.T) {
					if color {
						value, set := os.LookupEnv("NO_COLOR")
						_ = os.Unsetenv("NO_COLOR")
						t.Cleanup(func() {
							if set {
								_ = os.Setenv("NO_COLOR", value)
							} else {
								_ = os.Unsetenv("NO_COLOR")
							}
						})
					} else {
						t.Setenv("NO_COLOR", "1")
					}
					master, slave := setupTerminalPair(t)
					if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(columns), Row: 24}); err != nil {
						t.Fatal(err)
					}
					w, _ := setupTestWizard(t)
					w.operatorHome = t.TempDir()
					skill := filepath.Join(w.home, "release", "host", "skills", "culler")
					if err := os.MkdirAll(skill, 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("skill"), 0600); err != nil {
						t.Fatal(err)
					}
					link := filepath.Join(w.operatorHome, ".claude", "skills", "culler")
					if variant == "conflict" {
						if err := os.MkdirAll(link, 0700); err != nil {
							t.Fatal(err)
						}
					}
					terminal := onboarding.NewTerminal(slave, slave)
					w.ui, w.input, w.output = terminal, slave, terminal
					done := make(chan error, 1)
					go func() { done <- w.offerAgentSkillIntegration() }()
					session := &setupScreenSession{t: t, master: master, screen: newSetupScreen(columns, 24)}
					var evidence strings.Builder
					lines := session.wait("q exit")
					fmt.Fprintf(&evidence, "Skill offer\n%s\n\n", strings.Join(lines, "\n"))
					if variant == "skip" {
						session.send("\x1b[B")
					}
					session.send("\r")
					select {
					case err := <-done:
						if err != nil {
							t.Fatal(err)
						}
					case <-time.After(3 * time.Second):
						t.Fatal("the skill choice did not complete")
					}
					for session.read(150 * time.Millisecond) {
					}
					fmt.Fprintf(&evidence, "Choice result\n%s\n", strings.Join(session.screen.lines(), "\n"))
					if !w.session.AgentSkillOffered {
						t.Fatal("the installer did not retain the choice")
					}
					if variant == "add" {
						if target, err := os.Readlink(link); err != nil || target != skill {
							t.Fatalf("installed link = %q, %v", target, err)
						}
					} else if variant == "skip" {
						if _, err := os.Lstat(link); !os.IsNotExist(err) {
							t.Fatalf("skip wrote a skill: %v", err)
						}
					} else if info, err := os.Lstat(link); err != nil || !info.IsDir() {
						t.Fatal("the unrelated skill changed")
					}
					if root := os.Getenv("MC_SETUP_EVIDENCE_DIR"); root != "" {
						if err := os.MkdirAll(root, 0700); err != nil {
							t.Fatal(err)
						}
						name := fmt.Sprintf("agent-skill-%s-%d-color-%t", variant, columns, color)
						if err := os.WriteFile(filepath.Join(root, name+".txt"), []byte(evidence.String()), 0600); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(root, name+".ansi"), []byte(session.raw.String()), 0600); err != nil {
							t.Fatal(err)
						}
					}
				})
			}
		}
	}
}
