package verbs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mc/config"
	"mc/deployment"
	"mc/wiki"
)

const (
	AgentsViewAvailable   = "available"
	AgentsViewUnavailable = "unavailable"

	agentsViewPingTimeout    = 750 * time.Millisecond
	agentsViewHealthMaxAge   = 2 * time.Minute
	agentsViewHealthMaxSkew  = 5 * time.Second
	maxAgentsViewHealthBytes = 1 << 20
	maxOverdueNames          = 10

	agentsViewProductionOnly = "AgentsView runs only in a production deployment."
)

// agentsViewRepair returns the operator action that repairs the AgentsView
// fault of err. A rerun of install.sh keeps a port that another service holds
// and a token file that Culler cannot use. An HTTP status answer can come from
// another service or from AgentsView.
func agentsViewRepair(err error) string {
	switch {
	case errors.Is(err, deployment.ErrAnotherViewerService):
		return "Stop the other service, or ask your coding harness to set another AgentsView port in config.toml. Then run install.sh again."
	case errors.Is(err, deployment.ErrViewerHTTPStatus):
		return "If another service uses this port, stop that service, or ask your coding harness to set another AgentsView port in config.toml. Then run install.sh again."
	case errors.Is(err, deployment.ErrUnusableViewerSecret):
		return "Ask your coding harness to remove the AgentsView token file, then run install.sh again."
	}
	return "To repair AgentsView, run install.sh again."
}

// Viewers holds the addresses of the trace viewer and the wiki viewer, and
// the facts that the dashboard needs for its viewer links.
type Viewers struct {
	AgentsView AgentsViewViewer `json:"agentsview"`
	Wiki       WikiViewer       `json:"wiki"`
}

type AgentsViewViewer struct {
	URL   string `json:"url"`
	State string `json:"state"`
	// Reason is operator text. It is empty when AgentsView is available.
	Reason string `json:"reason"`
}

type WikiViewer struct {
	URL      string                `json:"url"`
	Projects map[string]WikiExport `json:"projects"`
}

type WikiExport struct {
	Commit string `json:"commit"`
	URL    string `json:"url"`
}

// AgentsViewPing returns nil when AgentsView answers at bind with the token
// of the deployment at home. The error text is operator text.
type AgentsViewPing func(ctx context.Context, home, bind string) error

// ViewerStatus reads the viewer addresses from config.toml, the wiki export
// records, and the answer of AgentsView. The conditions exist only when the
// deployment prepared an AgentsView unit.
func ViewerStatus(home string, now time.Time, ping AgentsViewPing) (Viewers, []SystemCondition) {
	viewers := Viewers{
		AgentsView: AgentsViewViewer{State: AgentsViewUnavailable},
		Wiki:       WikiViewer{Projects: map[string]WikiExport{}},
	}
	cfg, err := config.Load(filepath.Join(home, "config.toml"))
	if err != nil {
		viewers.AgentsView.Reason = "Culler cannot read the AgentsView address from config.toml."
		return viewers, nil
	}
	viewers.AgentsView.URL = viewerURL(cfg.Surfaces.AgentsView.Bind)
	viewers.Wiki.URL = viewerURL(cfg.Surfaces.Wiki.Bind)
	// Each export address holds the secret path prefix. Without the key, an
	// export has no address.
	key, keyErr := deployment.ReadWikiKey(home)
	for project, record := range wiki.ReadExports(deployment.WikiExportRoot(home)) {
		if ValidProjectID(project) {
			export := WikiExport{Commit: record.Commit}
			if keyErr == nil && viewers.Wiki.URL != "" {
				export.URL = viewers.Wiki.URL + "/" + key + "/" + url.PathEscape(project) + "/"
			}
			viewers.Wiki.Projects[project] = export
		}
	}
	if !agentsViewUnitPrepared(home) {
		viewers.AgentsView.Reason = agentsViewProductionOnly
		return viewers, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), agentsViewPingTimeout)
	defer cancel()
	if err := ping(ctx, home, cfg.Surfaces.AgentsView.Bind); err != nil {
		viewers.AgentsView.Reason = err.Error()
		return viewers, []SystemCondition{{
			ID: "agentsview:unavailable", Group: "execution", State: "failed", Name: "AgentsView",
			Condition: err.Error() + " Trace links do not open. Runs and Collie continue. " + agentsViewRepair(err),
			Action:    "View details",
		}}
	}
	viewers.AgentsView.State = AgentsViewAvailable
	if condition, ok := agentsViewOverdue(home, now); ok {
		return viewers, []SystemCondition{condition}
	}
	return viewers, nil
}

