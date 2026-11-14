package verbs

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	operatorconfig "mc/config"
	"mc/deployment"
)

// ViewerDoctorProbes holds the host effects of the AgentsView and wiki checks.
type ViewerDoctorProbes struct {
	// InspectAgentsView returns the AgentsView label when launchd loaded the
	// unit from its prepared definition.
	InspectAgentsView func(home string) (string, error)
	// PingAgentsView sends the AgentsView token of the deployment at home.
	PingAgentsView func(home, bind string) error
	AnswerWiki     func(bind string) error
}

// ViewerDoctorFindings computes the agentsview and wiki host checks, in that
// order. Each failure names its repair.
func ViewerDoctorFindings(home string, homeErr error, probes ViewerDoctorProbes) []DoctorFinding {
	agentsView := DoctorFinding{Check: "agentsview", Status: "fail"}
	wiki := DoctorFinding{Check: "wiki", Status: "fail"}
	notChecked := func(detail string) []DoctorFinding {
		agentsView.Detail, wiki.Detail = "not checked: "+detail, "not checked: "+detail
		return []DoctorFinding{agentsView, wiki}
	}
	if homeErr != nil {
		return notChecked("MC_HOME unresolved: " + homeErr.Error())
	}
	cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
	if err != nil {
		return notChecked("config.toml invalid: " + err.Error())
	}
	agentsViewBind := cfg.Surfaces.AgentsView.Bind
	label, err := probes.InspectAgentsView(home)
	if err == nil {
		err = probes.PingAgentsView(home, agentsViewBind)
	}
	if err != nil {
		agentsView.Detail = strings.TrimSuffix(err.Error(), ".") + viewerRepairDetail(err, "surfaces.agentsview.bind")
	} else {
		agentsView.Status = "ok"
		agentsView.Detail = fmt.Sprintf("%s loaded from its prepared definition; AgentsView answered at %s",
			label, deployment.ViewerURL(agentsViewBind))
	}
	wikiBind := cfg.Surfaces.Wiki.Bind
	if err := probes.AnswerWiki(wikiBind); err != nil {
		wiki.Detail = err.Error() + viewerRepairDetail(err, "surfaces.wiki.bind")
	} else {
		wiki.Status = "ok"
		wiki.Detail = fmt.Sprintf("the dashboard answered at the wiki address %s/", deployment.ViewerURL(wikiBind))
	}
	return []DoctorFinding{agentsView, wiki}
}

// viewerRepairDetail returns the repair pointer of a viewer check failure. A
// rerun of install.sh keeps the port of key and a present token file. An HTTP
// status answer can come from another service or from AgentsView.
func viewerRepairDetail(err error, key string) string {
	switch {
	case errors.Is(err, deployment.ErrAnotherViewerService):
		return "; repair: stop that service, or set a free port in " + key + " of config.toml, then run install.sh"
	case errors.Is(err, deployment.ErrViewerHTTPStatus):
		return "; repair: if another service uses this port, stop that service or set a free port in " + key + " of config.toml, then run install.sh"
	case errors.Is(err, deployment.ErrUnusableViewerSecret):
		return "; repair: remove the AgentsView token file, then run install.sh"
	}
	return frontDoorRepairDetail
}
