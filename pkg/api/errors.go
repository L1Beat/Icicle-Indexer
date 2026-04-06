package api

import (
	"log/slog"
	"net/http"
)

// ErrorCode represents structured error codes for API responses
type ErrorCode string

const (
	ErrInvalidParameter ErrorCode = "INVALID_PARAMETER"
	ErrNotFound         ErrorCode = "NOT_FOUND"
	ErrInternalError    ErrorCode = "INTERNAL_ERROR"
	ErrRateLimited      ErrorCode = "RATE_LIMITED"
	ErrValidationFailed ErrorCode = "VALIDATION_FAILED"
	ErrDatabaseError    ErrorCode = "DATABASE_ERROR"
)

// FieldError represents a validation error for a specific field
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// APIError is the structured error response
type APIError struct {
	Code       ErrorCode    `json:"code"`
	Message    string       `json:"message"`
	Details    string       `json:"details,omitempty"`
	Fields     []FieldError `json:"fields,omitempty"`
	RetryAfter int          `json:"retry_after,omitempty"`
}

// ErrorResponse wraps the error in a consistent envelope
type ErrorResponse struct {
	Error APIError `json:"error"`
}

// writeAPIError writes a structured error response
func writeAPIError(w http.ResponseWriter, status int, code ErrorCode, message string) {
	writeJSON(w, status, ErrorResponse{
		Error: APIError{
			Code:    code,
			Message: message,
		},
	})
}

// writeNotFoundError writes a 404 error for a specific resource
func writeNotFoundError(w http.ResponseWriter, resource string) {
	writeJSON(w, http.StatusNotFound, ErrorResponse{
		Error: APIError{
			Code:    ErrNotFound,
			Message: resource + " not found",
		},
	})
}

// writeRateLimitError writes a 429 error with retry information
func writeRateLimitError(w http.ResponseWriter, retryAfter int) {
	writeJSON(w, http.StatusTooManyRequests, ErrorResponse{
		Error: APIError{
			Code:       ErrRateLimited,
			Message:    "Rate limit exceeded",
			RetryAfter: retryAfter,
		},
	})
}

// writeInternalError writes a 500 error
func writeInternalError(w http.ResponseWriter, details string) {
	slog.Error("internal error", "details", details)
	writeJSON(w, http.StatusInternalServerError, ErrorResponse{
		Error: APIError{
			Code:    ErrInternalError,
			Message: "Internal server error",
		},
	})
}