// AgentsViewAccess supplies the local proxy with the service address and token.
// The token appears in no log or browser response.
func AgentsViewAccess(home string) (any, error) {
	token, err := deployment.ReadAgentsViewToken(home)
	if err != nil {
		return nil, Domainf("%s %s", err.Error(), agentsViewRepair(err))
	}
	cfg, err := config.Load(filepath.Join(home, "config.toml"))
	if err != nil {
		return nil, Domainf("Culler cannot read the AgentsView address.")
	}
	return map[string]string{"url": viewerURL(cfg.Surfaces.AgentsView.Bind), "token": token}, nil
}

// viewerURL writes the bind host as the configuration names it. An IPv6 host
// gets brackets.
func viewerURL(bind string) string {
	host, port, err := net.SplitHostPort(bind)
	if err != nil {
		return ""
	}
	return "http://" + net.JoinHostPort(host, port)
}

func agentsViewUnitPrepared(home string) bool {
	info, err := os.Lstat(filepath.Join(home, "supervision", "LaunchAgents", "agentsview.plist"))
	return err == nil && info.Mode().IsRegular()
}

type agentsViewHealth struct {
	ObservedAt string `json:"observed_at"`
	Overdue    []struct {
		Kind              string `json:"kind"`
		ID                string `json:"id"`
		AgentsViewSession string `json:"agentsview_session"`
		ChangedAt         string `json:"changed_at"`
	} `json:"overdue"`
}

// agentsViewOverdue reads the resident health file of the trace copies. A
// file older than two minutes gives no condition.
func agentsViewOverdue(home string, now time.Time) (SystemCondition, bool) {
	path := deployment.AgentsViewPathsFor(home).Health
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxAgentsViewHealthBytes {
		return SystemCondition{}, false
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return SystemCondition{}, false
	}
	var health agentsViewHealth
	if json.Unmarshal(body, &health) != nil {
		return SystemCondition{}, false
	}
	observed, err := time.Parse(time.RFC3339Nano, health.ObservedAt)
	if err != nil || observed.Before(now.Add(-agentsViewHealthMaxAge)) || observed.After(now.Add(agentsViewHealthMaxSkew)) {
		return SystemCondition{}, false
	}
	seen := map[string]bool{}
	names := []string{}
	for _, item := range health.Overdue {
		if !plainSessionID(item.ID) || (item.Kind != "run" && item.Kind != "collie") || seen[item.Kind+":"+item.ID] {
			continue
		}
		seen[item.Kind+":"+item.ID] = true
		if item.Kind == "run" {
			names = append(names, "run "+item.ID)
		} else {
			names = append(names, "Collie session "+item.ID)
		}
	}
	if len(names) == 0 {
		return SystemCondition{}, false
	}
	count := fmt.Sprintf("%d sessions", len(names))
	if len(names) == 1 {
		count = "1 session"
	}
	listed := strings.Join(names[:min(len(names), maxOverdueNames)], ", ")
	if len(names) > maxOverdueNames {
		listed += fmt.Sprintf(", and %d more", len(names)-maxOverdueNames)
	}
	return SystemCondition{
		ID: "agentsview:overdue", Group: "execution", State: "failed", Name: "AgentsView",
		Condition: fmt.Sprintf("AgentsView did not import new trace lines of %s within 5 minutes: %s. "+
			"Their trace links can show an incomplete trace. Culler continues to copy the traces.", count, listed),
		Action: "View details",
	}, true
}

func plainSessionID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' && r != '.' {
			return false
		}
	}
	return true
}
