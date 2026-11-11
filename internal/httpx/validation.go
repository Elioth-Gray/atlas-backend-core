package httpx

import (
	"errors"
	"reflect"
	"strings"
	"sync"

	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"

	"atlas-backend-core/internal/validation"
)

var (
	registerOnce sync.Once
	nameTags     = [...]string{"json", "form", "uri"}
)

// RegisterValidator configures gin's validator to report field names as they appear on
// the wire (json, form, or uri tag) instead of Go struct field names. Safe to call more
// than once.
func RegisterValidator() {
	registerOnce.Do(func() {
		if v, ok := binding.Validator.Engine().(*validator.Validate); ok {
			v.RegisterTagNameFunc(wireName)
		}
	})
}

func wireName(f reflect.StructField) string {
	for _, tag := range nameTags {
		name, _, _ := strings.Cut(f.Tag.Get(tag), ",")
		if name != "" && name != "-" {
			return name
		}
	}
	return f.Name
}

// ValidationDetails converts validator errors into field-level API error details.
func ValidationDetails(errs validator.ValidationErrors) []FieldError {
	details := make([]FieldError, 0, len(errs))
	for _, fe := range errs {
		details = append(details, FieldError{Field: fieldPath(fe), Message: validation.Message(fe)})
	}
	return details
}

// AsValidationErrors reports whether err contains validator errors and returns them.
func AsValidationErrors(err error) (validator.ValidationErrors, bool) {
	var ve validator.ValidationErrors
	ok := errors.As(err, &ve)
	return ve, ok
}

// fieldPath strips the root struct name from the namespace so nested fields read as
// "address.city" rather than "createUserRequest.address.city".
func fieldPath(fe validator.FieldError) string {
	ns := fe.Namespace()
	if _, rest, ok := strings.Cut(ns, "."); ok {
		return rest
	}
	return fe.Field()
}
