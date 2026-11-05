package deployment

import (
	"context"
)

// AcquireConnectionOwner coordinates provider requests and credential replacement.
// Process exit releases the operating system lock.
func AcquireConnectionOwner(ctx context.Context, home, id string) (func(), error) {
	if _, err := agentProfileID(id, ""); err != nil {
		return nil, err
	}
	root, err := agentProfileOperationsRoot(home)
	if err != nil {
		return nil, err
	}
	lock, err := agentProfileOperationLockContext(ctx, root, "owner-"+id)
	if err != nil {
		return nil, err
	}
	return func() { agentProfileUnlock(lock) }, nil
}
