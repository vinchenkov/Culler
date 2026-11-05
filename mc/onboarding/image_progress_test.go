package onboarding

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestImageProgressDescribesActualBuildOperations(t *testing.T) {
	var messages []string
	output := imageProgressOutput{report: func(message string) { messages = append(messages, message) }}
	lines := []string{
		"setup-progress: compile-linux",
		"setup-progress: compile-dispatcher",
		"setup-progress: build-container",
		"#3 [internal] load metadata for docker.io/library/ubuntu:24.04",
		"#6 [project  2/14] RUN printf 'Types: deb\\nURIs: http://snapshot.ubuntu.com/ubuntu/%s/\\n' \"20261001T000000Z\" >/etc/apt/sources.list.d/ubuntu.sources  && apt-get -o Acquire::https::Verify-Peer=false update  && apt-get update",
		"#9 [project  5/14] RUN curl -fsSL \"https://github.com/astral-sh/uv/releases/download/0.12.3/uv-aarch64-unknown-linux-gnu.tar.gz\" -o /tmp/uv.tar.gz",
		"#10 [project  6/14] RUN curl -fsSL \"https://nodejs.org/dist/v24.18.0/node-v24.18.0-linux-arm64.tar.xz\" -o /tmp/node.tar.xz",
		"#10 CACHED",
		"#12 [project  8/14] RUN cd /opt/project-tools  && npm_config_cache=/tmp/npm-cache npm ci --omit=dev --ignore-scripts  && playwright-cli install-browser chrome-for-testing --with-deps",
		"#12 2.5 browser download output",
		"#12 DONE 50.2s",
		"#14 [project 10/14] RUN curl -fsSL \"https://go.dev/dl/go1.25.12.linux-arm64.tar.gz\" -o /tmp/go.tar.gz",
		"#15 [project 11/14] RUN curl -fsSL \"https://github.com/oven-sh/bun/releases/download/bun-v1.3.12/bun-linux-aarch64.zip\" -o /tmp/bun.zip",
		"#17 [project 13/14] RUN groupadd --gid 10001 mc-completion  && useradd --uid 10001 --gid 10001 --no-create-home --shell /usr/sbin/nologin mc-completion",
		"#21 [runtime  2/17] RUN install -d /opt/culler-runtime/bin /opt/culler-runtime/commands /opt/culler-runtime/libexec /opt/culler-runtime/app /usr/local/libexec  && ln -s ../node/bin/node /opt/culler-runtime/bin/node  && ln -s /opt/culler-runtime/libexec/mc-real /usr/local/libexec/mc-real",
		"#30 [tools 11/11] RUN if [ \"1\" = 1 ]; then cd /opt/culler-runtime/app && /opt/culler-runtime/bun/bin/bun install --production --frozen-lockfile --omit=optional --omit=peer; fi",
		"#34 [runtime 15/17] COPY --chown=10001:10001 --chmod=6755 runner/image/mc-prod-bin /opt/culler-runtime/libexec/mc-real",
		"#22 exporting to image",
		"setup-progress: check-browser",
	}
	for _, line := range lines {
		for _, part := range []string{line[:len(line)/2], line[len(line)/2:] + "\r\n"} {
			if _, err := output.Write([]byte(part)); err != nil {
				t.Fatal(err)
			}
		}
	}
	want := []string{
		"Compile Culler for Linux",
		"Compile the container command dispatcher",
		"Build the agent container",
		"Download the base container",
		"Install the system packages",
		"Install the Python tools",
		"Install the JavaScript runtime",
		"Install the JavaScript runtime (cached)",
		"Install the browser and its required software",
		"Install the browser and its required software (complete)",
		"Install the Go tools",
		"Install the application runtime",
		"Prepare the agent account",
		"Configure the agent container",
		"Install the agent tools",
		"Install Culler",
		"Save the agent container",
		"Check the browser",
	}
	if got := slices.Compact(messages); !slices.Equal(got, want) {
		t.Fatalf("Progress operations differ:\n%v\nExpected:\n%v", got, want)
	}
}

func TestImageProgressConcealsRawOutputAndContinuesAfterCaptureLimit(t *testing.T) {
	var messages []string
	output := imageProgressOutput{report: func(message string) { messages = append(messages, message) }}
	output.Write([]byte("setup-progress: build-container\n"))
	output.Write(bytes.Repeat([]byte("SECRET_VALUE"), 16*1024))
	output.Write([]byte("\n#7 [4/17] RUN curl https://nodejs.org/dist/SECRET_VALUE\n"))
	output.Write([]byte("#7 2.1 token=SECRET_VALUE\x1b]52;c;SECRET_VALUE\a\n"))
	output.Write([]byte("#8 [999999999999999999/999999999999999999] SECRET_VALUE\n"))
	output.Write([]byte("setup-progress: SECRET_VALUE\nsetup-progress: check-browser"))
	output.finish()
	if output.capture.Len() != 64*1024 {
		t.Fatalf("Capture size = %d", output.capture.Len())
	}
	if !slices.Contains(messages, "Check the browser") {
		t.Fatal("Progress stopped when the command output reached its capture limit.")
	}
	for _, message := range messages {
		if strings.Contains(message, "SECRET_VALUE") || strings.ContainsAny(message, "\x1b\a\r\n") || len(message) > 160 {
			t.Fatalf("The progress exposed command output: %q", message)
		}
	}
}

func TestImageProgressReportsOutputWithinTheCurrentOperation(t *testing.T) {
	var messages []string
	output := imageProgressOutput{report: func(message string) { messages = append(messages, message) }}
	output.Write([]byte("setup-progress: compile-linux\n"))
	before := len(messages)
	output.Write([]byte("unstructured compiler output"))
	if len(messages) != before+1 || messages[len(messages)-1] != "Compile Culler for Linux" {
		t.Fatalf("The current operation omitted fresh output: %v", messages)
	}
}

func TestImageBuildReportsProgressBeforeTheCommandExits(t *testing.T) {
	root := t.TempDir()
	acknowledged := filepath.Join(root, "progress-observed")
	script := filepath.Join(root, "build.sh")
	body := `#!/bin/sh
printf '%s\n' 'setup-progress: compile-linux' >&2
while [ ! -f "$1" ]; do sleep 0.01; done
printf '%s\n' '#7 [3/17] RUN curl https://nodejs.org/dist/SECRET_VALUE'
printf '%s' 'setup-progress: check-browser' >&2
exit 17
`
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var messages []string
	capture, err := runImageBuildCommand(ctx, root, nil, func(message string) {
		messages = append(messages, message)
		if message == "Compile Culler for Linux" {
			if err := os.WriteFile(acknowledged, nil, 0600); err != nil {
				t.Error(err)
			}
		}
	}, "sh", script, acknowledged)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 17 {
		t.Fatalf("The build did not receive progress acknowledgement: %v", err)
	}
	if !bytes.Contains(capture, []byte("SECRET_VALUE")) || strings.Contains(strings.Join(messages, "\n"), "SECRET_VALUE") {
		t.Fatal("The command capture and terminal progress lost their separate scopes.")
	}
	if !slices.Contains(messages, "Check the browser") {
		t.Fatalf("The final command update is absent: %v", messages)
	}
}

func TestImageBuildProgressPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := runImageBuildCommand(ctx, "", nil, func(string) {}, "sh", "-c", "sleep 2")
	if err == nil || time.Since(started) > time.Second {
		t.Fatalf("The command exceeded its deadline: %v", err)
	}
}
