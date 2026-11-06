package verbs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"mc/domain"
)

// Collie agent verbs are the maximum structural surface that may be frozen
// into a session. Reads outside the Collie session-state family remain
// governed by their own read surface and need no allowlist row. Project
// registration is a host effect that a container cannot honor, so it is
// absent here.
var maximumCollieAgentVerbs = []string{
	"board.write",
	"collie.draft",
	"collie.end",
	"collie.history",
	"collie.list",
	"collie.title",
	"environment.get",
	"packet.decide",
	"project.archive",
	"project.pause",
	"project.role-save",
	"project.role-unblock",
	"project.vision-draft",
	"project.vision-edit",
	"task.add",
	"task.block",
	"task.cancel",
	"task.interrupt",
	"task.resume",
	"task.unblock",
}

// MaximumCollieAgentVerbs exposes that registry to callers outside this package
// — in practice test fixtures that must fabricate a run.json envelope matching
// what `mc collie start` freezes into the spine. It returns a copy, so no caller
// can mutate the registry it is checked against.
//
// It exists because the hand-copied alternative is this project's most expensive
// fixture class: a second literal list drifts the moment a verb is added, and it
// fails as an envelope/registry MISMATCH — a message that names neither the
// missing verb nor the copy that went stale.
func MaximumCollieAgentVerbs() []string {
	out := make([]string, len(maximumCollieAgentVerbs))
	copy(out, maximumCollieAgentVerbs)
	return out
}

type SurfaceRef struct {
	Surface    string `json:"surface"`
	ChannelRef string `json:"channel_ref"`
}

