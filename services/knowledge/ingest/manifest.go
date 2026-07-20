// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Package ingest validates declarations for explicitly selected, client-masked
// sandbox artifacts. It never reads client paths or performs server-side masking.
package ingest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

const defaultMaxFileBytes int64 = 32 << 20

const (
	maskManifestSchemaVersion = 1
	maxMaskManifestBytes      = 16 << 10
	maxManifestCategories     = 64
	maxManifestFindings       = 1_000_000
	maxPolicyVersionBytes     = 128
	maxSourceAuthorization    = 256
)

var (
	ErrMaskingNotDeclared  = errors.New("masked sandbox artifact declaration required")
	ErrInvalidManifest     = errors.New("invalid masked artifact manifest")
	ErrInvalidFileName     = errors.New("invalid masked artifact file name")
	ErrUnsupportedFileType = errors.New("unsupported masked artifact file type")
	ErrIntegrityMismatch   = errors.New("masked artifact integrity mismatch")
	ErrFileSize            = errors.New("masked artifact size is outside policy")
	ErrLocalPath           = errors.New("local path metadata is forbidden")
)

var (
	boundedToken      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	maskPolicyVersion = regexp.MustCompile(`^mask-policy-[A-Za-z0-9][A-Za-z0-9._-]*$`)
	grantID           = regexp.MustCompile(`^grant-id:[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

var allowedMaskCategories = map[string]struct{}{
	"access_token":      {},
	"address":           {},
	"api_key":           {},
	"bank_account":      {},
	"credit_card":       {},
	"credential":        {},
	"email":             {},
	"email_address":     {},
	"id_number":         {},
	"national_id":       {},
	"organization_name": {},
	"passport_number":   {},
	"password":          {},
	"person_name":       {},
	"phone":             {},
	"phone_number":      {},
	"physical_address":  {},
	"tax_id":            {},
}

// UploadManifest is the client declaration for one masked sandbox artifact.
type UploadManifest struct {
	Masked              bool
	FileName            string
	ContentSHA256       string
	Size                int64
	MIMEType            string
	MaskPolicyVersion   string
	MaskManifestJSON    json.RawMessage
	SourceAuthorization string
}

// UploadFacts are facts calculated from the received masked artifact.
type UploadFacts struct {
	ContentSHA256    string
	Size             int64
	DetectedMIMEType string
}

// UploadPolicy defines the bounded server-side acceptance policy.
type UploadPolicy struct {
	MaxFileBytes int64
	AllowedTypes map[string]string
}

// DefaultUploadPolicy returns a fresh production MVP policy.
func DefaultUploadPolicy() UploadPolicy {
	return UploadPolicy{
		MaxFileBytes: defaultMaxFileBytes,
		AllowedTypes: map[string]string{
			".pdf":  "application/pdf",
			".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
			".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
			".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
			".md":   "text/markdown",
			".txt":  "text/plain",
		},
	}
}

// ValidateUploadManifest fails closed unless the declaration and received facts
// describe the same explicitly masked, policy-allowed artifact.
func ValidateUploadManifest(manifest UploadManifest, facts UploadFacts, policy UploadPolicy) error {
	if !manifest.Masked {
		return ErrMaskingNotDeclared
	}
	if !validMaskPolicyVersion(manifest.MaskPolicyVersion) || !validSourceAuthorization(manifest.SourceAuthorization) {
		return ErrInvalidManifest
	}
	if !isSafeBaseName(manifest.FileName) {
		return ErrInvalidFileName
	}
	if policy.MaxFileBytes <= 0 || manifest.Size <= 0 || manifest.Size > policy.MaxFileBytes || facts.Size != manifest.Size {
		return ErrFileSize
	}
	if !validSHA256(manifest.ContentSHA256) || !validSHA256(facts.ContentSHA256) ||
		!equalSHA256(manifest.ContentSHA256, facts.ContentSHA256) {
		return ErrIntegrityMismatch
	}

	extension := strings.ToLower(filepath.Ext(manifest.FileName))
	wantMIME, allowed := policy.AllowedTypes[extension]
	if !allowed || normalizeMIME(manifest.MIMEType) != wantMIME || normalizeMIME(facts.DetectedMIMEType) != wantMIME {
		return ErrUnsupportedFileType
	}
	if err := validateManifestJSON(manifest.MaskManifestJSON); err != nil {
		return err
	}
	return nil
}

func isSafeBaseName(name string) bool {
	if name == "" || len(name) > 255 || name != strings.TrimSpace(name) || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, "/\\:\x00") || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return false
	}
	return filepath.Base(name) == name
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func equalSHA256(left, right string) bool {
	leftBytes, leftErr := hex.DecodeString(left)
	rightBytes, rightErr := hex.DecodeString(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftBytes, rightBytes)
}

func normalizeMIME(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if index := strings.IndexByte(value, ';'); index >= 0 {
		value = strings.TrimSpace(value[:index])
	}
	return value
}

func validateManifestJSON(raw json.RawMessage) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > maxMaskManifestBytes {
		return ErrInvalidManifest
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || len(fields) != 4 {
		return ErrInvalidManifest
	}
	for _, required := range []string{"schema_version", "masked", "findings_count", "categories"} {
		if _, exists := fields[required]; !exists {
			return ErrInvalidManifest
		}
	}
	type manifestV1 struct {
		SchemaVersion int      `json:"schema_version"`
		Masked        bool     `json:"masked"`
		FindingsCount int64    `json:"findings_count"`
		Categories    []string `json:"categories"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value manifestV1
	if err := decoder.Decode(&value); err != nil {
		return ErrInvalidManifest
	}
	var trailing manifestV1
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrInvalidManifest
	}
	if value.SchemaVersion != maskManifestSchemaVersion || !value.Masked || value.FindingsCount < 0 ||
		value.FindingsCount > maxManifestFindings || len(value.Categories) > maxManifestCategories {
		return ErrInvalidManifest
	}
	if (value.FindingsCount == 0) != (len(value.Categories) == 0) {
		return ErrInvalidManifest
	}
	seen := make(map[string]struct{}, len(value.Categories))
	for _, category := range value.Categories {
		if len(category) == 0 || len(category) > 64 {
			return ErrInvalidManifest
		}
		if _, allowed := allowedMaskCategories[category]; !allowed {
			return ErrInvalidManifest
		}
		if _, duplicate := seen[category]; duplicate {
			return ErrInvalidManifest
		}
		seen[category] = struct{}{}
	}
	return nil
}

func validBoundedToken(value string, maxBytes int) bool {
	return len(value) > 0 && len(value) <= maxBytes && value == strings.TrimSpace(value) && boundedToken.MatchString(value)
}

func validMaskPolicyVersion(value string) bool {
	return len(value) > 0 && len(value) <= maxPolicyVersionBytes &&
		value == strings.TrimSpace(value) &&
		maskPolicyVersion.MatchString(value)
}

func validSourceAuthorization(value string) bool {
	if len(value) == 0 || len(value) > maxSourceAuthorization || value != strings.TrimSpace(value) {
		return false
	}
	return grantID.MatchString(value)
}
