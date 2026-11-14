package deployment

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type foregroundProviderSession struct {
	home string
	stop func() error
}

// ProviderProxyStartError identifies a local failure before the model test starts.
type ProviderProxyStartError struct {
	Err error
}

func (e *ProviderProxyStartError) Error() string { return e.Err.Error() }
func (e *ProviderProxyStartError) Unwrap() error { return e.Err }

// HostBunPath selects the Bun of the provider proxy. A published install
// replaces it with the host Bun of its release record.
var HostBunPath = func(home string) (string, error) { return exec.LookPath("bun") }

func foregroundProviderCommand(ctx context.Context, home, socketHome string) (*exec.Cmd, error) {
	binary, err := os.Executable()
	if err != nil {
		return nil, err
	}
	bun, err := HostBunPath(home)
	if err != nil {
		return nil, fmt.Errorf("The host Bun executable is unavailable.")
	}
	entry := filepath.Join(home, "release", "host", "resident", "src", "provider-foreground.ts")
	info, err := os.Lstat(entry)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("The host provider proxy release is unavailable.")
	}
	command := exec.CommandContext(ctx, bun, entry, "--home", home, "--socket-home", socketHome, "--mc", binary)
	for _, name := range []string{"HOME", "PATH", "USER", "LOGNAME", "TMPDIR", "MC_HELPER", "MC_IMAGE"} {
		if value, ok := os.LookupEnv(name); ok {
			command.Env = append(command.Env, name+"="+value)
		}
	}
	command.Env = append(command.Env, "MC_HOME="+home)
	return command, nil
}

func startForegroundProviderAccess(ctx context.Context, home string) (*foregroundProviderSession, error) {
	session, err := startForegroundProviderCommand(ctx, home, foregroundProviderCommand)
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return nil, &ProviderProxyStartError{Err: err}
	}
	return session, err
}

func startForegroundProviderCommand(ctx context.Context, home string,
	build func(context.Context, string, string) (*exec.Cmd, error)) (*foregroundProviderSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	socketHome, err := os.MkdirTemp("/tmp", "culler-proxy-")
	if err != nil {
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			_ = os.RemoveAll(socketHome)
		}
	}()
	command, err := build(ctx, home, socketHome)
	if err != nil {
		return nil, err
	}
	input, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := command.StdoutPipe()
	if err != nil {
		_ = input.Close()
		return nil, err
	}
	command.Stderr = io.Discard
	command.WaitDelay = 35 * time.Second
	command.Cancel = func() error { return command.Process.Signal(syscall.SIGTERM) }
	if err := command.Start(); err != nil {
		_ = input.Close()
		return nil, err
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait(); close(exited) }()
	var once sync.Once
	var stopError error
	stop := func() error {
		once.Do(func() {
			_ = input.Close()
			select {
			case err := <-exited:
				if err != nil && ctx.Err() == nil {
					stopError = fmt.Errorf("The temporary host could not finish credential cleanup. Restore credential access, then retry.")
				}
			case <-time.After(2 * time.Second):
				go func() {
					select {
					case <-exited:
					case <-time.After(33 * time.Second):
						_ = command.Process.Kill()
						<-exited
					}
					_ = os.RemoveAll(socketHome)
				}()
				return
			}
			stopError = errors.Join(stopError, os.RemoveAll(socketHome))
		})
		return stopError
	}
	ready := make(chan bool, 1)
	go func() {
		line, err := bufio.NewReaderSize(output, 1024).ReadSlice('\n')
		ready <- err == nil && string(line) == "{\"ready\":true}\n"
	}()
	timer := time.NewTimer(45 * time.Second)
	defer timer.Stop()
	select {
	case accepted := <-ready:
		if !accepted {
			_ = stop()
			return nil, fmt.Errorf("The temporary host provider proxy could not start.")
		}
	case <-ctx.Done():
		_ = stop()
		return nil, ctx.Err()
	case <-timer.C:
		_ = stop()
		return nil, fmt.Errorf("The temporary host provider proxy did not become ready.")
	}
	failed = false
	return &foregroundProviderSession{home: socketHome, stop: stop}, nil
}

func providerAccessSocketHome(ctx context.Context, home string) (string, func() error, error) {
	_, err := os.Lstat(filepath.Join(home, "health", "provider-acceptance.sock"))
	if err == nil {
		return home, func() error { return nil }, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", nil, fmt.Errorf("The resident provider access socket is unavailable.")
	}
	session, err := startForegroundProviderAccess(ctx, home)
	if err != nil {
		return "", nil, err
	}
	return session.home, session.stop, nil
}
