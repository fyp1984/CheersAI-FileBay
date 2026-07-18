// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package knowledge

import (
	"regexp"
	"strings"
	"unicode"
)

const (
	maxGovernanceTextBytes = 1024
	maxTraceIDBytes        = 128
)

var safeTraceIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func validTraceID(value string) bool {
	return len(value) > 0 && len(value) <= maxTraceIDBytes &&
		value == strings.TrimSpace(value) &&
		safeTraceIDPattern.MatchString(value)
}

func validGovernanceText(value string, allowEmpty bool) bool {
	if value != strings.TrimSpace(value) {
		return false
	}
	if value == "" {
		return allowEmpty
	}
	if len(value) > maxGovernanceTextBytes || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return false
	}
	if strings.ContainsAny(value, "\\/:") {
		return false
	}
	lower := strings.ToLower(value)
	for _, marker := range []string{"api_key", "apikey", "secret", "password", "token=", "private key"} {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return true
}
