// Package model holds the GraphQL scalar marshallers and the generated model
// types (models_gen.go).
package model

import (
	"fmt"
	"io"

	"github.com/99designs/gqlgen/graphql"
	"github.com/google/uuid"
)

// MarshalUUID writes u as a quoted canonical string, such as
// "5b8f7c1e-...". gqlgen calls it for every UUID output.
func MarshalUUID(u uuid.UUID) graphql.Marshaler {
	return graphql.WriterFunc(func(w io.Writer) {
		_, _ = io.WriteString(w, `"`+u.String()+`"`)
	})
}

// UnmarshalUUID parses a UUID from a GraphQL string input. Anything else, or a
// malformed value, is rejected.
func UnmarshalUUID(v any) (uuid.UUID, error) {
	s, ok := v.(string)
	if !ok {
		return uuid.Nil, fmt.Errorf("UUID must be a string")
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("UUID is not valid: %w", err)
	}
	return id, nil
}