// CollieAttachmentClaim is the immutable file-plane identity carried by the
// runner's existing own-session claim. It contains no path: the trusted runner
// derives the one fixed inbound mount path from the digest.
type CollieAttachmentClaim struct {
	Ordinal     int64  `json:"ordinal"`
	SessionID   string `json:"session_id"`
	Direction   string `json:"direction"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	MediaType   string `json:"media_type"`
	DisplayName string `json:"display_name"`
}

func parseSurfaceRef(raw string) (SurfaceRef, error) {
	surface, channel, ok := strings.Cut(raw, ":")
	if !ok {
		return SurfaceRef{}, Usagef("Collie surface reference must be surface:channel_ref")
	}
	switch surface {
	case "discord", "dashboard", "cli":
	default:
		return SurfaceRef{}, Usagef("Collie surface must be discord|dashboard|cli")
	}
	if channel == "" {
		return SurfaceRef{}, Usagef("Collie surface reference requires a non-empty channel_ref")
	}
	return SurfaceRef{Surface: surface, ChannelRef: channel}, nil
}

func canonicalCollieAllowlist(requested *[]string) ([]string, string, error) {
	allow := maximumCollieAgentVerbs
	if requested != nil {
		allow = *requested
	}
	maximum := make(map[string]struct{}, len(maximumCollieAgentVerbs))
	for _, verb := range maximumCollieAgentVerbs {
		maximum[verb] = struct{}{}
	}
	seen := make(map[string]struct{}, len(allow))
	canonical := make([]string, 0, len(allow))
	for _, verb := range allow {
		verb = strings.TrimSpace(verb)
		if verb == "" {
			return nil, "", Usagef("Collie --allow entries must be non-empty; use --allow none for a read-only session")
		}
		if _, ok := maximum[verb]; !ok {
			return nil, "", Usagef("%q is not a Collie-agent verb", verb)
		}
		if _, duplicate := seen[verb]; duplicate {
			continue
		}
		seen[verb] = struct{}{}
		canonical = append(canonical, verb)
	}
	sort.Strings(canonical)
	b, err := json.Marshal(canonical)
	if err != nil {
		return nil, "", Usagef("encode Collie allowlist: %v", err)
	}
	return canonical, string(b), nil
}

type CollieStartArgs struct {
	DiscordGuild  string
	DiscordParent string
	Previous      string
	Request       string
	Binding       string
	Ensure        bool // reuses an ordinary session before it creates one
	VisionProject string
	Packet        int64 // with PacketVersion, the packet of a packet conversation
	PacketVersion int64
	Clear         bool // ends the current Vision or packet conversation and starts an empty one
	From          string
	Allowlist     *[]string // nil = the maximum default; non-nil empty = read-only
}

// PrivateCollieStartRequest is the closed host-to-helper carrier for the one
// Collie verb that freezes a routing decision. config.toml is host-owned, so
// the helper receives its already-resolved binding and the non-secret enabled
// catalog that vouches for that binding; it never reads config.toml itself.
type PrivateCollieStartRequest struct {
	DiscordGuild      string                   `json:"discord_guild,omitempty"`
	DiscordParent     string                   `json:"discord_parent,omitempty"`
	Previous          string                   `json:"previous,omitempty"`
	Request           string                   `json:"request,omitempty"`
	Ensure            bool                     `json:"ensure,omitempty"`
	Version           int                      `json:"version"`
	From              string                   `json:"from"`
	VisionProject     string                   `json:"vision_project,omitempty"`
	Packet            int64                    `json:"packet_id,omitempty"`
	PacketVersion     int64                    `json:"version_id,omitempty"`
	Clear             bool                     `json:"clear,omitempty"`
	Allowlist         []string                 `json:"allowlist"`
	AllowlistExplicit bool                     `json:"allowlist_explicit"`
	Binding           string                   `json:"binding"`
	RuntimeCapability PrivateRuntimeCapability `json:"runtime_capability"`
}

// CollieStartResult is kept concrete so the broker can validate and relay the
// helper result without granting it an open-ended JSON output channel.
type CollieStartResult struct {
	Replaced    string `json:"replaced,omitempty"`
	Preserved   bool   `json:"preserved,omitempty"`
	SessionID   string `json:"session_id"`
	Status      string `json:"status"`
	SessionPath string `json:"session_path"`
	Binding     string `json:"binding"`
	From        string `json:"from"`
}

// ResolvePrivateCollieStartRequest resolves the host-owned routing and runtime
// authority once, then places only its frozen, non-secret consequence in the
// private helper frame.
func ResolvePrivateCollieStartRequest(a CollieStartArgs) (PrivateCollieStartRequest, error) {
	home, err := resolveMCHome()
	if err != nil {
		return PrivateCollieStartRequest{}, err
	}
	route, authority, err := resolveRoleRouteWithAuthority(home, "collie")
	if err != nil {
		return PrivateCollieStartRequest{}, err
	}
	allow, _, err := canonicalCollieAllowlist(a.Allowlist)
	if err != nil {
		return PrivateCollieStartRequest{}, err
	}
	from, err := parseSurfaceRef(a.From)
	if err != nil {
		return PrivateCollieStartRequest{}, err
	}
	modelChange := a.Previous != "" || a.Request != "" || a.Binding != ""
	if err := refuseCollieStartFlags(from, a.VisionProject, a.Packet, a.PacketVersion, a.Clear, a.Ensure, modelChange); err != nil {
		return PrivateCollieStartRequest{}, err
	}
	binding := route.HistoricalBinding()
	if a.Binding != "" {
		binding = ""
		for _, spec := range authority.capability().Bindings {
			if spec.ID == a.Binding {
				binding = spec.Harness + "/" + spec.ID
				break
			}
		}
		if binding == "" {
			return PrivateCollieStartRequest{}, Domainf("The selected agent profile is unavailable.")
		}
	}
	if (a.Previous != "" || a.Request != "" || a.Binding != "") && (a.Previous == "" || a.Binding == "" || a.Request == "") {
		return PrivateCollieStartRequest{}, Usagef("A model change requires a conversation, an agent profile, and a request identifier.")
	}
	return PrivateCollieStartRequest{
		Previous: a.Previous, Request: a.Request, DiscordGuild: a.DiscordGuild, DiscordParent: a.DiscordParent,
		Version: 1, Ensure: a.Ensure, From: a.From, VisionProject: a.VisionProject, Packet: a.Packet, PacketVersion: a.PacketVersion,
		Clear: a.Clear, Allowlist: allow, AllowlistExplicit: a.Allowlist != nil,
		Binding: binding, RuntimeCapability: authority.capability(),
	}, nil
}

// CollieStart records one lease-free conversational session and its initial
// surface binding. The resident can prepare its container before the first message.
func CollieStart(db *sql.DB, id *RunIdentity, a CollieStartArgs) (any, error) {
	if err := RequireHostScope(id, "mc collie start"); err != nil {
		return nil, err
	}
	req, err := ResolvePrivateCollieStartRequest(a)
	if err != nil {
		return nil, err
	}
	return CollieStartPrivateDB(db, req)
}

// CollieStartPrivateDB is the helper half of the host-resolved start crossing.
// The exact helper scope is the caller authority; this verifier makes the
// carried capability and its selected historical binding inseparable.
func CollieStartPrivateDB(db *sql.DB, req PrivateCollieStartRequest) (CollieStartResult, error) {
	if req.Version != 1 {
		return CollieStartResult{}, Domainf("Collie private start frame version is invalid")
	}
	if err := validatePrivateRuntimeCapability(req.RuntimeCapability); err != nil {
		return CollieStartResult{}, err
	}
	runtime, ok := resolveFrozenCollieBinding(req.RuntimeCapability, req.Binding)
	if !ok {
		return CollieStartResult{}, Domainf("Collie private start binding is unavailable in the carried runtime capability")
	}
	from, err := parseSurfaceRef(req.From)
	if err != nil {
		return CollieStartResult{}, err
	}
	allow := req.Allowlist
	if !req.AllowlistExplicit {
		if len(allow) != len(maximumCollieAgentVerbs) {
			return CollieStartResult{}, Domainf("Collie private start omitted its default allowlist")
		}
	}
	_, allowJSON, err := canonicalCollieAllowlist(&allow)
	if err != nil {
		return CollieStartResult{}, err
	}
	return collieStartWithBinding(db, from, allowJSON, req.From, req.Binding, req.VisionProject, req.Packet, req.PacketVersion, req.Clear, req.Ensure, req.Previous, req.Request, req.DiscordGuild, req.DiscordParent, runtime.Effort)
}

// refuseCollieStartFlags refuses a start request that combines the flags of two
// conversation kinds. The host checks the request before the helper crossing,
// so each refusal keeps its own text.
func refuseCollieStartFlags(from SurfaceRef, visionProject string, packet, packetVersion int64, clear, ensureSession, modelChange bool) error {
	if packet != 0 || packetVersion != 0 {
		switch {
		case packet < 1 || packetVersion < 1:
			return Usagef("A packet conversation requires --packet and --version.")
		case visionProject != "":
			return Usagef("A conversation cannot be both a Vision conversation and a packet conversation.")
		case from.Surface != "dashboard":
			return Usagef("A packet conversation requires a dashboard surface.")
		case modelChange:
			return Usagef("A model change requires an ordinary dashboard conversation and a valid request identifier.")
		case ensureSession:
			return Usagef("--ensure requires an ordinary dashboard conversation")
		}
		return nil
	}
	if clear && visionProject == "" {
		return Usagef("--clear requires --vision-project or --packet")
	}
	return nil
}

func collieStartWithBinding(db *sql.DB, from SurfaceRef, allowJSON, rawFrom, binding, visionProject string, packet, packetVersion int64, clear bool, ensureSession bool, previous, request, discordGuild, discordParent, effort string) (CollieStartResult, error) {
	sessionID, err := newRunID()
	if err != nil {
		return CollieStartResult{}, err
	}
	if err := refuseCollieStartFlags(from, visionProject, packet, packetVersion, clear, ensureSession, previous != "" || request != ""); err != nil {
		return CollieStartResult{}, err
	}
	if previous != "" || request != "" {
		if previous == "" || !validModelRequest(request) || visionProject != "" || clear || ensureSession || from.Surface != "dashboard" {
			return CollieStartResult{}, Usagef("A model change requires an ordinary dashboard conversation and a valid request identifier.")
		}
		digest := sha256.Sum256([]byte(previous + "\x00" + request))
		sessionID = fmt.Sprintf("%x", digest[:8])
		from.ChannelRef = "model-" + sessionID
		rawFrom = "dashboard:" + from.ChannelRef
	}
	thread := discordGuild != "" || discordParent != ""
	if thread && (from.Surface != "discord" || !discordID(discordGuild) || !discordID(discordParent) ||
		!discordID(from.ChannelRef) || from.ChannelRef == discordParent || visionProject != "" || packet != 0 || clear || ensureSession || previous != "" || request != "") {
		return CollieStartResult{}, Usagef("A Discord thread requires its server, parent channel, and thread identifiers.")
	}
	replaced, preserved := "", false
	sessionID = "h-" + sessionID // disjoint from pipeline run ids and trace paths
	sessionPath := "sessions/" + sessionID
	containerName := "mc-collie-" + sessionID
	if ensureSession && (visionProject != "" || clear || from.Surface != "dashboard") {
		return CollieStartResult{}, Usagef("--ensure requires an ordinary dashboard conversation")
	}
	err = inTx(db, func(ctx context.Context, q Q) error {
		if thread {
			var guild, parent string
			var retired, deleted sql.NullString
			err := q.QueryRowContext(ctx, `SELECT s.id,s.session_path,s.binding,t.guild_id,t.channel_id,t.retired_at,s.deleted_at
				FROM collie_discord_threads t JOIN collie_sessions s ON s.id=t.session_id WHERE t.thread_id=?`, from.ChannelRef).
				Scan(&sessionID, &sessionPath, &binding, &guild, &parent, &retired, &deleted)
			if err == nil {
				if retired.Valid || deleted.Valid || guild != discordGuild || parent != discordParent {
					return Domainf("The Discord thread is unavailable for this conversation.")
				}
				return nil
			}
			if err != sql.ErrNoRows {
				return err
			}
		}
		if previous != "" {
			var foundBinding string
			var deleted sql.NullString
			err := q.QueryRowContext(ctx, `SELECT binding,deleted_at FROM collie_sessions WHERE id=?`, sessionID).Scan(&foundBinding, &deleted)
			if err == nil {
				if foundBinding != binding {
					return Domainf("This model request already selected another agent profile.")
				}
				if deleted.Valid {
					return Domainf("This conversation changed again. Select the current conversation before another model change.")
				}
				var oldDeleted sql.NullString
				if err := q.QueryRowContext(ctx, `SELECT deleted_at FROM collie_sessions WHERE id=?`, previous).Scan(&oldDeleted); err != nil {
					return err
				}
				if oldDeleted.Valid {
					replaced = previous
				} else {
					preserved = true
				}
				return nil
			}
			if err != sql.ErrNoRows {
				return err
			}
			var project sql.NullString
			var packetID sql.NullInt64
			var oldPath, oldBinding string
			if err := q.QueryRowContext(ctx, `SELECT vision_project_id,packet_id,deleted_at,session_path,binding FROM collie_sessions WHERE id=?`, previous).Scan(&project, &packetID, &deleted, &oldPath, &oldBinding); err != nil {
				if err == sql.ErrNoRows {
					return Domainf("The previous conversation is unavailable.")
				}
				return err
			}
			if project.Valid || packetID.Valid || deleted.Valid {
				return Domainf("The previous conversation is unavailable for this model change.")
			}
			if oldBinding == binding {
				sessionID, sessionPath = previous, oldPath
				return nil
			}
			var messages int
			if err := q.QueryRowContext(ctx, `SELECT count(*) FROM conversation_messages WHERE session_id=?`, previous).Scan(&messages); err != nil {
				return err
			}
			preserved = messages > 0
			if !preserved {
				if _, _, err := collieEndTx(ctx, q, "operator", previous, "The operator changed the model of an empty conversation."); err != nil {
					return err
				}
				if _, err := q.ExecContext(ctx, `UPDATE collie_sessions SET deleted_at=datetime('now') WHERE id=?`, previous); err != nil {
					return err
				}
				replaced = previous
			}
		}
		if ensureSession {
			err := q.QueryRowContext(ctx, `SELECT id,session_path,binding FROM collie_sessions
				WHERE vision_project_id IS NULL AND packet_id IS NULL AND deleted_at IS NULL
				ORDER BY last_activity_at DESC, rowid DESC LIMIT 1`).Scan(&sessionID, &sessionPath, &binding)
			if err == nil {
				return nil
			}
			if err != sql.ErrNoRows {
				return err
			}
		}
		if visionProject != "" {
			if from.Surface != "dashboard" || from.ChannelRef != "vision-"+visionProject {
				return Usagef("a Vision session requires dashboard:vision-<project> as its surface")
			}
			var projectState string
			if err := q.QueryRowContext(ctx, `SELECT status FROM projects WHERE id=?`, visionProject).Scan(&projectState); err != nil {
				return Domainf("the Vision Project is unavailable")
			}
			if projectState == "archived" {
				return Domainf("the Vision Project is archived")
			}
			var foundID, foundPath, foundBinding, foundStatus string
			err := q.QueryRowContext(ctx, `SELECT id,session_path,binding,status FROM collie_sessions WHERE vision_project_id=? ORDER BY rowid DESC LIMIT 1`, visionProject).Scan(&foundID, &foundPath, &foundBinding, &foundStatus)
			if err == nil && clear {
				if _, _, err := collieEndTx(ctx, q, "operator", foundID, "The operator cleared the Vision conversation."); err != nil {
					return err
				}
			} else if err == nil {
				sessionID, sessionPath, binding = foundID, foundPath, foundBinding
				if foundStatus != "active" {
					if err := resumeBoundSessionForSend(ctx, q, sessionID, from); err != nil {
						return err
					}
					_, err := ensureActiveBinding(ctx, q, sessionID, from)
					return err
				}
				return nil
			}
			if err != nil && err != sql.ErrNoRows {
				return err
			}
		}
		if packet != 0 {
			reused, err := preparePacketConversation(ctx, q, packet, packetVersion, clear)
			if err != nil {
				return err
			}
			if reused != nil {
				sessionID, sessionPath, binding, rawFrom = reused.id, reused.path, reused.binding, reused.from
				return nil
			}
		}
		var owner string
		err := q.QueryRowContext(ctx, `
			SELECT session_id FROM collie_bindings
			WHERE surface = ? AND channel_ref = ? AND active = 1`,
			from.Surface, from.ChannelRef).Scan(&owner)
		if err == nil {
			return Domainf("%s:%s is already bound to active Collie session %q",
				from.Surface, from.ChannelRef, owner)
		}
		if err != sql.ErrNoRows {
			return err
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO collie_sessions
				(id, container_name, verb_allowlist, session_path, binding, effort, vision_project_id, packet_id)
			VALUES (?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, 0))`,
			sessionID, containerName, allowJSON, sessionPath, binding, effort, visionProject, packet); err != nil {
			return err
		}
		if thread {
			if _, err := q.ExecContext(ctx, `INSERT INTO collie_discord_threads(session_id,guild_id,channel_id,thread_id) VALUES (?,?,?,?)`,
				sessionID, discordGuild, discordParent, from.ChannelRef); err != nil {
				return err
			}
		}
		_, err = q.ExecContext(ctx, `
			INSERT INTO collie_bindings (session_id, surface, channel_ref)
			VALUES (?, ?, ?)`, sessionID, from.Surface, from.ChannelRef)
		return err
	})
	if err != nil {
		return CollieStartResult{}, err
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM collie_sessions WHERE id=?`, sessionID).Scan(&status); err != nil {
		return CollieStartResult{}, err
	}
	return CollieStartResult{Replaced: replaced, Preserved: preserved, SessionID: sessionID, Status: status, SessionPath: sessionPath, Binding: binding, From: rawFrom}, nil
}

type packetConversation struct {
	id, path, binding, from string
}

// The resident mounts no archived Project, so the Collie container of a packet
// conversation of an archived Project cannot start.
const packetProjectArchived = "The Project of this packet is archived. Conversations about its packets are unavailable."

// refuseArchivedPacketProject refuses each message and each resume of a
// packet conversation whose Project is archived.
func refuseArchivedPacketProject(ctx context.Context, q Q, sessionID string) error {
	var archived bool
	err := q.QueryRowContext(ctx, `
		SELECT w.status = 'archived' FROM collie_sessions s
		JOIN review_versions p ON p.id = s.packet_id
		JOIN tasks t ON t.id = p.task_id
		JOIN projects w ON w.id = t.project
		WHERE s.id = ?`, sessionID).Scan(&archived)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if archived {
		return Domainf("%s", packetProjectArchived)
	}
	return nil
}

// preparePacketConversation finds the current conversation of the latest
// packet of a task. It resumes an ended current conversation through its own
// dashboard binding. A nil result without an error means that the caller
// creates a new current conversation: the packet has none, the operator
// deleted it, or clear ended it.
func preparePacketConversation(ctx context.Context, q Q, packet, packetVersion int64, clear bool) (*packetConversation, error) {
	var archived bool
	err := q.QueryRowContext(ctx, `
		SELECT w.status = 'archived' FROM review_versions p
		JOIN tasks t ON t.id = p.task_id AND t.current_review_id = p.id
		JOIN projects w ON w.id = t.project
		WHERE p.id = ? AND p.version_id = ?`, packet, packetVersion).Scan(&archived)
	if err == sql.ErrNoRows {
		return nil, domain.Errf(domain.CodeRecordConflict, "The review packet changed. Open the current packet.")
	}
	if err != nil {
		return nil, err
	}
	if archived {
		return nil, Domainf("%s", packetProjectArchived)
	}
	var found packetConversation
	var status string
	var deleted sql.NullString
	err = q.QueryRowContext(ctx, `
		SELECT id, session_path, binding, status, deleted_at FROM collie_sessions
		WHERE packet_id = ? ORDER BY rowid DESC LIMIT 1`, packet).Scan(&found.id, &found.path, &found.binding, &status, &deleted)
	if err == sql.ErrNoRows || (err == nil && deleted.Valid) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if clear {
		_, _, err := collieEndTx(ctx, q, "operator", found.id, "The operator started a new conversation for this packet.")
		return nil, err
	}
	var place SurfaceRef
	if err := q.QueryRowContext(ctx, `
		SELECT surface, channel_ref FROM collie_bindings
		WHERE session_id = ? AND surface = 'dashboard' ORDER BY id LIMIT 1`, found.id).Scan(&place.Surface, &place.ChannelRef); err != nil {
		return nil, err
	}
	found.from = place.Surface + ":" + place.ChannelRef
	if status != "active" {
		if err := resumeBoundSessionForSend(ctx, q, found.id, place); err != nil {
			return nil, err
		}
		if _, err := ensureActiveBinding(ctx, q, found.id, place); err != nil {
			return nil, err
		}
	}
	return &found, nil
}

// CollieBind appends one binding event to an active session. Same-session
// retries are idempotent; an occupied concrete place is never transferred
// implicitly to another session.
func CollieBind(db *sql.DB, id *RunIdentity, sessionID, rawFrom string) (any, error) {
	if err := RequireHostScope(id, "mc collie bind"); err != nil {
		return nil, err
	}
	from, err := parseSurfaceRef(rawFrom)
	if err != nil {
		return nil, err
	}
	bound := true
	err = inTx(db, func(ctx context.Context, q Q) error {
		var status string
		err := q.QueryRowContext(ctx,
			`SELECT status FROM collie_sessions WHERE id = ?`, sessionID).Scan(&status)
		if err == sql.ErrNoRows {
			return Domainf("unknown Collie session %q", sessionID)
		}
		if err != nil {
			return err
		}
		if status != "active" {
			return Domainf("binding requires an active Collie session; %q is %s (use resume)", sessionID, status)
		}

		bound, err = ensureActiveBinding(ctx, q, sessionID, from)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"session_id": sessionID,
		"surface":    from.Surface, "channel_ref": from.ChannelRef,
		"bound": bound,
	}, nil
}

// CollieResume is a record-only status/binding transition: host scope only,
// ended|reaped → active, requiring the immutable native locator pair (the
// conversation-rows fallback is a separate explicit arm, never an implicit
// downgrade). No launch effect — re-mounting the folder
// and relaunching the recorded harness is the resident's host authority.
func CollieResume(db *sql.DB, id *RunIdentity, sessionID, rawFrom string) (any, error) {
	if err := RequireHostScope(id, "mc collie resume"); err != nil {
		return nil, err
	}
	from, err := parseSurfaceRef(rawFrom)
	if err != nil {
		return nil, err
	}
	resumed := false
	err = inTx(db, func(ctx context.Context, q Q) error {
		var status string
		var nativeRef, traceFile sql.NullString
		err := q.QueryRowContext(ctx, `
			SELECT status, native_session_ref, trace_filename
			FROM collie_sessions WHERE id = ? AND deleted_at IS NULL`, sessionID,
		).Scan(&status, &nativeRef, &traceFile)
		if err == sql.ErrNoRows {
			return Domainf("unknown Collie session %q", sessionID)
		}
		if err != nil {
			return err
		}
		if err := refuseArchivedPacketProject(ctx, q, sessionID); err != nil {
			return err
		}
		if status == "active" {
			// Crash-after-commit retry: idempotent iff the requested place
			// is already this session's active binding.
			var owner string
			err := q.QueryRowContext(ctx, `
				SELECT session_id FROM collie_bindings
				WHERE surface = ? AND channel_ref = ? AND active = 1`,
				from.Surface, from.ChannelRef).Scan(&owner)
			if err == nil && owner == sessionID {
				return nil
			}
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			return Domainf("Collie session %q is already active (use bind)", sessionID)
		}
		if (!nativeRef.Valid || !traceFile.Valid) && from.Surface == "dashboard" {
			var messages int
			if err := q.QueryRowContext(ctx, `SELECT count(*) FROM conversation_messages WHERE session_id=?`, sessionID).Scan(&messages); err != nil {
				return err
			}
			if messages == 0 {
				if err := resumeBoundSessionForSend(ctx, q, sessionID, from); err != nil {
					return err
				}
				if _, err := ensureActiveBinding(ctx, q, sessionID, from); err != nil {
					return err
				}
				resumed = true
				return nil
			}
		}
		if !nativeRef.Valid || !traceFile.Valid {
			return Domainf("Collie session %q never registered its native locator pair; "+
				"native continue is impossible and the conversation-rows fallback "+
				"is a separate explicit arm", sessionID)
		}
		// Status flips first (the binding trigger requires an active session),
		// the dead launch generation is cleared, and native resume debt is
		// created in the same statement. The selector must wake this session
		// even before new input arrives; if input races the tick, the native debt
		// still wins so an explicit resume can never silently downgrade to fresh.
		if _, err := q.ExecContext(ctx, `
			UPDATE collie_sessions
			SET status = 'active', last_activity_at = datetime('now'),
			    current_launch_id = NULL, current_launch_mode = NULL,
			    current_prime_through_seq = NULL, current_prime_row_count = NULL,
			    current_container_id = NULL, launch_bound_at = NULL,
			    launch_started_at = NULL,
			    resume_owed = 1, resume_mode = 'native',
			    resume_prime_through_seq = NULL, resume_prime_row_count = NULL
			WHERE id = ?`, sessionID); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO activity (actor, kind, subject, detail)
			VALUES ('operator', 'collie.resumed', ?, ?)`,
			sessionID, from.Surface+":"+from.ChannelRef); err != nil {
			return err
		}
		if _, err := ensureActiveBinding(ctx, q, sessionID, from); err != nil {
			return err
		}
		resumed = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"session_id": sessionID, "status": "active",
		"surface": from.Surface, "channel_ref": from.ChannelRef,
		"resumed": resumed,
	}, nil
}

