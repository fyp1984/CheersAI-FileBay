// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package poc

import (
	"errors"
	"fmt"
)

var (
	// ErrNetworkDisabled is returned unless the caller explicitly enables PoC networking.
	ErrNetworkDisabled = errors.New("RAGFlow PoC network access is disabled")
	// ErrInvalidArtifact is returned when masked bytes or their manifest are invalid.
	ErrInvalidArtifact = errors.New("invalid masked artifact")
	// ErrInvalidInput is returned when a non-artifact operation has invalid input.
	ErrInvalidInput = errors.New("invalid RAGFlow PoC input")
)

// PublishError reports a post-upload failure and its one cleanup attempt.
type PublishError struct {
	Operation        string
	Err              error
	CleanupAttempted bool
	CleanupErr       error
}

func (e *PublishError) Error() string {
	if e == nil {
		return "publish failed"
	}
	if e.CleanupErr != nil {
		return fmt.Sprintf("publish %s failed; cleanup also failed", e.Operation)
	}
	if e.CleanupAttempted {
		return fmt.Sprintf("publish %s failed; cleanup attempted", e.Operation)
	}
	return fmt.Sprintf("publish %s failed", e.Operation)
}

// Unwrap preserves the sanitized original operation failure.
func (e *PublishError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}
