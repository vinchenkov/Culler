package main

import (
	"fmt"
	"io"
	"strings"

	"mc/verbs"
)

func parseCollieStartArgs(args []string) (verbs.CollieStartArgs, error) {
	fs := newFlags("mc collie start")
	discordGuild := fs.String("discord-guild", "", "Discord server identifier")
	discordParent := fs.String("discord-parent", "", "Discord parent channel identifier")
	from := fs.String("from", "", "initial surface:channel_ref")
	visionProject := fs.String("vision-project", "", "Project for this Vision conversation")
	packet := fs.Int64("packet", 0, "packet of this packet conversation")
	packetVersion := fs.Int64("version", 0, "version of the packet of this packet conversation")
	clear := fs.Bool("clear", false, "end the current Vision or packet conversation and start an empty one")
	ensure := fs.Bool("ensure", false, "reuse an ordinary conversation before a new conversation")
	previous := fs.String("previous", "", "conversation before a model change")
	binding := fs.String("binding", "", "agent profile for a model change")
	request := fs.String("request", "", "request identifier for a model change")
	allowSet := false
	allowRaw := ""
	fs.Func("allow", "comma-separated Collie-agent verbs, or none", func(value string) error {
		if allowSet {
			return fmt.Errorf("--allow may be supplied only once")
		}
		allowSet = true
		allowRaw = value
		return nil
	})
	if err := parse(fs, args); err != nil {
		return verbs.CollieStartArgs{}, err
	}
	if *from == "" {
		return verbs.CollieStartArgs{}, verbs.Usagef("mc collie start requires --from <surface:channel_ref>")
	}
	if (flagSet(fs, "packet") || flagSet(fs, "version")) && (*packet < 1 || *packetVersion < 1) {
		return verbs.CollieStartArgs{}, verbs.Usagef("A packet conversation requires --packet and --version.")
	}
	var allowlist *[]string
	if allowSet {
		values := []string{}
		if allowRaw != "none" {
			values = strings.Split(allowRaw, ",")
		}
		allowlist = &values
	}
	return verbs.CollieStartArgs{DiscordGuild: *discordGuild, DiscordParent: *discordParent, Previous: *previous, Binding: *binding, Request: *request, From: *from, Allowlist: allowlist, VisionProject: *visionProject,
		Packet: *packet, PacketVersion: *packetVersion, Clear: *clear, Ensure: *ensure}, nil
}

func brokerCollieStart(args []string, stdout, stderr io.Writer) int {
	start, err := parseCollieStartArgs(args[2:])
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	// The local CollieStart still calls this, but production only ever reaches
	// the broker, so the fence has to be evaluated HERE too or it is not a
	// fence at all. It runs before routing resolution and before any Docker
	// call: a refused caller must not move the deployment at all.
	id, err := verbs.LoadIdentity()
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := verbs.RequireHostScope(id, "mc collie start"); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if _, err := ensureRuntimeHelper(); err != nil {
		return writeVerbError(stdout, stderr, verbs.Usagef("ensure warm helper: %v", err))
	}
	request, err := verbs.ResolvePrivateCollieStartRequest(start)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	var result verbs.CollieStartResult
	if err := callPrivateHelper("__collie-start", request, &result); err != nil {
		return writeVerbError(stdout, stderr, privateHelperRefusal("private Collie start", err))
	}
	if err := writeJSON(stdout, result); err != nil {
		fmt.Fprintln(stderr, "mc:", err)
		return 2
	}
	return 0
}
