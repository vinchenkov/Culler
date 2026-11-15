package deployment

import (
	"context"
	"fmt"
	"path/filepath"
)

// ImportSelectedStagedRuntimeAuth saves one native session before its model test.
func ImportSelectedStagedRuntimeAuth(home string, stage *RuntimeAuthSourceStage, verifier RuntimeAuthVerifier) (string, error) {
	return importStagedRuntimeAuth(home, stage, verifier, importSelectedRuntimeAuth)
}

func importSelectedRuntimeAuth(home string, catalog RuntimeCatalog, bindings []string, verifier RuntimeAuthVerifier, build func(string) (any, error)) (string, error) {
	ctx := context.Background()
	if scoped, ok := verifier.(interface{ VerificationContext() context.Context }); ok && scoped.VerificationContext() != nil {
		ctx = scoped.VerificationContext()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	credentials, err := catalog.credentialsOf(bindings)
	if err != nil {
		return "", err
	}
	if len(credentials) != 1 {
		return "", fmt.Errorf("Select one connection for each access check.")
	}
	id := credentials[0]
	release, err := AcquireConnectionOwner(ctx, home, id)
	if err != nil {
		return "", err
	}
	var identity string
	err = withAgentConnectionLockContext(ctx, home, id, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		grant, err := build(id)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := publishAgentConnectionAuthorization(home, id, grant); err != nil {
			return err
		}
		identity, err = agentConnectionIdentityLocked(home, id)
		return err
	})
	release()
	if err != nil {
		return "", err
	}
	for _, binding := range bindings {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if err := verifier.Verify(binding, filepath.Join(home, "refresh-grants")); err != nil {
			return "", err
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	current, err := AgentConnectionAuthorizationIdentity(home, id)
	if err != nil {
		return "", err
	}
	if current != identity {
		return "", fmt.Errorf("The connection changed during its model test. Test the current connection again.")
	}
	return "done", nil
}
