package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"mc/onboarding"
)

const cullerLifecycleGuidance = "Use culler for lifecycle operations."
const cullerConfigurationGuidance = "Configure Culler in the dashboard or through Collie."
const cullerInstallerURL = "https://raw.githubusercontent.com/vinchenkov/Culler/main/install.sh"

type cullerOptions struct {
	command, service, home      string
	help, noColor, check        bool
	restoreUpdate               bool
	tail                        int
	keepData, fullWipe          bool
	dryRun, yes                 bool
	homes, workspaces, binaries []string
}

var cullerCommands = []struct{ name, usage, description string }{
	{"status", "status", "Show the deployment state."},
	{"version", "version", "Show the release versions."},
	{"start", "start [service]", "Start Culler services."},
	{"stop", "stop [service]", "Stop Culler services safely."},
	{"restart", "restart [service]", "Restart Culler services."},
	{"update", "update [--check | --restore]", "Install, recover, or check for an update."},
	{"uninstall", "uninstall [options]", "Remove all Culler installations."},
	{"logs", "logs [service] [--tail N]", "Show recent service logs."},
	{"help", "help [command]", "Show command help."},
}

func knownCullerCommand(name string) bool {
	for _, command := range cullerCommands {
		if command.name == name {
			return true
		}
	}
	return false
}

func parseCuller(args []string) (cullerOptions, error) {
	o := cullerOptions{tail: 100}
	var positional []string
	tailSet, version, homeSet := false, false, false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--help":
			o.help = true
		case "--no-color":
			o.noColor = true
		case "--version":
			version = true
		case "--check":
			o.check = true
		case "--restore":
			o.restoreUpdate = true
		case "--keep-data":
			o.keepData = true
		case "--full-wipe":
			o.fullWipe = true
		case "--dry-run":
			o.dryRun = true
		case "--yes":
			o.yes = true
		case "--home", "--workspace", "--binary":
			i++
			if i == len(args) || !strings.HasPrefix(args[i], "/") {
				return o, fmt.Errorf("The %s option requires an absolute path.", arg)
			}
			switch arg {
			case "--home":
				o.homes = append(o.homes, args[i])
			case "--workspace":
				o.workspaces = append(o.workspaces, args[i])
			case "--binary":
				o.binaries = append(o.binaries, args[i])
			}
		case "--mc-home", "--tail":
			i++
			if i == len(args) {
				return o, fmt.Errorf("The %s option requires a value.", arg)
			}
			if arg == "--mc-home" {
				o.home = args[i]
				homeSet = true
			} else {
				n, err := strconv.Atoi(args[i])
				if err != nil || n < 1 || n > 1000 {
					return o, fmt.Errorf("Use --tail with a number from 1 through 1000.")
				}
				o.tail, tailSet = n, true
			}
		default:
			if strings.HasPrefix(arg, "-") {
				return o, fmt.Errorf("The option %q is unknown.", arg)
			}
			if len(positional) == 0 {
				o.command = arg
			}
			positional = append(positional, arg)
		}
	}
	if len(positional) == 0 {
		o.command, o.help = "help", !version
	} else {
		o.command = positional[0]
	}
	if version {
		if len(positional) != 0 || o.check || tailSet {
			return o, fmt.Errorf("Use --version without another command.")
		}
		o.command = "version"
	}
	if !knownCullerCommand(o.command) {
		return o, fmt.Errorf("The command %q is unknown. %s", o.command, cullerConfigurationGuidance)
	}
	if o.command == "help" {
		o.help = true
		if len(positional) > 2 {
			return o, fmt.Errorf("Help accepts one command.")
		}
		if len(positional) == 2 {
			o.command = positional[1]
			if !knownCullerCommand(o.command) {
				return o, fmt.Errorf("The command %q is unknown.", o.command)
			}
		}
	} else {
		if len(positional) > 2 {
			return o, fmt.Errorf("This command accepts at most one service.")
		}
		if len(positional) == 2 {
			o.service = positional[1]
		}
	}
	serviceCommand := o.command == "start" || o.command == "stop" || o.command == "restart" || o.command == "logs"
	if o.service != "" && !serviceCommand {
		return o, fmt.Errorf("The %s command accepts no service.", o.command)
	}
	if o.service != "" && o.service != "resident" && o.service != "dashboard" && o.service != "agentsview" {
		return o, fmt.Errorf("Use resident, dashboard, or agentsview as the service.")
	}
	if o.check && o.command != "update" {
		return o, fmt.Errorf("The --check option belongs to culler update.")
	}
	if o.restoreUpdate && (o.command != "update" || o.check) {
		return o, fmt.Errorf("Use --restore with culler update and without --check.")
	}
	if tailSet && o.command != "logs" {
		return o, fmt.Errorf("The --tail option belongs to culler logs.")
	}
	if o.keepData || o.fullWipe || o.dryRun || o.yes || len(o.homes)+len(o.workspaces)+len(o.binaries) > 0 {
		if o.command != "uninstall" {
			return o, fmt.Errorf("The removal options belong to culler uninstall.")
		}
	}
	if o.keepData && o.fullWipe {
		return o, fmt.Errorf("Choose --keep-data or --full-wipe.")
	}
	if o.command == "uninstall" && homeSet {
		return o, fmt.Errorf("Uninstall removes all detected Culler installations. Use --home to include another deployment, instead of --mc-home.")
	}
	return o, nil
}

