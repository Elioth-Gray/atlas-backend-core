package handler

import (
	"github.com/gin-gonic/gin"
)

// bindError marks a failure to decode or validate client input so handleError reports it
// as a 400 instead of a 500.
type bindError struct{ err error }

func (e *bindError) Error() string { return "bind request: " + e.err.Error() }
func (e *bindError) Unwrap() error { return e.err }

// bindJSON decodes and validates a JSON request body into dst.
func bindJSON(c *gin.Context, dst any) error {
	if err := c.ShouldBindJSON(dst); err != nil {
		return &bindError{err: err}
	}
	return nil
}

// bindQuery decodes and validates query parameters into dst.
func bindQuery(c *gin.Context, dst any) error {
	if err := c.ShouldBindQuery(dst); err != nil {
		return &bindError{err: err}
	}
	return nil
}
