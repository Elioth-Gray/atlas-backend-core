package domain

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// TextArray maps a PostgreSQL TEXT[] column. A nil slice is written as an empty array.
type TextArray []string

// Scan implements sql.Scanner.
func (a *TextArray) Scan(src any) error {
	var out []string
	if err := pgtype.NewMap().SQLScanner(&out).Scan(src); err != nil {
		return fmt.Errorf("scan text[]: %w", err)
	}
	if out == nil {
		out = []string{}
	}
	*a = out
	return nil
}

// Value implements driver.Valuer using the PostgreSQL array literal syntax.
func (a TextArray) Value() (driver.Value, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, s := range a {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		b.WriteString(strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String(), nil
}

// JSONB maps a JSONB column to a Go value of type T.
type JSONB[T any] struct {
	V T
}

// NewJSONB wraps v.
func NewJSONB[T any](v T) JSONB[T] { return JSONB[T]{V: v} }

// Scan implements sql.Scanner.
func (j *JSONB[T]) Scan(src any) error {
	var raw []byte
	switch v := src.(type) {
	case nil:
		var zero T
		j.V = zero
		return nil
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return fmt.Errorf("scan jsonb: unsupported source type %T", src)
	}
	if err := json.Unmarshal(raw, &j.V); err != nil {
		return fmt.Errorf("scan jsonb: %w", err)
	}
	return nil
}

// Value implements driver.Valuer.
func (j JSONB[T]) Value() (driver.Value, error) {
	raw, err := json.Marshal(j.V)
	if err != nil {
		return nil, fmt.Errorf("marshal jsonb: %w", err)
	}
	return string(raw), nil
}

// NullJSONB maps a nullable JSONB column. Valid is false for SQL NULL.
type NullJSONB[T any] struct {
	V     T
	Valid bool
}

// Scan implements sql.Scanner.
func (j *NullJSONB[T]) Scan(src any) error {
	if src == nil {
		var zero T
		j.V, j.Valid = zero, false
		return nil
	}
	var inner JSONB[T]
	if err := inner.Scan(src); err != nil {
		return err
	}
	j.V, j.Valid = inner.V, true
	return nil
}

// Value implements driver.Valuer.
func (j NullJSONB[T]) Value() (driver.Value, error) {
	if !j.Valid {
		return nil, nil
	}
	return JSONB[T]{V: j.V}.Value()
}