func cullerNotice(t *onboarding.Terminal) {
	t.Guidance(cullerLifecycleGuidance, cullerConfigurationGuidance)
}

func cullerHelp(t *onboarding.Terminal, command string) {
	t.CommandTitle()
	cullerNotice(t)
	if command == "help" {
		t.Panel("Usage", nil)
		t.Literal("culler <command> [options]")
		t.Panel("Commands", nil)
		for _, item := range cullerCommands {
			t.CommandItem(item.usage, item.description)
		}
		t.Panel("Services", []string{"resident", "dashboard", "agentsview"})
		t.Panel("Defaults", []string{"Start, stop, and restart select all services.", "Logs select the resident."})
	} else {
		for _, item := range cullerCommands {
			if item.name == command {
				t.Panel("Purpose", []string{item.description})
				t.Panel("Usage", nil)
				t.Literal("culler " + item.usage)
			}
		}
		detail := map[string][]string{
			"status":    {"Show local service checks without external network requests.", "A deliberate stop remains distinct from a service failure."},
			"version":   {"Show the versions of the command and the installed release.", "This command works without active services."},
			"start":     {"An omitted service selects all services.", "A ready service remains active without a restart."},
			"stop":      {"An omitted service selects all services.", "The resident waits up to three minutes for active work.", "A stop remains in effect after login and computer restart."},
			"restart":   {"An omitted service selects all services.", "The resident waits for active work before it stops.", "A dashboard restart leaves the resident active."},
			"update":    {"Resume an incomplete update before selecting a new release.", "Use --restore to restore an incomplete update before commit.", "Use --check to inspect release availability without an update."},
			"uninstall": {"Remove all detected Culler and old Mission Control installations.", "The default removes software and permanently deletes Culler data.", "Active agents stop. Removal can discard unfinished work.", "Project repositories and shared host tools remain.", "Review the plan, then type wipe to confirm.", "Use --keep-data to preserve configuration, credentials, and history.", "MC_HOME includes a deployment; it does not limit removal to that deployment."},
			"logs":      {"An omitted service selects the resident.", "Show 100 recent records and exit.", "Use --tail N for 1 through 1000 records."},
		}
		t.Panel("Details", detail[command])
		if command == "start" || command == "stop" || command == "restart" || command == "logs" {
			t.Panel("Services", []string{"resident", "dashboard", "agentsview"})
		}
	}
	t.Panel("Options", nil)
	if command == "uninstall" {
		t.CommandItem("--keep-data", "Remove software and keep Culler data.")
		t.CommandItem("--full-wipe", "Delete Culler data. This is the default.")
		t.CommandItem("--dry-run", "Show the removal plan and exit.")
		t.CommandItem("--yes", "Confirm removal without a prompt.")
		t.CommandItem("--home <absolute-path>", "Include another deployment. Repeat as needed.")
		t.CommandItem("--workspace <absolute-path>", "Include another Project. Repeat as needed.")
		t.CommandItem("--binary <absolute-path>", "Include a runtime executable named mc.")
	} else if command == "help" {
		t.CommandItem("--mc-home <absolute-path>", "Select the deployment home, except for uninstall.")
	} else {
		t.CommandItem("--mc-home <absolute-path>", "Select the deployment home.")
	}
	t.CommandItem("--no-color", "Disable color.")
	t.CommandItem("--help", "Show help.")
	if command == "help" {
		t.CommandItem("--version", "Show the release versions.")
	}
}

func cullerUsage(args []string, stdout, stderr io.Writer) (cullerOptions, bool, int) {
	o, err := parseCuller(args)
	if err != nil {
		t := onboarding.NewCommandTerminal(stderr, o.noColor)
		t.CommandTitle()
		t.Problem("Check the command", []string{err.Error()})
		if !strings.Contains(err.Error(), cullerConfigurationGuidance) {
			cullerNotice(t)
		} else {
			t.Guidance(cullerLifecycleGuidance)
		}
		help := "culler help"
		if knownCullerCommand(o.command) && o.command != "help" {
			help += " " + o.command
		}
		t.Literal(help)
		return o, true, 2
	}
	if o.help {
		cullerHelp(onboarding.NewCommandTerminal(stdout, o.noColor), o.command)
		return o, true, 0
	}
	return o, false, 0
}
