package deployment

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestInstalledHostReleaseStartsTheForegroundProviderProxy(t *testing.T) {
	if _, err := exec.LookPath("bun"); err != nil {
		t.Fatal("Bun is required for the installed host proxy regression.")
	}
	source, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallHostRelease(home, source); err != nil {
		t.Fatal(err)
	}
	bundle, err := newProviderTLS(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := json.Marshal(bundle.ProviderProxyTLS)
	if err != nil {
		t.Fatal(err)
	}
	fixture := t.TempDir()
	if err := os.WriteFile(filepath.Join(fixture, "certificate.json"), certificate, 0600); err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(fixture, "mc")
	script := `#!/bin/sh
set -eu
fixture_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
printf '%s %s\n' "${1-}" "${2-}" >> "$fixture_dir/requests"
[ "$#" -eq 2 ] || exit 21
case "$1 $2" in
  "agent-profile runtime-catalog") printf '%s\n' '{"bindings":{},"connections":{}}' ;;
  "agent-profile proxy-tls") cat "$fixture_dir/certificate.json" ;;
  *) exit 22 ;;
esac
`
	if err := os.WriteFile(program, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	session, err := startForegroundProviderCommand(ctx, home, func(ctx context.Context, home, socketHome string) (*exec.Cmd, error) {
		command, err := foregroundProviderCommand(ctx, home, socketHome)
		if err != nil {
			return nil, err
		}
		if len(command.Args) != 8 || command.Args[6] != "--mc" {
			return nil, fmt.Errorf("The installed proxy command lost its host command argument.")
		}
		command.Args[7] = program
		return command, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.stop(); err != nil {
			t.Error(err)
		}
	})
	private, err := os.Stat(session.home)
	if err != nil || !private.IsDir() || private.Mode().Perm() != 0700 {
		t.Fatal("The installed proxy did not retain a private socket directory.")
	}
	socket := filepath.Join(session.home, "health", "provider-acceptance.sock")
	info, err := os.Lstat(socket)
	if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 {
		t.Fatal("The installed proxy did not create its private control socket.")
	}
	connection, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		t.Fatal("The installed proxy control socket refused a local connection.")
	}
	_ = connection.Close()
	registry := filepath.Join(home, "health", "provider-proxies")
	info, err = os.Stat(registry)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatal("The installed proxy registry is not private.")
	}
	entries, err := os.ReadDir(registry)
	if err != nil || len(entries) != 1 {
		t.Fatal("The installed proxy did not register its control socket.")
	}
	registration := filepath.Join(registry, entries[0].Name())
	info, err = os.Lstat(registration)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatal("The installed proxy registration is not a private regular file.")
	}
	var record struct {
		SocketHome string `json:"socket_home"`
	}
	body, err := os.ReadFile(registration)
	if err != nil || json.Unmarshal(body, &record) != nil {
		t.Fatal("The installed proxy registration is invalid.")
	}
	canonical, err := filepath.EvalSymlinks(session.home)
	if err != nil || record.SocketHome != canonical {
		t.Fatal("The installed proxy registered another socket directory.")
	}
	if err := session.stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(session.home); !os.IsNotExist(err) {
		t.Fatal("The installed proxy retained its socket directory after stop.")
	}
	entries, err = os.ReadDir(registry)
	if err != nil || len(entries) != 0 {
		t.Fatal("The installed proxy retained its registration after stop.")
	}
	requests, err := os.ReadFile(filepath.Join(fixture, "requests"))
	if err != nil || string(requests) != "agent-profile runtime-catalog\nagent-profile proxy-tls\n" {
		t.Fatal("The empty installed proxy requested credentials or another host operation.")
	}
	for _, path := range []string{"provider-proxy", "refresh-grants", "supervision"} {
		if _, err := os.Lstat(filepath.Join(home, path)); !os.IsNotExist(err) {
			t.Fatal("The installed proxy test created a credential or supervision store.")
		}
	}
}
