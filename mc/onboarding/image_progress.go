package onboarding

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const imageProgressLineLimit = 8 * 1024

var imageBuildStep = regexp.MustCompile(`^#([0-9]+) \[([^\]]+)\] (.+)$`)

type imageProgressOutput struct {
	capture prepareOutput
	report  func(string)
	current string
	steps   map[string]string
	line    []byte
	discard bool
}

func reportImageProgress(report func(string), message string) {
	if report != nil {
		report(message)
	}
}

func (output *imageProgressOutput) update(message string) {
	output.current = message
	reportImageProgress(output.report, message)
}

func (output *imageProgressOutput) Write(data []byte) (int, error) {
	n, err := output.capture.Write(data)
	if len(data) > 0 && output.current != "" {
		reportImageProgress(output.report, output.current)
	}
	for _, value := range data {
		if value == '\n' || value == '\r' {
			if !output.discard && len(output.line) > 0 {
				output.consume(string(output.line))
			}
			output.line = output.line[:0]
			output.discard = false
			continue
		}
		if output.discard {
			continue
		}
		if len(output.line) == imageProgressLineLimit {
			output.line = output.line[:0]
			output.discard = true
			continue
		}
		output.line = append(output.line, value)
	}
	return n, err
}

func (output *imageProgressOutput) finish() {
	if !output.discard && len(output.line) > 0 {
		output.consume(string(output.line))
	}
	output.line = nil
}

func (output *imageProgressOutput) consume(line string) {
	switch line {
	case "setup-progress: compile-linux":
		output.update("Compile Culler for Linux")
		return
	case "setup-progress: compile-dispatcher":
		output.update("Compile the container command dispatcher")
		return
	case "setup-progress: build-container":
		output.update("Build the agent container")
		return
	case "setup-progress: check-browser":
		output.update("Check the browser")
		return
	}
	if match := imageBuildStep.FindStringSubmatch(line); match != nil {
		message := imageStepMessage(match[3])
		if output.steps == nil {
			output.steps = make(map[string]string)
		}
		if len(output.steps) < 256 {
			output.steps[match[1]] = message
		}
		output.update(message)
		return
	}
	if !strings.HasPrefix(line, "#") {
		return
	}
	vertex, detail, ok := strings.Cut(line[1:], " ")
	if !ok {
		return
	}
	if strings.HasPrefix(detail, "exporting ") || strings.HasPrefix(detail, "writing image ") || strings.HasPrefix(detail, "naming to ") {
		output.update("Save the agent container")
		return
	}
	if step := output.steps[vertex]; step != "" {
		switch {
		case detail == "CACHED":
			output.update(step + " (cached)")
		case detail == "DONE" || strings.HasPrefix(detail, "DONE "):
			output.update(step + " (complete)")
		default:
			output.update(step)
		}
	}
}

// Only fixed descriptions reach the terminal.
func imageStepMessage(description string) string {
	switch {
	case strings.Contains(description, "load metadata for "), strings.HasPrefix(description, "FROM "):
		return "Download the base container"
	case strings.Contains(description, "load build context"), strings.Contains(description, "load build definition"):
		return "Read the container build files"
	case strings.Contains(description, "playwright-cli install-browser"):
		return "Install the browser and its required software"
	case strings.Contains(description, "bun install --production --frozen-lockfile"):
		return "Install the agent tools"
	case strings.Contains(description, "nodejs.org/dist/"):
		return "Install the JavaScript runtime"
	case strings.Contains(description, "oven-sh/bun/releases/download/"):
		return "Install the application runtime"
	case strings.Contains(description, "astral-sh/uv/releases/"):
		return "Install the Python tools"
	case strings.Contains(description, "go.dev/dl/"):
		return "Install the Go tools"
	case strings.Contains(description, "apt-get update"):
		return "Install the system packages"
	case strings.HasSuffix(description, " /opt/culler-runtime/libexec/mc-real"):
		return "Install Culler"
	case strings.HasPrefix(description, "RUN groupadd"):
		return "Prepare the agent account"
	case strings.HasPrefix(description, "COPY "):
		return "Copy the container files"
	default:
		return "Configure the agent container"
	}
}

func runImageBuildCommand(ctx context.Context, dir string, env []string, report func(string), command string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.WaitDelay = 250 * time.Millisecond
	output := imageProgressOutput{report: report}
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	output.finish()
	return output.capture.Bytes(), err
}
