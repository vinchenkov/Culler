package verbs

import (
	"context"
	"database/sql"
	"regexp"
	"time"

	"mc/domain"
)

type ProviderAccessObservation = domain.ProviderAccessObservation

var providerRequestIDPattern = regexp.MustCompile(`^(?:[0-9a-f]{32}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)
var providerReferencePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,255}$`)

func ProviderAccessObserve(db *sql.DB, id *RunIdentity, a ProviderAccessObservation) (any, error) {
	if err := RequireHostScope(id, "provider access observation"); err != nil {
		return nil, err
	}
	if !providerRequestIDPattern.MatchString(a.RequestID) || a.DurationMS < 0 || a.DurationMS > int64((31*24*time.Hour)/time.Millisecond) {
		return nil, Usagef("The provider request identity or duration is invalid.")
	}
	for _, value := range []string{a.AuthorizationID, a.RunID, a.LaunchID, a.Connection, a.AuthorizationRevision, a.CredentialRef, a.ProfileID, a.ProfileRevision, a.AttemptID, a.Operation, a.CurrentAuthorizationRevision, a.CurrentCredentialRef, a.ProviderRequestID} {
		if value != "" && !providerReferencePattern.MatchString(value) {
			return nil, Usagef("The provider request metadata is invalid.")
		}
	}
	if a.Connection != "" && !connectionIDPattern.MatchString(a.Connection) {
		return nil, Usagef("The provider connection is invalid.")
	}
	if a.CurrentCredentialType != "" && a.CurrentCredentialType != "api_key" && a.CurrentCredentialType != "account" {
		return nil, Usagef("The current credential type is invalid.")
	}
	if a.ProfileID != "" && (!connectionIDPattern.MatchString(a.ProfileID) || a.ProfileRevision == "") {
		return nil, Usagef("The provider profile scope is invalid.")
	}
	switch a.Origin {
	case "proxy_authorization", "proxy_limit", "proxy", "credential_store", "provider_renewal", "provider_network", "provider", "client":
	default:
		return nil, Usagef("The provider request origin is invalid.")
	}
	switch a.Outcome {
	case "rejected", "response_complete", "interrupted", "ready", "failed":
	default:
		return nil, Usagef("The provider request outcome is invalid.")
	}
	if a.HTTPStatus != nil && (*a.HTTPStatus < 100 || *a.HTTPStatus > 599) || !recognizedProviderCode(a.ProviderErrorCode) {
		return nil, Usagef("The provider response metadata is invalid.")
	}
	if a.NetworkError != "" && !networkErrorClass(a.NetworkError) {
		return nil, Usagef("The provider network failure is invalid.")
	}
	if (a.Origin == "provider" || a.Origin == "provider_network") && !a.ProviderSent {
		return nil, Usagef("A provider observation requires an attempted provider request.")
	}
	if a.Origin == "provider" && (a.AuthorizationRevision == "" || a.CredentialRef == "" || a.Connection == "") {
		return nil, Usagef("The provider credential reference is required.")
	}
	if a.ObservedAt == "" {
		return nil, Usagef("The provider observation time is required.")
	}
	for _, value := range []string{a.ObservedAt, a.RetryAfter, a.ExpiresAt} {
		if value != "" {
			if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
				return nil, Usagef("The provider observation time is invalid.")
			}
		}
	}
	var result domain.ProviderAccessResult
	err := inTx(db, func(ctx context.Context, q Q) error {
		var err error
		result, err = domain.RecordProviderAccess(ctx, q, a)
		return err
	})
	return result, err
}

// ProviderAccessAdmission keeps host service failures separate from account authorization.
func ProviderAccessAdmission(ctx context.Context, q Q, connection string) error {
	conditions, err := domain.ProviderServiceConditions(ctx, q, connection)
	if err != nil {
		return err
	}
	if len(conditions) > 0 {
		return Domainf("%s Open System health in the dashboard.", conditions[0].Condition)
	}
	return nil
}
