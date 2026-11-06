//go:build !test_fake_routing

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"mc/onboarding"
)

func cullerUninstall(ctx context.Context, o cullerOptions, stdin io.Reader, stdout, stderr io.Writer, tempRoots []string) int {
	out := onboarding.NewCommandTerminal(stdout, o.noColor)
	diagnostic := onboarding.NewCommandInput(stdin, stderr, o.noColor)
	if !o.dryRun && !o.yes && !diagnostic.HasTerminal() {
		diagnostic.Problem("Confirmation requires a terminal", []string{
			"Use --dry-run to review the removal plan.",
			"Run culler uninstall in a terminal to confirm removal.",
			"Use --yes only when you intend removal without a prompt.",
		})
		return 2
	}
	out.Panel("Uninstall scope", []string{
		"This command removes all detected Culler installations.",
		"The plan includes old Mission Control installations.",
		"Active agents stop. Removal can discard unfinished work.",
		"Project repositories and shared host tools remain.",
	})
	mode, confirmation := "--full-wipe", "wipe"
	if o.keepData {
		mode, confirmation = "--keep-data", "uninstall"
		out.Panel("Keep data", []string{"Remove Culler software and its commands.", "Keep configuration, credentials, and history for reinstallation."})
	} else {
		out.Problem("Full wipe", []string{"Permanently delete Culler data, credentials, task branches, and review files.", "Remove Culler software and its commands."})
	}
	args := []string{"__uninstall", mode, "--confirm"}
	if o.dryRun {
		args = append(args, "--dry-run")
	}
	for _, option := range []struct {
		name  string
		paths []string
	}{{"--home", o.homes}, {"--workspace", o.workspaces}, {"--binary", o.binaries}} {
		for _, path := range option.paths {
			args = append(args, option.name, path)
		}
	}
	var result bytes.Buffer
	confirmed := false
	empty := false
	diagnostic.Panel("Inspect installed resources", nil)
	code := runUninstallWith(ctx, args, &result, diagnostic, tempRoots, uninstallPresentation{
		plan: func(report uninstallReport) {
			empty = !cullerUninstallHasResources(report)
			renderCullerUninstallPlan(out, report)
		},
		problems: func(problems []string) {
			lines := make([]string, len(problems))
			for i, problem := range problems {
				lines[i] = setupSafeDiagnostic(strings.ReplaceAll(problem, "./uninstall.sh", "culler uninstall"))
			}
			diagnostic.Problem("Uninstall requires attention", lines)
		},
		progress: func(message string) {
			labels := map[string]string{
				"Stopping Culler services...":       "Stop Culler services",
				"Removing containers and images...": "Remove containers and images",
				"Removing Culler data...":           "Remove Culler data",
				"Removing installed files...":       "Remove installed files",
			}
			diagnostic.Panel(labels[message], nil)
		},
		confirm: func(uninstallReport) bool {
			if empty {
				return false
			}
			if !o.yes {
				diagnostic.Guidance("Press Enter without text to cancel.")
				answer, err := diagnostic.Ask(onboarding.Prompt{Label: "Type " + confirmation + " to confirm removal", CancelOnly: true})
				if err != nil || answer != confirmation {
					return false
				}
			}
			confirmed = ctx.Err() == nil
			return confirmed
		},
	})
	if empty && code == 130 && ctx.Err() == nil {
		out.CommandResult("No Culler resources to remove", true)
		return 0
	}
	if code == 130 && !confirmed {
		out.CommandResult("Uninstall cancelled", false)
		out.Guidance("Culler removed no resources.")
		return 130
	}
	var report uninstallReport
	if err := json.Unmarshal(result.Bytes(), &report); err != nil {
		diagnostic.Problem("The uninstall result is unavailable", []string{"Review the diagnostics before you retry."})
		return 1
	}
	if code != 0 {
		if !confirmed {
			out.CommandResult("The preview failed", false)
			out.Guidance("Culler removed no resources.")
		} else {
			out.CommandResult("The uninstall did not finish", false)
			out.Guidance("Some resources can remain.", "Use the installer to restore Culler before you retry:")
			out.Literal(cullerInstallCommand(o.home))
		}
		if ctx.Err() != nil {
			return 130
		}
		return code
	}
	if o.dryRun {
		out.CommandResult("Preview complete", true)
		out.Guidance("Culler removed no resources.")
		return 0
	}
	if !report.Uninstalled {
		diagnostic.Problem("The uninstall did not finish", []string{"The removal result did not confirm completion."})
		return 1
	}
	if o.keepData {
		out.CommandResult("Culler software removed", true)
		out.Guidance("Culler data remains. Reinstall at the same deployment paths to reuse it.")
	} else {
		out.CommandResult("Culler software and data removed", true)
	}
	out.Guidance("Project repositories and shared host tools remain.")
	return 0
}

func renderCullerUninstallPlan(out *onboarding.Terminal, report uninstallReport) {
	groups := []struct {
		name  string
		items []string
	}{
		{"Deployment directories", report.Homes},
		{"Smoke workspaces", report.SmokeRoots},
		{"Setup diagnostics", report.Diagnostics},
		{"Installed files", report.Software},
		{"Culler Bun", report.HostBuns},
		{"Database volumes", report.Volumes},
		{"Culler runtime volumes", report.RuntimeVolumes},
		{"Package cache volumes", report.CacheVolumes},
		{"Containers", report.Containers},
		{"Images", report.Images},
		{"Startup units", report.LaunchAgents},
		{"Runtime executables", report.Binaries},
		{"Culler commands", report.Commands},
		{"Culler entries in shell files", report.Shells},
	}
	for _, group := range groups {
		if len(group.items) == 0 {
			continue
		}
		out.Panel(fmt.Sprintf("%s · %d", group.name, len(group.items)), nil)
		for _, item := range group.items {
			out.Literal(item)
		}
	}
	for _, workspace := range report.Workspaces {
		if len(workspace.Removed)+len(workspace.Branches) == 0 {
			continue
		}
		out.Panel("Remove from Project", nil)
		out.Literal(workspace.Workspace)
		for _, path := range workspace.Removed {
			out.Literal(path)
		}
		for _, branch := range workspace.Branches {
			out.Field("Task branch", branch)
		}
	}
	if len(report.KeptHomes) > 0 {
		out.Panel("Keep Culler data", []string{"Reinstall at these deployment paths to reuse the data."})
		for _, home := range report.KeptHomes {
			out.Literal(home)
		}
	}
	if len(report.KeptVolumes) > 0 {
		out.Panel("Keep database volumes", nil)
		for _, volume := range report.KeptVolumes {
			out.Literal(volume)
		}
	}
}

func cullerUninstallHasResources(report uninstallReport) bool {
	for _, items := range [][]string{report.Homes, report.SmokeRoots, report.Diagnostics, report.Software,
		report.HostBuns, report.Volumes, report.RuntimeVolumes, report.CacheVolumes, report.Containers,
		report.Images, report.LaunchAgents, report.Binaries, report.Commands, report.Shells} {
		if len(items) > 0 {
			return true
		}
	}
	for _, workspace := range report.Workspaces {
		if len(workspace.Removed)+len(workspace.Branches) > 0 {
			return true
		}
	}
	return false
}
