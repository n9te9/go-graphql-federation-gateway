package gateway

import (
	"encoding/json"
	"net/http"
)

// GraphQLError is a spec-compliant ([October 2021 §7.1.2]) GraphQL error object.
//
// [October 2021 §7.1.2]: https://spec.graphql.org/October2021/#sec-Errors
type GraphQLError struct {
	Message    string         `json:"message"`
	Locations  []Location     `json:"locations,omitempty"`
	Path       []any          `json:"path,omitempty"`
	Extensions map[string]any `json:"extensions,omitempty"`
}

// Location identifies a position in the GraphQL document associated with an error.
type Location struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

// Error code constants used in `extensions.code`.
// Names follow Apollo conventions where applicable.
const (
	ErrCodeBadRequest              = "BAD_REQUEST"
	ErrCodeGraphQLParseFailed      = "GRAPHQL_PARSE_FAILED"
	ErrCodeGraphQLValidationFailed = "GRAPHQL_VALIDATION_FAILED"
	ErrCodeInaccessibleField       = "INACCESSIBLE_FIELD"
	ErrCodePlanningFailed          = "PLANNING_FAILED"
	ErrCodeInternalServerError     = "INTERNAL_SERVER_ERROR"
	ErrCodeIntrospectionDisabled   = "INTROSPECTION_DISABLED"
	ErrCodeMethodNotAllowed        = "METHOD_NOT_ALLOWED"
)

// newGraphQLError constructs a GraphQLError with a code in extensions.
func newGraphQLError(message, code string) GraphQLError {
	return GraphQLError{
		Message:    message,
		Extensions: map[string]any{"code": code},
	}
}

// writeGraphQLErrors serialises errs as `{ "errors": [...] }` and writes the
// response with the given HTTP status. status == 0 leaves the default 200.
func writeGraphQLErrors(w http.ResponseWriter, status int, errs ...GraphQLError) {
	w.Header().Set("Content-Type", "application/json")
	if status != 0 {
		w.WriteHeader(status)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"errors": errs})
}

// writeGraphQLError is a convenience wrapper for a single error.
func writeGraphQLError(w http.ResponseWriter, status int, message, code string) {
	writeGraphQLErrors(w, status, newGraphQLError(message, code))
}
