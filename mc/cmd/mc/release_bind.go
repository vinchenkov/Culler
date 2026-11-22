package main

import "mc/verbs"

// Bind the agent-scope run envelope to this binary's release build id (the G-B
// identity bind). Untagged so it runs in the production build and in the
// test_fake_routing build the end-to-end containers use; in both, main's
// releaseBuildID (ldflags-overridden) is the value a matched-pair envelope
// must carry. This mirrors how AgentSkillReleaseID reaches the deployment
// package, and keeps the bind at the single identity-load throat rather than
// re-checking at every verb.
func init() { verbs.SetSelfReleaseBuildID(releaseBuildID) }
