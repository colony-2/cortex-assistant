// Package assistantassets contains the production recipe/helper bytes shipped
// with this module. Test fixtures compare these before substituting inference.
package assistantassets

import "embed"

// Production includes all policy implementation, helper and dependency bytes.
// Tests are embedded by the glob but excluded from fixture preparation.
//
//go:embed assistant/*.go cmd/discussion-op/main.go cmd/bindings-op/main.go ops/discussion/op.yaml ops/bindings/op.yaml go.mod go.sum discussion.yaml discussion-goal.yaml inference.yaml
var Production embed.FS
