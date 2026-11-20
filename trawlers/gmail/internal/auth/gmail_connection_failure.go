package auth

import (
	"errors"
	"fmt"
)

// GmailConnectionFailureKind names a connection failure the reader can act
// on. Every other failure is GmailConnectionStepFailed, with the step named
// for the run log.
type GmailConnectionFailureKind string

const (
	GmailIsNotConnected                           GmailConnectionFailureKind = "not_connected"
	GmailIsAlreadyConnected                       GmailConnectionFailureKind = "already_connected"
	GoogleSignInWasCancelled                      GmailConnectionFailureKind = "sign_in_cancelled"
	GoogleSignInTimedOut                          GmailConnectionFailureKind = "sign_in_timed_out"
	GoogleSignInDidNotGrantGmailReadAccess        GmailConnectionFailureKind = "gmail_read_access_not_granted"
	GoogleNoLongerAcceptsGmailGrant               GmailConnectionFailureKind = "google_rejected_grant"
	OpenTrawlBuildHasNoGoogleOAuthClient          GmailConnectionFailureKind = "build_has_no_google_oauth_client"
	OpenTrawlBuildCannotUseDataProtectionKeychain GmailConnectionFailureKind = "keychain_needs_signed_app"
	GmailConnectionStepFailed                     GmailConnectionFailureKind = "connection_step_failed"
)

type GmailConnectionFailure struct {
	GmailConnectionFailureKind GmailConnectionFailureKind
	FailedGmailConnectionStep  string
	Cause                      error
}

// Error names the kind and step only. Causes stay out of the sentence but
// are kept for the run log; none of them carries a token or secret.
func (failure *GmailConnectionFailure) Error() string {
	return fmt.Sprintf("gmail connection failed: %s during %s", failure.GmailConnectionFailureKind, failure.FailedGmailConnectionStep)
}

func (failure *GmailConnectionFailure) Unwrap() error {
	return failure.Cause
}

func gmailConnectionFailure(kind GmailConnectionFailureKind, failedStep string, cause error) error {
	if errors.Is(cause, ErrMacOSKeychainNeedsSignedApp) {
		kind = OpenTrawlBuildCannotUseDataProtectionKeychain
	}
	return &GmailConnectionFailure{GmailConnectionFailureKind: kind, FailedGmailConnectionStep: failedStep, Cause: cause}
}

func gmailConnectionStepFailure(failedStep string, cause error) error {
	return gmailConnectionFailure(GmailConnectionStepFailed, failedStep, cause)
}
