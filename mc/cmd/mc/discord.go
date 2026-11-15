package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"mc/config"
	"mc/verbs"
)

type discordPolicy struct {
	Enabled     bool     `json:"enabled"`
	BotToken    string   `json:"bot_token"`
	GuildID     string   `json:"guild_id"`
	ChannelID   string   `json:"channel_id"`
	OperatorIDs []string `json:"operator_ids"`
}

type discordRequest struct {
	Expected        string `json:"expected"`
	Draft           string `json:"draft"`
	Token           string `json:"token"`
	Reuse           bool   `json:"reuse"`
	Guild           string `json:"guild"`
	Channel         string `json:"channel"`
	ReplaceAccounts bool   `json:"replace_accounts"`
	Blocked         bool   `json:"blocked"`
	Health          bool   `json:"health"`
	Confirm         bool   `json:"confirm"`
}

func discordRuntime(cfg config.Config) discordPolicy {
	d := cfg.Surfaces.Discord
	return discordPolicy{d.Enabled, d.BotToken, d.GuildID, d.ChannelID, append([]string{}, d.OperatorIDs...)}
}

func discordDigest(value any) string {
	body, _ := json.Marshal(value)
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func discordRevision(cfg config.Config) string {
	return discordDigest([]any{discordRuntime(cfg), cfg.Surfaces.Alerts})
}

func discordRPC(home, action string, request any) (map[string]json.RawMessage, error) {
	path := filepath.Join(home, "discord.socket")
	info, err := os.Lstat(path)
	if err != nil {
		return nil, verbs.Domainf("The Discord service is unavailable. Check Culler in System.")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 || stat.Uid != uint32(os.Getuid()) {
		return nil, verbs.Domainf("The Discord service socket is invalid.")
	}
	conn, err := net.DialTimeout("unix", path, 3*time.Second)
	if err != nil {
		return nil, verbs.Domainf("The Discord service is unavailable. Check Culler in System.")
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(70 * time.Second)); err != nil {
		return nil, err
	}
	if err := json.NewEncoder(conn).Encode(map[string]any{"action": action, "request": request}); err != nil {
		return nil, verbs.Domainf("The Discord service did not accept the request.")
	}
	line, err := bufio.NewReader(io.LimitReader(conn, 512*1024)).ReadBytes('\n')
	if err != nil {
		return nil, verbs.Domainf("The Discord operation remains unconfirmed. Check the connection again.")
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(line, &result) != nil {
		return nil, verbs.Domainf("The Discord service returned an invalid result.")
	}
	if message, ok := result["error"]; ok {
		var detail string
		if json.Unmarshal(message, &detail) != nil || len(detail) > 500 {
			detail = "The Discord operation failed."
		}
		return nil, verbs.Domainf("%s", detail)
	}
	return result, nil
}

func discordSpine(action string, request any) (map[string]any, error) {
	body, _ := json.Marshal(request)
	var output, diagnostic bytes.Buffer
	if run([]string{"__discord-state", action}, bytes.NewReader(body), &output, &diagnostic) != 0 {
		return nil, verbs.Domainf("The Discord state is unavailable. Check the deployment in System.")
	}
	var value map[string]any
	if err := json.Unmarshal(output.Bytes(), &value); err != nil {
		return nil, err
	}
	return value, nil
}

func discordProject(cfg config.Config, retire bool) error {
	d := discordRuntime(cfg)
	_, err := discordSpine("apply", verbs.DiscordProjection{
		Connection: discordDigest(d), Enabled: d.Enabled, Channel: d.ChannelID, Retire: retire,
		Blocked: d.Enabled && slices.Contains(cfg.Surfaces.Alerts.Blocked, "discord"),
		Health:  d.Enabled && slices.Contains(cfg.Surfaces.Alerts.Health, "discord"),
	})
	return err
}

func discordView(home string, cfg config.Config) (any, error) {
	availability, err := discordSpine("show", map[string]any{"configured": cfg.Surfaces.Discord.Enabled})
	if err != nil {
		return nil, err
	}
	view := map[string]any{"available": availability["available"], "revision": discordRevision(cfg),
		"enabled": cfg.Surfaces.Discord.Enabled, "guild_id": cfg.Surfaces.Discord.GuildID,
		"channel_id": cfg.Surfaces.Discord.ChannelID, "operator_ids": append([]string{}, cfg.Surfaces.Discord.OperatorIDs...),
		"blocked": slices.Contains(cfg.Surfaces.Alerts.Blocked, "discord"), "health": slices.Contains(cfg.Surfaces.Alerts.Health, "discord"),
	}
	if availability["available"] != true {
		return view, nil
	}
	status, err := discordRPC(home, "status", nil)
	if err != nil {
		view["runtime"] = map[string]any{"status": "unavailable", "detail": err.Error()}
	} else {
		view["runtime"] = status
	}
	return view, nil
}

func discordAlertRoute(routes []string, enabled bool) []string {
	result := []string{}
	for _, route := range routes {
		if route != "discord" {
			result = append(result, route)
		}
	}
	if !slices.Contains(result, "dashboard") {
		result = append(result, "dashboard")
	}
	if enabled {
		result = append(result, "discord")
	}
	return result
}

func commandDiscord(args []string, stdin io.Reader) (any, error) {
	id, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	if err := verbs.RequireHostScope(id, "Discord configuration"); err != nil {
		return nil, err
	}
	if len(args) != 1 {
		return nil, verbs.Usagef("A Discord action is required.")
	}
	home, err := configuredCanonicalHome()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, "config.toml")
	cfg, err := config.Load(path)
	if err != nil {
		return nil, verbs.Domainf("The host configuration is unavailable.")
	}
	action := args[0]
	if action == "show" {
		return discordView(home, cfg)
	}
	var request discordRequest
	if err := decodeConfigRequest(stdin, &request); err != nil {
		return nil, err
	}
	availability, err := discordSpine("show", map[string]any{"configured": cfg.Surfaces.Discord.Enabled})
	if err != nil {
		return nil, err
	}
	if availability["available"] != true {
		return nil, verbs.Domainf("Complete the first Project setup before you configure Discord.")
	}
	switch action {
	case "check-bot", "servers", "channels", "test", "reply", "renew", "cancel", "draft", "test-current", "check":
		if action != "cancel" && request.Expected != discordRevision(cfg) {
			return nil, verbs.Domainf("The Discord configuration changed. Reload the page before another change.")
		}
		if action == "check" {
			var result map[string]json.RawMessage
			_, err := config.UpdateAtomicAndApply(path, func(current *config.Config) error {
				if request.Expected != discordRevision(*current) {
					return verbs.Domainf("The Discord configuration changed. Reload the page before another change.")
				}
				return nil
			}, func(current config.Config) error {
				if err := discordProject(current, false); err != nil {
					return err
				}
				if _, err := discordRPC(home, "apply", map[string]any{"policy": discordRuntime(current)}); err != nil {
					return err
				}
				var err error
				result, err = discordRPC(home, action, request)
				return err
			})
			return result, err
		}
		return discordRPC(home, action, request)
	case "connect", "alerts", "disconnect":
		if action == "disconnect" && !request.Confirm {
			return nil, verbs.Usagef("Confirm the Discord disconnect.")
		}
	default:
		return nil, verbs.Usagef("The Discord action is unsupported.")
	}
	var previous config.Config
	var stageID string
	defer func() {
		if stageID != "" {
			_, _ = discordRPC(home, "discard-stage", map[string]string{"stage": stageID})
		}
	}()
	changedConnection := false
	err = config.UpdateAtomicWithRecovery(path, func(next *config.Config) error {
		previous = *next
		if request.Expected != discordRevision(*next) {
			return verbs.Domainf("The Discord configuration changed. Reload the page before another change.")
		}
		switch action {
		case "connect":
			candidate, err := discordRPC(home, "candidate", request)
			if err != nil {
				return err
			}
			var policy discordPolicy
			if json.Unmarshal(candidate["policy"], &policy) != nil || !policy.Enabled {
				return verbs.Domainf("The confirmed Discord connection is invalid.")
			}
			next.Surfaces.Discord = config.Discord{Enabled: true, BotToken: policy.BotToken, GuildID: policy.GuildID, ChannelID: policy.ChannelID, OperatorIDs: policy.OperatorIDs}
			if !previous.Surfaces.Discord.Enabled {
				next.Surfaces.Alerts.Blocked = discordAlertRoute(next.Surfaces.Alerts.Blocked, true)
				next.Surfaces.Alerts.Health = discordAlertRoute(next.Surfaces.Alerts.Health, true)
			}
		case "disconnect":
			next.Surfaces.Discord = config.Discord{OperatorIDs: []string{}}
			next.Surfaces.Alerts.Blocked = discordAlertRoute(next.Surfaces.Alerts.Blocked, false)
			next.Surfaces.Alerts.Health = discordAlertRoute(next.Surfaces.Alerts.Health, false)
		case "alerts":
			if !next.Surfaces.Discord.Enabled {
				return verbs.Domainf("Connect Discord before you select alerts.")
			}
			next.Surfaces.Alerts.Blocked = discordAlertRoute(next.Surfaces.Alerts.Blocked, request.Blocked)
			next.Surfaces.Alerts.Health = discordAlertRoute(next.Surfaces.Alerts.Health, request.Health)
		}
		changedConnection = discordDigest(discordRuntime(previous)) != discordDigest(discordRuntime(*next))
		if err := next.Validate(); err != nil {
			return err
		}
		if changedConnection && next.Surfaces.Discord.Enabled {
			staged, err := discordRPC(home, "stage", map[string]any{"policy": discordRuntime(*next)})
			if err != nil {
				return err
			}
			if json.Unmarshal(staged["stage"], &stageID) != nil || stageID == "" {
				return verbs.Domainf("The Discord connection check remains unconfirmed. Try again.")
			}
		}
		return nil
	}, func(next config.Config) error {
		if changedConnection {
			if _, err := discordRPC(home, "quiesce", nil); err != nil {
				return err
			}
		}
		if err := discordProject(next, changedConnection); err != nil {
			return err
		}
		if changedConnection {
			if _, err := discordRPC(home, "apply", map[string]any{"policy": discordRuntime(next)}); err != nil {
				return err
			}
		}
		return nil
	}, func(previous config.Config) error {
		if err := discordProject(previous, false); err != nil {
			return err
		}
		_, err := discordRPC(home, "apply", map[string]any{"policy": discordRuntime(previous)})
		return err
	})
	if err != nil {
		return nil, err
	}
	if action == "connect" {
		_, _ = discordRPC(home, "cancel", request)
	}
	cfg, err = config.Load(path)
	if err != nil {
		return nil, err
	}
	return discordView(home, cfg)
}

func commandDiscordRuntime() (any, error) {
	id, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	if err := verbs.RequireHostScope(id, "Discord runtime"); err != nil {
		return nil, err
	}
	home, err := configuredCanonicalHome()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(filepath.Join(home, "config.toml"))
	if err != nil {
		return nil, err
	}
	if err := discordProject(cfg, false); err != nil {
		return nil, err
	}
	return discordRuntime(cfg), nil
}

func commandDiscordState(args []string, stdin io.Reader) (any, error) {
	id, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	if err := verbs.RequireHostScope(id, "Discord state"); err != nil {
		return nil, err
	}
	if len(args) != 1 {
		return nil, verbs.Usagef("A Discord state action is required.")
	}
	if args[0] == "show" {
		var request struct {
			Configured bool `json:"configured"`
		}
		if err := decodeConfigRequest(stdin, &request); err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) { return verbs.DiscordAvailable(db, request.Configured) })
	}
	if args[0] == "apply" {
		var request verbs.DiscordProjection
		if err := decodeConfigRequest(stdin, &request); err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) { return verbs.DiscordApply(db, request) })
	}
	return nil, verbs.Usagef("The Discord state action is unsupported.")
}
