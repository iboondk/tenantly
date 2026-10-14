//go:build tools

// Package tools pins build-time tool dependencies so they stay in go.mod.
package tools

import (
	_ "github.com/99designs/gqlgen"
)
