package deployment

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// AuthorizationFailure contains fixed diagnostics, never native command output.
type AuthorizationFailure struct {
	Code     string
	ExitCode int
}

func (failure *AuthorizationFailure) Error() string {
	messages := map[string]string{
		"credential_missing":     "The native sign-in completed without a credential file. Ask your coding harness to check the native credential store.",
		"credential_permissions": "The native credential file has unsafe permissions. Ask your coding harness to repair the isolated credential directory.",
		"credential_changed":     "The native credential source changed during sign-in. Start a new sign-in attempt.",
		"credential_invalid":     "The native sign-in returned an unsupported credential record. Ask your coding harness to check the installed harness version.",
		"credential_mode":        "The native sign-in returned another authentication method. Ask your coding harness to check the isolated sign-in configuration.",
		"credential_api_key":     "The native sign-in returned an API key. Select the API key connection method or start account sign-in again.",
		"credential_access":      "The native sign-in returned no access token. Start a new sign-in attempt.",
		"credential_id":          "The native sign-in returned no identity token. Ask your coding harness to check the installed harness version.",
		"credential_refresh":     "The native sign-in returned no refresh token. Start a new sign-in attempt.",
		"credential_account":     "The native sign-in returned no account identifier. Ask your coding harness to check the installed harness version.",
		"credential_cleanup":     "The host could not remove the temporary sign-in files. Ask your coding harness to repair the host credential directory.",
		"credential_source":      "The host could not read the native sign-in credential. Ask your coding harness to check the isolated credential directory.",

		"callback": "The sign-in callback port is unavailable. Close another sign-in attempt, then retry.",
		"network":  "The sign-in could not reach the provider. Check the host network, then retry.",
		"denied":   "The provider refused this sign-in. Review the account access, then retry.",
		"expired":  "The sign-in expired. Start a new sign-in attempt.",
		"storage":  "The native sign-in could not save its credential. Ask your coding harness to check host file access.",
	}
	if message := messages[failure.Code]; message != "" {
		return message
	}
	return fmt.Sprintf("The native sign-in exited with code %d. Start a new sign-in attempt.", failure.ExitCode)
}

type authorizationFailureOutput struct {
	mu     sync.Mutex
	target io.Writer
	tail   string
	code   string
}

func (output *authorizationFailureOutput) Write(body []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	text := strings.ToLower(output.tail + string(body))
	switch {
	case strings.Contains(text, "address already in use") || strings.Contains(text, "failed to bind"):
		output.code = "callback"
	case strings.Contains(text, "access_denied") || strings.Contains(text, "access denied") || strings.Contains(text, "403 forbidden"):
		output.code = "denied"
	case strings.Contains(text, "expired") || strings.Contains(text, "invalid_grant"):
		output.code = "expired"
	case strings.Contains(text, "permission denied") || strings.Contains(text, "read-only file system"):
		output.code = "storage"
	case strings.Contains(text, "error sending request") || strings.Contains(text, "connection refused") || strings.Contains(text, "dns error") || strings.Contains(text, "certificate verify failed") || strings.Contains(text, "connection timed out"):
		output.code = "network"
	}
	if len(text) > 128 {
		text = text[len(text)-128:]
	}
	output.tail = text
	if output.target == nil {
		return len(body), nil
	}
	return output.target.Write(body)
}

func (output *authorizationFailureOutput) failure(exitCode int) error {
	output.mu.Lock()
	defer output.mu.Unlock()
	output.tail = ""
	return &AuthorizationFailure{Code: output.code, ExitCode: exitCode}
}

func nativeCredentialFailure(err error) error {
	if err == nil {
		return nil
	}
	var native *AuthorizationFailure
	if errors.As(err, &native) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrHostCredentialUnavailable) {
		return err
	}
	code := "credential_source"
	var cleanup *RuntimeAuthCleanupError
	switch {
	case errors.As(err, &cleanup):
		code = "credential_cleanup"
	case errors.Is(err, os.ErrNotExist):
		code = "credential_missing"
	case errors.Is(err, os.ErrPermission) || strings.Contains(err.Error(), "mode-0600") || strings.Contains(err.Error(), "owner-only"):
		code = "credential_permissions"
	case strings.Contains(err.Error(), "changed") || strings.Contains(err.Error(), "replacement"):
		code = "credential_changed"
	}
	return &AuthorizationFailure{Code: code, ExitCode: -1}
}
