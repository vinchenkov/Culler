package verbs

import (
	"context"
	"database/sql"
	"mc/trace"
)

// RegisterSetupSession records only trace locators. A completed operation retains this capability.
// The host uses the same immutable registration after it recovers a native trace.
func RegisterSetupSession(db *sql.DB, id *RunIdentity, operationID, nativeRef, file string) (any, error) {
	if id != nil && (id.Tier != "setup" || id.Role != "setup" || id.RunID == "" || id.RunID != operationID) {
		return nil, Domainf("Trace registration requires the assigned setup operation identity.")
	}
	err := inTx(db, func(ctx context.Context, q Q) error {
		op, err := environmentOperationTx(ctx, q, operationID)
		if err != nil {
			return err
		}
		if op.Kind != "setup" && op.Kind != "validate" {
			return Domainf("This environment operation has no agent trace.")
		}
		binding := op.RuntimeBinding.Harness + "/" + op.RuntimeBinding.ID
		if trace.SessionID(binding, nativeRef) == "" || !trace.ValidFilename(binding, file) {
			return Usagef("The setup trace identity or relative filename is invalid.")
		}
		if op.NativeSessionRef != "" || op.TraceFilename != "" {
			if op.NativeSessionRef == nativeRef && op.TraceFilename == trace.Filename(binding, file) {
				return nil
			}
			return Domainf("The setup trace identity is immutable after registration.")
		}
		_, err = q.ExecContext(ctx, `UPDATE environment_operations SET native_session_ref=?,trace_filename=? WHERE id=?`, nativeRef, file, operationID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"operation_id": operationID, "native_session_ref": nativeRef, "trace_filename": file}, nil
}