// resumeBoundSessionForSend reactivates a non-active session for an inbound
// turn arriving from a place that session was already bound to. It is
// `CollieResume`'s transition, less the operator's explicit intent, and it
// differs from it in exactly one way: where the native locator pair is absent
// it owes no resume debt instead of refusing.
//
// `CollieResume` can refuse there because an operator can be told to start a
// new session. A send cannot — the turn is already in the operator's hand. The
// pair is registered as soon as a harness session exists, so its absence means
// none was ever established and there is nothing to continue: clear the dead
// generation and let the wake selector start fresh.
func resumeBoundSessionForSend(ctx context.Context, q Q, sessionID string, from SurfaceRef) error {
	var known int
	if err := q.QueryRowContext(ctx, `
		SELECT count(*) FROM collie_bindings
		WHERE session_id = ? AND surface = ? AND channel_ref = ?`,
		sessionID, from.Surface, from.ChannelRef).Scan(&known); err != nil {
		return err
	}
	if known == 0 {
		return Domainf("Collie session %q is not bound to %s:%s; use resume before send",
			sessionID, from.Surface, from.ChannelRef)
	}
	var nativeRef, traceFile sql.NullString
	if err := q.QueryRowContext(ctx, `
		SELECT native_session_ref, trace_filename FROM collie_sessions WHERE id = ?`,
		sessionID).Scan(&nativeRef, &traceFile); err != nil {
		return err
	}
	// Native continue is owed only when there is a recorded session to
	// continue from; otherwise the wake starts fresh and owes nothing. Same
	// statement as the explicit resume, so the dead launch generation cannot
	// survive the flip and strand the session unwakeable.
	resumeOwed, resumeMode := 0, any(nil)
	if nativeRef.Valid && traceFile.Valid {
		resumeOwed, resumeMode = 1, "native"
	}
	if _, err := q.ExecContext(ctx, `
		UPDATE collie_sessions
		SET status = 'active', last_activity_at = datetime('now'),
		    current_launch_id = NULL, current_launch_mode = NULL,
		    current_prime_through_seq = NULL, current_prime_row_count = NULL,
		    current_container_id = NULL, launch_bound_at = NULL,
		    launch_started_at = NULL,
		    resume_owed = ?, resume_mode = ?,
		    resume_prime_through_seq = NULL, resume_prime_row_count = NULL
		WHERE id = ?`, resumeOwed, resumeMode, sessionID); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `
		INSERT INTO activity (actor, kind, subject, detail)
		VALUES ('operator', 'collie.resumed', ?, ?)`,
		sessionID, from.Surface+":"+from.ChannelRef)
	return err
}

func ensureActiveBinding(ctx context.Context, q Q, sessionID string, from SurfaceRef) (bool, error) {
	if from.Surface != "discord" {
		var thread string
		err := q.QueryRowContext(ctx, `SELECT thread_id FROM collie_discord_threads WHERE session_id=? AND retired_at IS NULL`, sessionID).Scan(&thread)
		if err == nil {
			if _, err := ensureActiveBinding(ctx, q, sessionID, SurfaceRef{Surface: "discord", ChannelRef: thread}); err != nil {
				return false, err
			}
		} else if err != sql.ErrNoRows {
			return false, err
		}
	} else {
		var retired sql.NullString
		err := q.QueryRowContext(ctx, `SELECT retired_at FROM collie_discord_threads WHERE thread_id=?`, from.ChannelRef).Scan(&retired)
		if err != nil && err != sql.ErrNoRows {
			return false, err
		}
		if retired.Valid {
			return false, Domainf("The Discord thread destination is retired.")
		}
	}
	var owner string
	err := q.QueryRowContext(ctx, `
		SELECT session_id FROM collie_bindings
		WHERE surface = ? AND channel_ref = ? AND active = 1`,
		from.Surface, from.ChannelRef).Scan(&owner)
	switch {
	case err == nil && owner == sessionID:
		return false, nil
	case err == nil:
		return false, Domainf("%s:%s is already bound to active Collie session %q",
			from.Surface, from.ChannelRef, owner)
	case err != sql.ErrNoRows:
		return false, err
	}
	_, err = q.ExecContext(ctx, `
		INSERT INTO collie_bindings (session_id, surface, channel_ref)
		VALUES (?, ?, ?)`, sessionID, from.Surface, from.ChannelRef)
	return true, err
}

