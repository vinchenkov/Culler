package main

import (
	"fmt"
	"io"

	"mc/verbs"
)

// extendSkillStoreOnHost grows the operator skill store for a Project that
// `mc project add` just registered. The store lives under the host's MC_HOME, which the helper
// container and the agent containers never see, so the host half of the
// verb owns this step: after the delegated add returns success on darwin, and
// after the in-process add on a host that opens the spine directly.
//
// Never a failure of the add. The row exists either way, and a non-zero exit
// here would read as "the Project was not registered" to a caller that
// would then retry the insert; the miss is reported on stderr and the
// operator creates the directories by hand, which the store permits.
func extendSkillStoreOnHost(project string, stderr io.Writer) {
	home, err := configuredCanonicalHome()
	if err == nil {
		_, err = verbs.ExtendSkillStore(home, project)
	}
	if err != nil {
		fmt.Fprintf(stderr, "mc: project %s registered, but its operator skill store directories were not created: %v\n", project, err)
	}
}
