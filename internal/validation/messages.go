// Package validation turns go-playground/validator failures into human-readable messages
// shared by every transport (REST field errors, gRPC BadRequest field violations).
package validation

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
)

// Message returns a client-facing description of a single validation failure, written to
// follow the field name (e.g. "email" + " must be a valid email address").
func Message(fe validator.FieldError) string {
	param := fe.Param()
	switch fe.Tag() {
	case "required":
		return "is required"
	case "email":
		return "must be a valid email address"
	case "uuid", "uuid4", "uuid_rfc4122":
		return "must be a valid UUID"
	case "url", "http_url":
		return "must be a valid URL"
	case "oneof":
		return "must be one of: " + strings.ReplaceAll(param, " ", ", ")
	case "min":
		return boundMessage(fe.Kind(), "at least", param)
	case "max":
		return boundMessage(fe.Kind(), "at most", param)
	case "len":
		return boundMessage(fe.Kind(), "exactly", param)
	case "gt":
		return "must be greater than " + param
	case "gte":
		return "must be greater than or equal to " + param
	case "lt":
		return "must be less than " + param
	case "lte":
		return "must be less than or equal to " + param
	default:
		return fmt.Sprintf("failed the %q rule", fe.Tag())
	}
}

func boundMessage(kind reflect.Kind, bound, param string) string {
	switch kind {
	case reflect.String:
		return fmt.Sprintf("must be %s %s characters long", bound, param)
	case reflect.Slice, reflect.Array, reflect.Map:
		return fmt.Sprintf("must contain %s %s items", bound, param)
	default:
		return fmt.Sprintf("must be %s %s", bound, param)
	}
}
