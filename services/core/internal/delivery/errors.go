package delivery

// Exit codes. The 0-3 codes are the host management codes and keep
// their meaning; the remaining codes classify failures freezes for the
// installer, the updater and the media runtime manager.
const (
	ExitOK                  = 0  // command completed; optional warnings stay in the report
	ExitInternal            = 1  // execution failed without a more specific class
	ExitInvalidArguments    = 2  // unknown command, bad flag, missing confirmation or mutually exclusive options
	ExitConfigOrHealth      = 3  // configuration validation failed or Core health check failed
	ExitPermissionDenied    = 4  // the action needs an administrator or root
	ExitUnsupportedPlatform = 5  // the host or the requested platform/architecture is not supported
	ExitRuntimeUnavailable  = 6  // no usable media runtime: missing, incompatible or wrong architecture
	ExitNetworkFailure      = 7  // DNS, TLS, proxy, HTTP status or offline source failure
	ExitVerificationFailed  = 8  // signature, SHA-256, size or archive layout verification failed
	ExitInsufficientStorage = 9  // not enough disk space, or an archive exceeded an expansion limit
	ExitServiceFailure      = 10 // service install, start, stop or restart failed
	// ExitRestorePrecheckFailed classifies a restore whose path conditions
	// could not be proven. The input is readable, but the restore must not
	// proceed and the caller has to keep the current service and data running.
	ExitRestorePrecheckFailed = 11
	ExitCancelled             = 130 // the administrator cancelled the operation
)

// Stable error identifiers. They appear in the JSON error envelope and are the
// only machine-readable failure signal consumers may branch on; human messages
// are free to change.
const (
	CodeConfigOrHealth        = "config_or_health"
	CodeInvalidArguments      = "invalid_arguments"
	CodeUnsupportedPlatform   = "unsupported_platform"
	CodePermissionDenied      = "permission_denied"
	CodeRuntimeUnavailable    = "runtime_unavailable"
	CodeNetworkFailure        = "network_failure"
	CodeVerificationFailed    = "verification_failed"
	CodeInsufficientStorage   = "insufficient_storage"
	CodeServiceFailure        = "service_failure"
	CodeRestorePrecheckFailed = "restore_precheck_failed"
	CodeCancelled             = "cancelled"
	CodeNotImplemented        = "not_implemented"
	CodeInternalError         = "internal_error"
)

// Error is a classified failure with a stable identifier and an exit code.
type Error struct {
	Code     string
	Message  string
	ExitCode int
}

func (deliveryError *Error) Error() string { return deliveryError.Message }

// NewError builds a classified failure using the exit code registered for code.
func NewError(code, message string) *Error {
	return &Error{Code: code, Message: message, ExitCode: ExitCodeFor(code)}
}

// ExitCodeFor maps a stable error identifier onto its exit code.
func ExitCodeFor(code string) int {
	switch code {
	case CodeConfigOrHealth:
		return ExitConfigOrHealth
	case CodeInvalidArguments:
		return ExitInvalidArguments
	case CodeUnsupportedPlatform:
		return ExitUnsupportedPlatform
	case CodePermissionDenied:
		return ExitPermissionDenied
	case CodeRuntimeUnavailable:
		return ExitRuntimeUnavailable
	case CodeNetworkFailure:
		return ExitNetworkFailure
	case CodeVerificationFailed:
		return ExitVerificationFailed
	case CodeInsufficientStorage:
		return ExitInsufficientStorage
	case CodeServiceFailure:
		return ExitServiceFailure
	case CodeRestorePrecheckFailed:
		return ExitRestorePrecheckFailed
	case CodeCancelled:
		return ExitCancelled
	default:
		return ExitInternal
	}
}

// ErrorSchemaVersion is the schema version of the JSON error envelope.
const ErrorSchemaVersion = 1

// ErrorEnvelope is the single JSON object a failing host command writes to
// stdout when it was started with --json. Commands that succeed keep their own
// payload shape; only failures use this envelope.
type ErrorEnvelope struct {
	Report        any        `json:"report,omitempty"`
	SchemaVersion int        `json:"schemaVersion"`
	OK            bool       `json:"ok"`
	Command       string     `json:"command,omitempty"`
	Error         *ErrorBody `json:"error,omitempty"`
}

// ErrorBody carries the stable identifier and a human readable message. The
// message must never contain secrets, tokens or absolute host paths that a
// remote surface is not allowed to see.
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// NewErrorEnvelope builds the failure envelope for a command.
func NewErrorEnvelope(command string, deliveryError *Error) ErrorEnvelope {
	return ErrorEnvelope{
		SchemaVersion: ErrorSchemaVersion,
		OK:            false,
		Command:       command,
		Error:         &ErrorBody{Code: deliveryError.Code, Message: deliveryError.Message},
	}
}