func parseAttachments(verb, raw string) ([]string, any, error) {
	if raw == "" {
		return []string{}, nil, nil
	}
	var refs []any
	if err := json.Unmarshal([]byte(raw), &refs); err != nil || refs == nil {
		return nil, nil, Usagef("%s --attachments must be a JSON array", verb)
	}
	if len(refs) != 0 {
		// Path strings were an early placeholder, never a file capability. New
		// attachment traffic must enter through the sealed typed
		// ingress/publication records; accepting a path here would recreate an
		// authority the later native connector could accidentally open.
		return nil, nil, Usagef("%s --attachments requires typed attachment publication; legacy path strings are refused", verb)
	}
	return []string{}, nil, nil
}

type CollieSendArgs struct {
	SourceMessage string
	Session       string
	From          string
	Body          string
	Attachments   string
}

type collieEchoPayload struct {
	MessageID   int64      `json:"message_id"`
	Seq         int64      `json:"seq"`
	Body        string     `json:"body"`
	Attachments any        `json:"attachments"`
	Origin      SurfaceRef `json:"origin"`
}

// CollieSend is native-surface transport into one active conversation. The
// inbound record, first-traffic binding, activity timestamp, and echo rows to
// every other binding commit together. It is structurally host-only;
// the Collie model never owns inbound transport.
func CollieSend(db *sql.DB, id *RunIdentity, a CollieSendArgs) (any, error) {
	if err := RequireHostScope(id, "mc collie send"); err != nil {
		return nil, err
	}
	from, err := parseSurfaceRef(a.From)
	if err != nil {
		return nil, err
	}
	if a.SourceMessage != "" && (from.Surface != "discord" || !discordID(a.SourceMessage)) {
		return nil, Usagef("A source message requires a Discord message identifier.")
	}
	attachments, attachmentValue, err := parseAttachments("mc collie send", a.Attachments)
	if err != nil {
		return nil, err
	}
	if a.Body == "" && len(attachments) == 0 {
		return nil, Usagef("mc collie send requires a body or attachment")
	}

	var messageID, seq int64
	echoes := 0
	duplicate := false
	err = inTx(db, func(ctx context.Context, q Q) error {
		if a.SourceMessage != "" {
			var session, channel, body string
			err := q.QueryRowContext(ctx, `SELECT id,seq,session_id,channel_ref,body FROM conversation_messages WHERE source_message_id=?`, a.SourceMessage).
				Scan(&messageID, &seq, &session, &channel, &body)
			if err == nil {
				if session != a.Session || channel != from.ChannelRef || body != a.Body {
					return Domainf("The Discord message already belongs to another request.")
				}
				duplicate = true
				return nil
			}
			if err != sql.ErrNoRows {
				return err
			}
		}
		var status string
		err := q.QueryRowContext(ctx,
			`SELECT status FROM collie_sessions WHERE id = ? AND deleted_at IS NULL`, a.Session).Scan(&status)
		if err == sql.ErrNoRows {
			return Domainf("unknown Collie session %q", a.Session)
		}
		if err != nil {
			return err
		}
		if err := refuseArchivedPacketProject(ctx, q, a.Session); err != nil {
			return err
		}
		if status != "active" {
			// A known surface can resume its ended session.
			// The saved native locator preserves the conversation across launches.
			if err := resumeBoundSessionForSend(ctx, q, a.Session, from); err != nil {
				return err
			}
		}
		if _, err := ensureActiveBinding(ctx, q, a.Session, from); err != nil {
			return err
		}
		if err := q.QueryRowContext(ctx, `
			SELECT COALESCE(MAX(seq), 0) + 1
			FROM conversation_messages WHERE session_id = ?`, a.Session).Scan(&seq); err != nil {
			return err
		}
		inserted, err := q.ExecContext(ctx, `
			INSERT INTO conversation_messages
				(session_id, seq, direction, surface, channel_ref, body, attachments, source_message_id)
			VALUES (?, ?, 'inbound', ?, ?, ?, ?, NULLIF(?,''))`,
			a.Session, seq, from.Surface, from.ChannelRef, a.Body, attachmentValue, a.SourceMessage)
		if err != nil {
			return err
		}
		messageID, err = inserted.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE collie_sessions SET last_activity_at = datetime('now')
			WHERE id = ? AND status = 'active'`, a.Session); err != nil {
			return err
		}
		payload, err := json.Marshal(collieEchoPayload{
			MessageID: messageID, Seq: seq, Body: a.Body,
			Attachments: attachments, Origin: from,
		})
		if err != nil {
			return err
		}
		rows, err := q.QueryContext(ctx, `
			SELECT surface, channel_ref FROM collie_bindings
			WHERE session_id = ? AND active = 1
			  AND NOT (surface = ? AND channel_ref = ?)
			ORDER BY surface, channel_ref, id`,
			a.Session, from.Surface, from.ChannelRef)
		if err != nil {
			return err
		}
		var destinations []SurfaceRef
		for rows.Next() {
			var destination SurfaceRef
			if err := rows.Scan(&destination.Surface, &destination.ChannelRef); err != nil {
				rows.Close()
				return err
			}
			destinations = append(destinations, destination)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, destination := range destinations {
			if _, err := insertFanoutOutboxRow(ctx, q, "collie_echo",
				a.Session, destination.Surface, destination.ChannelRef, string(payload)); err != nil {
				return err
			}
			echoes++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"session_id": a.Session, "message_id": messageID, "seq": seq,
		"echoes": echoes, "duplicate": duplicate,
	}, nil
}

// requireCollieRunner fences the conversation transport verbs to the Collie
// runner's own-session identity: run.json tier `collie` naming this
// exact session. Like register-session, this is the runner's private scope —
// the frozen verb_allowlist governs only the model's operator verbs and is
// deliberately never consulted here. Host and pipeline callers own no
// conversation transport.
func requireCollieRunner(id *RunIdentity, sessionID, verb string) error {
	if id == nil || id.Tier != "collie" {
		return Domainf("%s is the Collie runner's own-session transport scope", verb)
	}
	if id.RunID == "" || id.RunID != sessionID {
		return Domainf("a Collie runner touches only its own conversation; run.json identifies %q, target is %q", id.RunID, sessionID)
	}
	return nil
}

// requireActiveCollieSessionTx requires the canonical registry row to be live
// inside the caller's write transaction: transport on an ended or reaped
// session is inert. Resume is an explicit host transition, never a side
// effect of a zombie runner's traffic. The rejection carries
// CodeSessionInactive so the runner can tell it from a transient failure.
func requireActiveCollieSessionTx(ctx context.Context, q Q, sessionID string) error {
	var status string
	err := q.QueryRowContext(ctx,
		`SELECT status FROM collie_sessions WHERE id = ?`, sessionID).Scan(&status)
	if err == sql.ErrNoRows {
		return Domainf("unknown Collie session %q", sessionID)
	}
	if err != nil {
		return err
	}
	if status != "active" {
		return domain.Errf(domain.CodeSessionInactive,
			"Collie session %q is %s; transport requires an active session", sessionID, status)
	}
	return nil
}

func loadCollieAttachmentClaims(
	ctx context.Context,
	q Q,
	messageID int64,
	sessionID string,
	legacy sql.NullString,
) ([]CollieAttachmentClaim, error) {
	if legacy.Valid {
		var values []any
		if err := json.Unmarshal([]byte(legacy.String), &values); err != nil || values == nil {
			return nil, Domainf("conversation message %d has malformed legacy attachments", messageID)
		}
		if len(values) != 0 {
			return nil, Domainf(
				"conversation message %d carries legacy path attachments; typed attachment claims are required",
				messageID,
			)
		}
	}
	rows, err := q.QueryContext(ctx, `
		SELECT r.ordinal,r.session_id,r.direction,r.sha256,r.size,
		       r.media_type,r.display_name,o.state
		FROM conversation_attachment_refs r
		LEFT JOIN attachment_objects o
		  ON o.session_id=r.session_id AND o.direction=r.direction
		 AND o.sha256=r.sha256 AND o.size=r.size
		WHERE r.message_id=?
		ORDER BY r.ordinal`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	claims := []CollieAttachmentClaim{}
	var total int64
	for rows.Next() {
		var claim CollieAttachmentClaim
		var state sql.NullString
		if err := rows.Scan(
			&claim.Ordinal, &claim.SessionID, &claim.Direction, &claim.SHA256,
			&claim.Size, &claim.MediaType, &claim.DisplayName, &state,
		); err != nil {
			return nil, err
		}
		if claim.Ordinal != int64(len(claims)+1) ||
			claim.SessionID != sessionID || claim.Direction != "in" ||
			!safeAttachmentHex(claim.SHA256, 64) ||
			claim.Size < 0 || claim.Size > maxAttachmentBytes ||
			total > maxAttachmentBatch-claim.Size ||
			!safeMediaType(claim.MediaType) ||
			!safeDisplayName(claim.DisplayName) ||
			!state.Valid || state.String != "live" {
			return nil, Domainf(
				"conversation message %d attachment claims are incomplete or no longer live",
				messageID,
			)
		}
		total += claim.Size
		claims = append(claims, claim)
		if len(claims) > maxAttachmentObjects {
			return nil, Domainf(
				"conversation message %d attachment claims exceed their sealed bound",
				messageID,
			)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return claims, nil
}

// CollieClaim returns the next authorized inbound message in the caller's conversation.
// Failed replies require an explicit retry. The claim preserves last_activity_at.
func CollieClaim(db *sql.DB, id *RunIdentity, sessionID string) (any, error) {
	if err := requireCollieRunner(id, sessionID, "mc collie claim"); err != nil {
		return nil, err
	}
	var message map[string]any
	err := inTx(db, func(ctx context.Context, q Q) error {
		if err := requireActiveCollieSessionTx(ctx, q, sessionID); err != nil {
			return err
		}
		var messageID, seq int64
		var surface, body, createdAt string
		var channelRef, attachmentJSON, claimedAt sql.NullString
		err := q.QueryRowContext(ctx, `
			SELECT id, seq, surface, channel_ref, body, attachments, created_at, claimed_at
			FROM conversation_messages
			WHERE session_id = ? AND direction = 'inbound' AND completed_at IS NULL
              AND COALESCE((SELECT state FROM collie_reply_attempts a WHERE a.message_id=conversation_messages.id ORDER BY a.rowid DESC LIMIT 1),'authorized') IN ('authorized','active')
			ORDER BY id LIMIT 1`, sessionID,
		).Scan(&messageID, &seq, &surface, &channelRef, &body, &attachmentJSON, &createdAt, &claimedAt)
		if err == sql.ErrNoRows {
			return nil // nothing pending is an ordinary poll outcome
		}
		if err != nil {
			return err
		}
		attempt, err := claimReplyAttempt(ctx, q, id, sessionID, messageID)
		if err != nil {
			return err
		}
		if attempt == "" {
			return nil
		}
		reclaimed := claimedAt.Valid
		if !reclaimed {
			if _, err := q.ExecContext(ctx, `
				UPDATE conversation_messages SET claimed_by = ?, claimed_at = datetime('now')
				WHERE id = ?`, sessionID, messageID); err != nil {
				return err
			}
			if err := q.QueryRowContext(ctx,
				`SELECT claimed_at FROM conversation_messages WHERE id = ?`, messageID,
			).Scan(&claimedAt); err != nil {
				return err
			}
		}
		attachments, err := loadCollieAttachmentClaims(
			ctx, q, messageID, sessionID, attachmentJSON)
		if err != nil {
			return err
		}
		var channel any
		if channelRef.Valid {
			channel = channelRef.String
		}
		message = map[string]any{
			"id": fmt.Sprint(messageID), "seq": fmt.Sprint(seq),
			"surface": surface, "channel_ref": channel,
			"body": body, "attachments": attachments, "created_at": createdAt,
			"claimed_at": claimedAt.String, "reclaimed": reclaimed, "attempt_id": attempt,
		}
		var project sql.NullString
		if err := q.QueryRowContext(ctx, `SELECT vision_project_id FROM collie_sessions WHERE id=?`, sessionID).Scan(&project); err != nil {
			return err
		}
		if project.Valid {
			vision, err := VisionDraftContext(ctx, q, project.String)
			if err != nil {
				return err
			}
			message["vision_context"] = vision
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"session_id": sessionID, "message": message}, nil
}

// collieBodyMaxBytes bounds a reply body and a draft body alike.
const collieBodyMaxBytes = 65536

type CollieDraftArgs struct {
	Session string
	To      int64
	Body    string
	Attempt string
}

// CollieDraft replaces the provisional reply text of a claimed, unreplied
// inbound turn of the runner's own session. The runner writes it while the
// harness turn runs; the reply supersedes it. The verb is frozen into the
// session allowlist at start, so a session frozen without it is refused.
func CollieDraft(db *sql.DB, id *RunIdentity, a CollieDraftArgs) (any, error) {
	if err := requireCollieRunner(id, a.Session, "mc collie draft"); err != nil {
		return nil, err
	}
	if a.Body == "" {
		return nil, Usagef("mc collie draft requires a body")
	}
	if len(a.Body) > collieBodyMaxBytes {
		return nil, Usagef("mc collie draft body must be at most %d bytes", collieBodyMaxBytes)
	}
	err := inTx(db, func(ctx context.Context, q Q) error {
		if err := requireActiveCollieSessionTx(ctx, q, a.Session); err != nil {
			return err
		}
		if err := requireActiveCollieVerb(ctx, q, id, "collie.draft"); err != nil {
			return err
		}
		var turnSession, direction string
		var claimedBy, completedAt sql.NullString
		err := q.QueryRowContext(ctx, `
			SELECT session_id, direction, claimed_by, completed_at
			FROM conversation_messages WHERE id = ?`, a.To,
		).Scan(&turnSession, &direction, &claimedBy, &completedAt)
		if err == sql.ErrNoRows {
			return Domainf("unknown conversation message %d", a.To)
		}
		if err != nil {
			return err
		}
		if turnSession != a.Session {
			return Domainf("message %d belongs to another conversation", a.To)
		}
		if direction != "inbound" {
			return Domainf("mc collie draft answers an inbound turn; message %d is a %s row", a.To, direction)
		}
		if !claimedBy.Valid {
			return Domainf("inbound turn %d is unclaimed; the runner claims a turn before drafting", a.To)
		}
		if completedAt.Valid {
			return Domainf("inbound turn %d already has its reply; a draft is meaningless", a.To)
		}
		if err := requireReplyAttempt(ctx, q, id, a.To, a.Attempt, false); err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, `
			UPDATE conversation_messages SET draft = ? WHERE id = ?`, a.Body, a.To)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"session_id": a.Session, "message_id": a.To, "bytes": len(a.Body),
	}, nil
}

type CollieReplyArgs struct {
	Session     string
	To          int64
	Body        string
	Attachments string
	Attempt     string
}

type collieReplyPayload struct {
	MessageID   int64    `json:"message_id"`
	Seq         int64    `json:"seq"`
	Body        string   `json:"body"`
	Attachments []string `json:"attachments"`
	ReplyTo     int64    `json:"reply_to"`
}

// CollieReply is the runner's outbound half of the turn: one
// transaction appends the reply row against the claimed inbound turn,
// completes the claim, advances activity time, and fans one collie_reply
// outbox row to EVERY active binding (a reply reads identically on every
// bound surface, origin included — unlike an inbound echo). Replay of a
// committed turn is idempotent for the identical logical reply and refused
// for any other: one inbound turn, one logical reply.
func CollieReply(db *sql.DB, id *RunIdentity, a CollieReplyArgs) (any, error) {
	if err := requireCollieRunner(id, a.Session, "mc collie reply"); err != nil {
		return nil, err
	}
	attachments, attachmentValue, err := parseAttachments("mc collie reply", a.Attachments)
	if err != nil {
		return nil, err
	}
	if a.Body == "" && len(attachments) == 0 {
		return nil, Usagef("mc collie reply requires a body or attachment")
	}
	if len(a.Body) > collieBodyMaxBytes {
		return nil, Usagef("mc collie reply body must be at most %d bytes", collieBodyMaxBytes)
	}
	var out map[string]any
	err = inTx(db, func(ctx context.Context, q Q) error {
		if err := requireActiveCollieSessionTx(ctx, q, a.Session); err != nil {
			return err
		}
		var turnSession, direction string
		var claimedBy, completedAt sql.NullString
		err := q.QueryRowContext(ctx, `
			SELECT session_id, direction, claimed_by, completed_at
			FROM conversation_messages WHERE id = ?`, a.To,
		).Scan(&turnSession, &direction, &claimedBy, &completedAt)
		if err == sql.ErrNoRows {
			return Domainf("unknown conversation message %d", a.To)
		}
		if err != nil {
			return err
		}
		if turnSession != a.Session {
			return Domainf("message %d belongs to another conversation", a.To)
		}
		if direction != "inbound" {
			return Domainf("mc collie reply answers an inbound turn; message %d is a %s row", a.To, direction)
		}
		if !claimedBy.Valid {
			return Domainf("inbound turn %d is unclaimed; the runner claims a turn before replying", a.To)
		}
		if completedAt.Valid {
			// Crash-after-commit retry: the turn's one logical reply exists.
			var replyID, replySeq int64
			var replyBody string
			var replyAttachments sql.NullString
			err := q.QueryRowContext(ctx, `
				SELECT id, seq, body, attachments FROM conversation_messages
				WHERE reply_to = ?`, a.To,
			).Scan(&replyID, &replySeq, &replyBody, &replyAttachments)
			if err == sql.ErrNoRows {
				return Domainf("inbound turn %d is completed but carries no reply row; refusing to guess", a.To)
			}
			if err != nil {
				return err
			}
			sameAttachments := (!replyAttachments.Valid && attachmentValue == nil) ||
				(replyAttachments.Valid && attachmentValue != nil && replyAttachments.String == attachmentValue.(string))
			if replyBody != a.Body || !sameAttachments {
				return Domainf("inbound turn %d already has its one logical reply", a.To)
			}
			out = map[string]any{
				"session_id": a.Session, "message_id": replyID, "seq": replySeq,
				"reply_to": a.To, "deliveries": 0, "replied": false,
			}
			return nil
		}
		if err := requireReplyAttempt(ctx, q, id, a.To, a.Attempt, false); err != nil {
			return err
		}

		var seq int64
		if err := q.QueryRowContext(ctx, `
			SELECT COALESCE(MAX(seq), 0) + 1
			FROM conversation_messages WHERE session_id = ?`, a.Session).Scan(&seq); err != nil {
			return err
		}
		inserted, err := q.ExecContext(ctx, `
			INSERT INTO conversation_messages
				(session_id, seq, direction, surface, channel_ref, body, attachments, reply_to)
			VALUES (?, ?, 'reply', 'collie', NULL, ?, ?, ?)`,
			a.Session, seq, a.Body, attachmentValue, a.To)
		if err != nil {
			return err
		}
		replyID, err := inserted.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE conversation_messages SET completed_at = datetime('now')
			WHERE id = ? AND completed_at IS NULL`, a.To); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE collie_sessions SET last_activity_at = datetime('now')
			WHERE id = ? AND status = 'active'`, a.Session); err != nil {
			return err
		}
		var attempt, launch string
		if err := q.QueryRowContext(ctx, `SELECT attempt_id,launch_id FROM collie_reply_attempts WHERE message_id=? AND ended_at IS NULL`, a.To).Scan(&attempt, &launch); err != nil {
			return err
		}
		if launch != id.Launch {
			return Domainf("The reply belongs to another launch.")
		}
		if err := finishReplyAttempt(ctx, q, attempt, "succeeded", "runner", "The reply completed."); err != nil {
			return err
		}
		payload, err := json.Marshal(collieReplyPayload{
			MessageID: replyID, Seq: seq, Body: a.Body,
			Attachments: attachments, ReplyTo: a.To,
		})
		if err != nil {
			return err
		}
		rows, err := q.QueryContext(ctx, `
			SELECT surface, channel_ref FROM collie_bindings
			WHERE session_id = ? AND active = 1
			ORDER BY surface, channel_ref, id`, a.Session)
		if err != nil {
			return err
		}
		var destinations []SurfaceRef
		for rows.Next() {
			var destination SurfaceRef
			if err := rows.Scan(&destination.Surface, &destination.ChannelRef); err != nil {
				rows.Close()
				return err
			}
			destinations = append(destinations, destination)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		deliveries := 0
		for _, destination := range destinations {
			if _, err := insertFanoutOutboxRow(ctx, q, "collie_reply",
				a.Session, destination.Surface, destination.ChannelRef, string(payload)); err != nil {
				return err
			}
			deliveries++
		}
		out = map[string]any{
			"session_id": a.Session, "message_id": replyID, "seq": seq,
			"reply_to": a.To, "deliveries": deliveries, "replied": true,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func sortedUniqueStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	uniq := out[:0]
	for _, value := range out {
		if len(uniq) == 0 || uniq[len(uniq)-1] != value {
			uniq = append(uniq, value)
		}
	}
	return uniq
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// requireActiveCollieVerb fences Collie-agent state access to the canonical
// active registry row and its persisted frozen allowlist. Mutation callers
// invoke this helper inside their write transaction so an ended zombie can
// never race a preflight check.
func requireActiveCollieVerb(ctx context.Context, q Q, id *RunIdentity, verb string) error {
	if id == nil || id.Tier != "collie" || id.RunID == "" {
		return Domainf("%s requires an active Collie session identity", verb)
	}
	var status, persistedJSON string
	err := q.QueryRowContext(ctx, `
		SELECT status, verb_allowlist FROM collie_sessions WHERE id = ?`, id.RunID,
	).Scan(&status, &persistedJSON)
	if err == sql.ErrNoRows {
		return Domainf("unknown Collie session %q", id.RunID)
	}
	if err != nil {
		return err
	}
	if status != "active" {
		return Domainf("Collie session %q is not active (status %s)", id.RunID, status)
	}
	var persisted []string
	if err := json.Unmarshal([]byte(persistedJSON), &persisted); err != nil {
		return fmt.Errorf("parse frozen allowlist for Collie session %s: %w", id.RunID, err)
	}
	persisted = sortedUniqueStrings(persisted)
	envelope := sortedUniqueStrings(id.VerbAllowlist)
	if !equalStrings(persisted, envelope) {
		return Domainf("Collie session %q envelope allowlist does not match its frozen registry value", id.RunID)
	}
	for _, allowed := range persisted {
		if allowed == verb {
			return nil
		}
	}
	return Domainf("Collie session is not allowed operator verb %s", verb)
}

func requireOperatorVerbTx(ctx context.Context, q Q, id *RunIdentity, verb string) error {
	if id == nil { // host/operator provenance
		return nil
	}
	return requireActiveCollieVerb(ctx, q, id, verb)
}

type collieListRow struct {
	Title            any
	FirstMessage     any
	VisionProject    any
	Packet           any
	ID               string
	Status           string
	CreatedAt        string
	LastActivityAt   string
	ContainerName    string
	VerbAllowlist    []string
	SessionPath      string
	Binding          string
	NativeSessionRef any
	TraceFilename    any
	ActiveBindings   []map[string]any
	// KnownBindings retains inactive rows too. A native surface needs that
	// historical place only to select the explicit `collie resume` path; it may
	// never infer a different channel for an ended conversation.
	KnownBindings []map[string]any
}

func (r collieListRow) jsonMap() map[string]any {
	return map[string]any{
		"id": r.ID, "title": r.Title, "first_message": r.FirstMessage, "status": r.Status,
		"vision_project": r.VisionProject, "packet": r.Packet,
		"created_at": r.CreatedAt, "last_activity_at": r.LastActivityAt,
		"container_name": r.ContainerName, "verb_allowlist": r.VerbAllowlist,
		"session_path": r.SessionPath, "binding": r.Binding,
		"native_session_ref": r.NativeSessionRef, "trace_filename": r.TraceFilename,
		"agentsview":      agentsViewSession(r.Binding, r.NativeSessionRef),
		"active_bindings": r.ActiveBindings,
		"known_bindings":  r.KnownBindings,
	}
}

// CollieList is host-all (including ended/reaped resumable rows) or one
// allowlisted Collie agent's own active session.
func CollieList(db *sql.DB, id *RunIdentity) (any, error) {
	where := " WHERE s.deleted_at IS NULL"
	args := []any{}
	if id != nil {
		if id.Tier != "collie" {
			return nil, Domainf("mc collie list is host or Collie-agent scope; run.json tier is %q", id.Tier)
		}
		if err := requireActiveCollieVerb(context.Background(), db, id, "collie.list"); err != nil {
			return nil, classify(err)
		}
		where += " AND s.id = ?"
		args = append(args, id.RunID)
	}
	// The newest conversation of a packet is its current conversation, unless
	// the operator deleted it.
	rows, err := db.Query(`
		SELECT s.id, s.status, s.created_at, s.last_activity_at, s.container_name,
		       s.verb_allowlist, s.session_path, s.binding,
		       s.native_session_ref, s.trace_filename, s.vision_project_id, s.title,
		       (SELECT substr(body,1,240) FROM conversation_messages
		        WHERE session_id=s.id AND direction='inbound' ORDER BY seq LIMIT 1),
		       s.packet_id, p.version_id, p.task_id, t.title, t.project,
		       NOT EXISTS (SELECT 1 FROM collie_sessions n WHERE n.packet_id = s.packet_id AND n.rowid > s.rowid)
		FROM collie_sessions s
		LEFT JOIN review_versions p ON p.id = s.packet_id
		LEFT JOIN tasks t ON t.id = p.task_id`+where+` ORDER BY s.rowid`, args...)
	if err != nil {
		return nil, classify(err)
	}
	var sessions []collieListRow
	for rows.Next() {
		var row collieListRow
		var allowJSON string
		var nativeRef, traceFile, visionProject, title, firstMessage, taskTitle, project sql.NullString
		var packet, version, task sql.NullInt64
		var current bool
		if err := rows.Scan(
			&row.ID, &row.Status, &row.CreatedAt, &row.LastActivityAt,
			&row.ContainerName, &allowJSON, &row.SessionPath, &row.Binding,
			&nativeRef, &traceFile, &visionProject, &title, &firstMessage,
			&packet, &version, &task, &taskTitle, &project, &current,
		); err != nil {
			rows.Close()
			return nil, classify(err)
		}
		if err := json.Unmarshal([]byte(allowJSON), &row.VerbAllowlist); err != nil {
			rows.Close()
			return nil, classify(fmt.Errorf("parse Collie session %s allowlist: %w", row.ID, err))
		}
		if title.Valid {
			row.Title = title.String
		}
		if firstMessage.Valid {
			row.FirstMessage = firstMessage.String
		}
		if visionProject.Valid {
			row.VisionProject = visionProject.String
		}
		if packet.Valid {
			row.Packet = map[string]any{
				"packet_id": packet.Int64, "version_id": version.Int64, "task_id": task.Int64,
				"task_title": taskTitle.String, "project_id": project.String, "current": current,
			}
		}
		if nativeRef.Valid {
			row.NativeSessionRef = nativeRef.String
		}
		if traceFile.Valid {
			row.TraceFilename = traceFile.String
		}
		sessions = append(sessions, row)
	}
	if err := rows.Close(); err != nil {
		return nil, classify(err)
	}
	if err := rows.Err(); err != nil {
		return nil, classify(err)
	}

	out := make([]map[string]any, 0, len(sessions))
	for i := range sessions {
		bindingRows, err := db.Query(`
			SELECT CAST(id AS TEXT) AS binding_id, surface, channel_ref, bound_at
			FROM collie_bindings
			WHERE session_id = ? AND active = 1
			ORDER BY surface, channel_ref, id`, sessions[i].ID)
		if err != nil {
			return nil, classify(err)
		}
		bindings, err := rowsToMaps(bindingRows)
		bindingRows.Close()
		if err != nil {
			return nil, classify(err)
		}
		sessions[i].ActiveBindings = bindings
		knownRows, err := db.Query(`
			SELECT CAST(id AS TEXT) AS binding_id, surface, channel_ref, active, bound_at
			FROM collie_bindings
			WHERE session_id = ?
			ORDER BY active DESC, bound_at DESC, id DESC`, sessions[i].ID)
		if err != nil {
			return nil, classify(err)
		}
		known, err := rowsToMaps(knownRows)
		knownRows.Close()
		if err != nil {
			return nil, classify(err)
		}
		sessions[i].KnownBindings = known
		item := sessions[i].jsonMap()
		var guild, parent, thread string
		var initialReceived bool
		var retired sql.NullString
		err = db.QueryRow(`SELECT guild_id,channel_id,thread_id,retired_at,EXISTS(SELECT 1 FROM conversation_messages m WHERE m.session_id=t.session_id AND m.source_message_id=t.thread_id) FROM collie_discord_threads t WHERE session_id=?`, sessions[i].ID).Scan(&guild, &parent, &thread, &retired, &initialReceived)
		if err == nil {
			item["discord_thread"] = map[string]any{"guild_id": guild, "channel_id": parent, "thread_id": thread, "retired": retired.Valid, "initial_received": initialReceived}
		} else if err != sql.ErrNoRows {
			return nil, classify(err)
		}
		out = append(out, item)
	}
	return map[string]any{"sessions": out}, nil
}

// collieFollowDeadline bounds how long `--follow` waits for the next reply
// before returning the transcript as it stands, and collieFollowInterval is how
// often it re-looks.
//
// A compiled-in pair rather than a tunable: a new `timing.*` key is a config
// surface, and the caller needs the flag, not a knob. Expiry is not an
// error — the caller re-invokes, which is the same loop it would have written
// by hand, except it costs one query per two minutes instead of one per second.
// Two minutes is roughly a conversational agent turn; a session that has gone
// quiet surfaces as an unchanged transcript rather than as a hang.
// Vars rather than consts for exactly one reason: the suite shrinks them so the
// expiry path is proven in milliseconds instead of two minutes. Nothing outside
// tests writes them, and neither is reachable from any operator surface.
var (
	collieFollowDeadline = 120 * time.Second
	collieFollowInterval = time.Second
)

// CollieHistory reads the one durable transcript in stable sequence order.
// Host may read any retained session; a Collie agent is canonical-active,
// allowlisted, and restricted to its own conversation.
//
// `follow` is the CLI's read channel over the conversation rows: the caller
// reads the conversation back with `mc collie history --follow` and needs no
// delivery loop. The CLI surface has no delivery loop of its own — no
// websocket, no outbox consumer — so without this every caller writes the
// same poll by hand, which is exactly what the operator's own daily ritual
// does today.
//
// It waits for the next `reply` row, not merely the next row: an `inbound` row
// is the caller's own message coming back, so waking on it would return
// instantly and follow nothing. It also returns as soon as the session leaves
// `active`, because an ended session will never produce another reply and
// waiting out the deadline would report a hang that is really a terminal.
//
// REFUSED FOR AGENT CALLERS, and not as a matter of taste. A Collie agent
// invoking this inside its own turn would block waiting for the reply that only
// its own turn can produce — a self-deadlock, bounded only by the deadline
// above, spending the turn on nothing. The read itself stays open to agents;
// only the waiting is host-only.
func CollieHistory(db *sql.DB, id *RunIdentity, sessionID string, follow bool) (any, error) {
	if id != nil {
		if follow {
			return nil, Domainf("mc collie history --follow is host scope; a Collie agent would wait on its own reply")
		}
		if id.Tier != "collie" {
			return nil, Domainf("mc collie history is host or Collie-agent scope; run.json tier is %q", id.Tier)
		}
		if id.RunID != sessionID {
			return nil, Domainf("a Collie agent may read only its own session; caller is %q, target is %q", id.RunID, sessionID)
		}
		if err := requireActiveCollieVerb(context.Background(), db, id, "collie.history"); err != nil {
			return nil, classify(err)
		}
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM collie_sessions WHERE id = ? AND deleted_at IS NULL`, sessionID).Scan(&status); err == sql.ErrNoRows {
		return nil, Domainf("unknown Collie session %q", sessionID)
	} else if err != nil {
		return nil, classify(err)
	}
	if follow {
		// The waiting happens BEFORE the transcript read, so what comes back is
		// one consistent snapshot taken after the wait ended rather than a
		// pre-wait read with a reply bolted on.
		newStatus, err := waitForCollieReply(db, sessionID, status)
		if err != nil {
			return nil, err
		}
		status = newStatus
	}
	// A draft is meaningful only until the turn's reply exists: the reply
	// completes the turn in the same transaction that inserts it.
	rows, err := db.Query(`
		SELECT id, seq, direction, surface, channel_ref, body, attachments, created_at,
		       reply_to, CASE WHEN completed_at IS NULL THEN draft END
		FROM conversation_messages WHERE session_id = ? ORDER BY seq, id`, sessionID)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()
	messages := []map[string]any{}
	for rows.Next() {
		var messageID, seq int64
		var direction, surface, body, createdAt string
		var channelRef, attachmentJSON, draft sql.NullString
		var replyTo sql.NullInt64
		if err := rows.Scan(
			&messageID, &seq, &direction, &surface, &channelRef,
			&body, &attachmentJSON, &createdAt, &replyTo, &draft,
		); err != nil {
			return nil, classify(err)
		}
		var attachments any = []string{}
		if attachmentJSON.Valid {
			legacy := []string{}
			if err := json.Unmarshal([]byte(attachmentJSON.String), &legacy); err != nil {
				return nil, classify(fmt.Errorf("parse attachments for conversation message %d: %w", messageID, err))
			}
			attachments = legacy
		}
		var channel, replyToValue, draftValue any
		if channelRef.Valid {
			channel = channelRef.String
		}
		if replyTo.Valid {
			replyToValue = replyTo.Int64
		}
		if draft.Valid {
			draftValue = draft.String
		}
		messages = append(messages, map[string]any{
			"id": messageID, "seq": seq, "direction": direction,
			"surface": surface, "channel_ref": channel, "body": body,
			"attachments": attachments, "created_at": createdAt,
			"reply_to": replyToValue, "draft": draftValue,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, classify(err)
	}
	rows.Close()
	for _, message := range messages {
		var attempt, state string
		err := db.QueryRow(`SELECT attempt_id,state FROM collie_reply_attempts WHERE message_id=? ORDER BY rowid DESC LIMIT 1`, message["id"]).Scan(&attempt, &state)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		if err == nil {
			message["attempt_id"] = attempt
			message["reply_state"] = state
		}
	}
	if err := attachCollieHistoryClaims(db, sessionID, messages); err != nil {
		return nil, err
	}
	return map[string]any{
		"session_id": sessionID, "status": status, "messages": messages,
	}, nil
}

// attachCollieHistoryClaims surfaces each message's typed attachment claims
// into the transcript read, identity only — session, direction,
// sha256, size plus the declared metadata. That identity is the exact
// addressing the byte-serving read (`mc __attachment-bytes`) accepts, so a
// transcript reader can reach a claim's bytes without deriving anything. The
// legacy attachments column, where a pre-typed transcript still carries it,
// keeps its string form untouched.
func attachCollieHistoryClaims(db *sql.DB, sessionID string, messages []map[string]any) error {
	rows, err := db.Query(`
		SELECT r.message_id, r.ordinal, r.session_id, r.direction, r.sha256,
		       r.size, r.media_type, r.display_name
		FROM conversation_attachment_refs r
		WHERE r.session_id = ? ORDER BY r.message_id, r.ordinal`, sessionID)
	if err != nil {
		return classify(err)
	}
	defer rows.Close()
	claims := map[int64][]CollieAttachmentClaim{}
	for rows.Next() {
		var messageID int64
		var claim CollieAttachmentClaim
		if err := rows.Scan(
			&messageID, &claim.Ordinal, &claim.SessionID, &claim.Direction,
			&claim.SHA256, &claim.Size, &claim.MediaType, &claim.DisplayName,
		); err != nil {
			return classify(err)
		}
		claims[messageID] = append(claims[messageID], claim)
	}
	if err := rows.Err(); err != nil {
		return classify(err)
	}
	for _, message := range messages {
		id, ok := message["id"].(int64)
		if !ok {
			continue
		}
		if typed, carried := claims[id]; carried {
			message["attachments"] = typed
		}
	}
	return nil
}

// waitForCollieReply blocks until the session produces a reply it did not
// already have, or leaves `active`, or the deadline expires. It returns the
// session status as of the moment it stopped waiting, so the caller's snapshot
// reports the status it actually observed rather than the one it started with.
//
// The baseline is the highest `reply` seq at entry, read once. Keying on the
// count would miss nothing here (rows are append-only) but keying on the max
// seq is what makes a caller that invokes twice in a row wait for a
// genuinely NEW reply both times.
//
// A read error inside the loop is returned rather than retried: the loop's only
// job is to notice a row appear, and a spine that cannot be read is a condition
// the caller must see, not one to spin on for two minutes.
func waitForCollieReply(db *sql.DB, sessionID, status string) (string, error) {
	if status != "active" {
		// Nothing will ever arrive. Returning immediately is not a failure —
		// the transcript is complete, which is the honest answer to "follow".
		return status, nil
	}
	baseline, err := latestCollieReplySeq(db, sessionID)
	if err != nil {
		return status, err
	}
	for waited := time.Duration(0); waited < collieFollowDeadline; waited += collieFollowInterval {
		time.Sleep(collieFollowInterval)
		seq, err := latestCollieReplySeq(db, sessionID)
		if err != nil {
			return status, err
		}
		if seq > baseline {
			break
		}
		var current string
		if err := db.QueryRow(`SELECT status FROM collie_sessions WHERE id = ?`, sessionID).Scan(&current); err != nil {
			return status, classify(err)
		}
		if current != "active" {
			// Ended or reaped while waiting. An ended session produces no further
			// reply, so continuing would spend the deadline reporting a hang that
			// is really a terminal.
			return current, nil
		}
	}
	var current string
	if err := db.QueryRow(`SELECT status FROM collie_sessions WHERE id = ?`, sessionID).Scan(&current); err != nil {
		return status, classify(err)
	}
	return current, nil
}

func latestCollieReplySeq(db *sql.DB, sessionID string) (int64, error) {
	var seq sql.NullInt64
	if err := db.QueryRow(`
		SELECT MAX(seq) FROM conversation_messages
		WHERE session_id = ? AND direction = 'reply'`, sessionID).Scan(&seq); err != nil {
		return 0, classify(err)
	}
	if !seq.Valid {
		return 0, nil
	}
	return seq.Int64, nil
}

// CollieDelete removes an ordinary conversation or a packet conversation from
// operator access.
// Records remain available for container cleanup and internal references.
func CollieDelete(db *sql.DB, id *RunIdentity, sessionID string) (any, error) {
	if err := RequireHostScope(id, "mc collie delete"); err != nil {
		return nil, err
	}
	err := inTx(db, func(ctx context.Context, q Q) error {
		var project, deleted sql.NullString
		err := q.QueryRowContext(ctx, `SELECT vision_project_id,deleted_at FROM collie_sessions WHERE id=?`, sessionID).Scan(&project, &deleted)
		if err == sql.ErrNoRows {
			return Domainf("unknown Collie session %q", sessionID)
		}
		if err != nil {
			return err
		}
		if project.Valid {
			return Domainf("Use the Vision page to clear its conversation.")
		}
		if deleted.Valid {
			return nil
		}
		if _, _, err := collieEndTx(ctx, q, "operator", sessionID, "The operator deleted the conversation."); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `UPDATE collie_sessions SET deleted_at=datetime('now') WHERE id=?`, sessionID); err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, `INSERT INTO activity (actor,kind,subject,detail) VALUES ('operator','collie.deleted',?,'The operator deleted the conversation.')`, sessionID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"session_id": sessionID, "deleted": true}, nil
}

// CollieEnd changes only durable conversation state. The substrate trigger
// deactivates bindings; the next resident sweep owns any container stop.
// Host retries are idempotent. A Collie agent may end only its own active,
// canonically allowlisted session.
func CollieEnd(db *sql.DB, id *RunIdentity, sessionID, reason string) (any, error) {
	if reason == "" {
		return nil, Usagef("mc collie end requires --reason")
	}
	if id != nil {
		if id.Tier != "collie" {
			return nil, Domainf("mc collie end is host or Collie-agent scope; run.json tier is %q", id.Tier)
		}
		if id.RunID != sessionID {
			return nil, Domainf("a Collie agent may end only its own session; caller is %q, target is %q", id.RunID, sessionID)
		}
		if err := RequireOperatorVerb(id, "collie.end"); err != nil {
			return nil, err
		}
	}
	ended := false
	status := ""
	err := inTx(db, func(ctx context.Context, q Q) error {
		if id != nil {
			if err := requireActiveCollieVerb(ctx, q, id, "collie.end"); err != nil {
				return err
			}
		}
		actor := "operator"
		if id != nil {
			actor = "collie"
		}
		var err error
		ended, status, err = collieEndTx(ctx, q, actor, sessionID, reason)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"session_id": sessionID, "status": status, "ended": ended,
	}, nil
}

// collieEndTx is the end transaction's body: `active -> ended` plus
// one `collie.ended` activity carrying the reason, with the substrate trigger
// deactivating the bindings alongside it.
//
// It takes a Q rather than a *sql.DB so that a caller which already holds a
// transaction can end a session inside it. The dispatch seam is that
// caller: a policy-invalid Collie must be ended in the *same* transaction that
// refused it, or it stays the repeatedly selected oldest active row and starves
// pipeline work. Opening a second transaction from inside the first would
// deadlock on the spine's single writer.
//
// The caller owns authorization: CollieEnd checks caller scope and the frozen
// verb allowlist before calling in, and the dispatch seam acts as the system.
func collieEndTx(ctx context.Context, q Q, actor, sessionID, reason string) (bool, string, error) {
	if reason == "" {
		return false, "", Usagef("ending a Collie session requires a reason")
	}
	var status string
	err := q.QueryRowContext(ctx,
		`SELECT status FROM collie_sessions WHERE id = ?`, sessionID).Scan(&status)
	if err == sql.ErrNoRows {
		return false, "", Domainf("unknown Collie session %q", sessionID)
	}
	if err != nil {
		return false, "", err
	}
	if status != "active" {
		return false, status, nil // host/surface retry: preserve ended or reaped truth
	}
	terminal := []string{}
	if actor == "operator" || actor == "collie" {
		terminal = []string{"cancelled"}
	}
	if err := failSessionReplies(ctx, q, sessionID, "The session ended before the reply completed.", terminal...); err != nil {
		return false, "", err
	}
	if _, err := q.ExecContext(ctx,
		`UPDATE collie_sessions SET status = 'ended' WHERE id = ? AND status = 'active'`,
		sessionID); err != nil {
		return false, "", err
	}
	if _, err := q.ExecContext(ctx, `
		INSERT INTO activity (actor, kind, subject, detail)
		VALUES (?, 'collie.ended', ?, ?)`, actor, sessionID, reason); err != nil {
		return false, "", err
	}
	return true, "ended", nil
}

// CollieLaunchBind is the resident's private pre-start bind receipt: after
// creating and inspecting a Collie container but before starting it, the
// resident CASes the exact Docker id onto the session's current launch. It
// is idempotent — the same launch plus the same container id returns the
// original receipt — and fenced: a stale launch, an ended session, or a
// different already-bound container binds nothing and returns fenced:false so
// the resident abandons the container. The non-idle guard refuses to bind a
// launch whose session has idled past collie_idle_timeout_s; that session is
// owed an end, not a container.
func CollieLaunchBind(db *sql.DB, id *RunIdentity, sessionID, launchID, containerID string) (any, error) {
	if err := RequireHostScope(id, "mc collie launch-bind"); err != nil {
		return nil, err
	}
	if !validLowercaseHex(launchID, 16) {
		return nil, Usagef("mc collie launch-bind --launch must be exactly 16 lowercase hex characters")
	}
	if !validLowercaseHex(containerID, 64) {
		return nil, Usagef("mc collie launch-bind --container-id must be exactly 64 lowercase hex characters")
	}
	result := map[string]any{"session": sessionID, "launch": launchID, "container_id": containerID}
	err := inTx(db, func(ctx context.Context, q Q) error {
		var status string
		var curLaunch, curContainer, boundAt, lastActivity sql.NullString
		var idleTimeout int64
		err := q.QueryRowContext(ctx, `
			SELECT s.status, s.current_launch_id, s.current_container_id,
			       s.launch_bound_at, s.last_activity_at,
			       (SELECT collie_idle_timeout_s FROM settings WHERE id = 1)
			FROM collie_sessions s WHERE s.id = ?`, sessionID).Scan(
			&status, &curLaunch, &curContainer, &boundAt, &lastActivity, &idleTimeout)
		if err == sql.ErrNoRows {
			return Domainf("unknown Collie session %q", sessionID)
		}
		if err != nil {
			return err
		}
		// A late bind from an old launch (session ended, or the current launch
		// moved on) binds nothing: fenced:false.
		if status != "active" || !curLaunch.Valid || curLaunch.String != launchID {
			result["fenced"] = false
			return nil
		}
		// Idempotent success iff already bound to this exact container; a
		// different bound id is fenced.
		if curContainer.Valid {
			result["fenced"] = curContainer.String == containerID
			if curContainer.String == containerID {
				result["bound_at"] = boundAt.String
			}
			return nil
		}
		// The launch must bind while the session is still non-idle.
		var live int
		if err := q.QueryRowContext(ctx, `
			SELECT CASE WHEN datetime('now') < datetime(?, '+' || ? || ' seconds')
			            THEN 1 ELSE 0 END`,
			lastActivity.String, idleTimeout).Scan(&live); err != nil {
			return err
		}
		if live == 0 {
			result["fenced"] = false
			return nil
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE collie_sessions
			SET current_container_id = ?, launch_bound_at = datetime('now')
			WHERE id = ? AND status = 'active' AND current_launch_id = ?
			  AND current_container_id IS NULL`,
			containerID, sessionID, launchID); err != nil {
			return err
		}
		var newBound string
		if err := q.QueryRowContext(ctx,
			`SELECT launch_bound_at FROM collie_sessions WHERE id = ?`, sessionID).Scan(&newBound); err != nil {
			return err
		}
		result["fenced"] = true
		result["bound_at"] = newBound
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// CollieRunnerStarted is the runner's private lifecycle receipt:
// after the native harness has successfully started or resumed, the runner
// CASes launch_started_at onto its own session's bound launch. It is fenced to
// the current launch and its bound container, idempotent (a second call
// returns the original start), and repeats the non-idle guard. It runs under
// the runner's own tier:"collie" identity, never the model's allowlist.
func CollieRunnerStarted(db *sql.DB, id *RunIdentity, sessionID, launchID, containerID string) (any, error) {
	if err := requireCollieRunner(id, sessionID, "mc collie runner-started"); err != nil {
		return nil, err
	}
	if !validLowercaseHex(launchID, 16) {
		return nil, Usagef("mc collie runner-started --launch must be exactly 16 lowercase hex characters")
	}
	if !validLowercaseHex(containerID, 64) {
		return nil, Usagef("mc collie runner-started --container-id must be exactly 64 lowercase hex characters")
	}
	result := map[string]any{"session": sessionID, "launch": launchID, "container_id": containerID}
	err := inTx(db, func(ctx context.Context, q Q) error {
		var status string
		var curLaunch, curContainer, startedAt, lastActivity sql.NullString
		var idleTimeout int64
		err := q.QueryRowContext(ctx, `
			SELECT s.status, s.current_launch_id, s.current_container_id,
			       s.launch_started_at, s.last_activity_at,
			       (SELECT collie_idle_timeout_s FROM settings WHERE id = 1)
			FROM collie_sessions s WHERE s.id = ?`, sessionID).Scan(
			&status, &curLaunch, &curContainer, &startedAt, &lastActivity, &idleTimeout)
		if err == sql.ErrNoRows {
			return Domainf("unknown Collie session %q", sessionID)
		}
		if err != nil {
			return err
		}
		// A start from an old launch or a container that is not the bound one
		// stamps nothing: fenced:false.
		if status != "active" || !curLaunch.Valid || curLaunch.String != launchID ||
			!curContainer.Valid || curContainer.String != containerID {
			result["fenced"] = false
			return nil
		}
		// Idempotent: already started returns the original stamp.
		if startedAt.Valid {
			result["fenced"] = true
			result["started_at"] = startedAt.String
			return nil
		}
		var live int
		if err := q.QueryRowContext(ctx, `
			SELECT CASE WHEN datetime('now') < datetime(?, '+' || ? || ' seconds')
			            THEN 1 ELSE 0 END`,
			lastActivity.String, idleTimeout).Scan(&live); err != nil {
			return err
		}
		if live == 0 {
			result["fenced"] = false
			return nil
		}
		if _, err := q.ExecContext(ctx, `
			UPDATE collie_sessions SET launch_started_at = datetime('now')
			WHERE id = ? AND status = 'active' AND current_launch_id = ?
			  AND current_container_id = ? AND launch_started_at IS NULL`,
			sessionID, launchID, containerID); err != nil {
			return err
		}
		var newStarted string
		if err := q.QueryRowContext(ctx,
			`SELECT launch_started_at FROM collie_sessions WHERE id = ?`, sessionID).Scan(&newStarted); err != nil {
			return err
		}
		result["fenced"] = true
		result["started_at"] = newStarted
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// CollieExit is the resident-observed exit receipt, private host
// scope: `mc collie exit <session> --launch <id> --container-id <id> --reason`.
// It CASes the current active launch and its bound container and is
// idempotent. If this launch's runner started, it ends the exact session. A
// pre-runner-start launch instead clears the confirmed-exited bound pair and
// retains the same launch/mode as effect debt, so the selector re-launches a
// fresh container for the same generation. Session-wide native locators do not
// prove that the current generation started: they may predate it. A late exit
// from an old launch (or an already-processed session) is fenced:false and
// changes nothing.
func CollieExit(db *sql.DB, id *RunIdentity, sessionID, launchID, containerID, reason string) (any, error) {
	if err := RequireHostScope(id, "mc collie exit"); err != nil {
		return nil, err
	}
	if !validLowercaseHex(launchID, 16) {
		return nil, Usagef("mc collie exit --launch must be exactly 16 lowercase hex characters")
	}
	if !validLowercaseHex(containerID, 64) {
		return nil, Usagef("mc collie exit --container-id must be exactly 64 lowercase hex characters")
	}
	if reason == "" {
		return nil, Usagef("mc collie exit requires --reason")
	}
	result := map[string]any{"session": sessionID, "launch": launchID, "container_id": containerID, "reason": reason}
	err := inTx(db, func(ctx context.Context, q Q) error {
		var status string
		var curLaunch, curContainer, startedAt sql.NullString
		err := q.QueryRowContext(ctx, `
			SELECT status, current_launch_id, current_container_id,
			       launch_started_at
			FROM collie_sessions WHERE id = ?`, sessionID).Scan(
			&status, &curLaunch, &curContainer, &startedAt)
		if err == sql.ErrNoRows {
			return Domainf("unknown Collie session %q", sessionID)
		}
		if err != nil {
			return err
		}
		// A late exit from an old launch, or an already-processed session,
		// changes nothing: fenced:false, idempotent.
		if status != "active" || !curLaunch.Valid || curLaunch.String != launchID ||
			!curContainer.Valid || curContainer.String != containerID {
			result["fenced"] = false
			return nil
		}
		if err := failSessionReplies(ctx, q, sessionID, "The Collie process exited before its reply completed."); err != nil {
			return err
		}
		if startedAt.Valid {
			// This exact launch reached its runner: end the session.
			if _, _, err := collieEndTx(ctx, q, "resident", sessionID, reason); err != nil {
				return err
			}
			result["fenced"] = true
			result["ended"] = true
			return nil
		}
		// Pre-runner-start null-locator launch: clear the confirmed-exited bound
		// pair; the launch/mode stays as effect debt for the selector to retry.
		if _, err := q.ExecContext(ctx, `
			UPDATE collie_sessions SET current_container_id = NULL, launch_bound_at = NULL
			WHERE id = ? AND status = 'active' AND current_launch_id = ? AND current_container_id = ?`,
			sessionID, launchID, containerID); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO activity (actor, kind, subject, detail)
			VALUES ('resident', 'collie.launch_cleared', ?, ?)`, sessionID, reason); err != nil {
			return err
		}
		result["fenced"] = true
		result["ended"] = false
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func validModelRequest(request string) bool {
	if len(request) < 16 || len(request) > 80 {
		return false
	}
	for _, r := range request {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}
