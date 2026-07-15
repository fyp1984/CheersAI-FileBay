// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package poc

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
)

func validateArtifact(artifact MaskedArtifact) ([]byte, error) {
	if !artifact.Masked {
		return nil, invalidArtifact("masked attestation is required")
	}
	if len(artifact.Bytes) == 0 || len(artifact.Bytes) > MaxArtifactBytes {
		return nil, invalidArtifact("masked byte length is outside the allowed range")
	}
	if !safeFileName(artifact.FileName) {
		return nil, invalidArtifact("file name must be a safe base name")
	}
	if !opaqueSourceID(artifact.SourceID) {
		return nil, invalidArtifact("source ID must be opaque and must not be a path")
	}
	if !validDigest(artifact.SHA256) {
		return nil, invalidArtifact("SHA-256 must be lowercase hexadecimal")
	}
	if strings.TrimSpace(artifact.MaskPolicyVersion) == "" {
		return nil, invalidArtifact("mask policy version is required")
	}
	if strings.TrimSpace(artifact.PublicationID) == "" {
		return nil, invalidArtifact("publication ID is required")
	}
	if artifact.PublicationGeneration < 1 {
		return nil, invalidArtifact("publication generation must be positive")
	}

	maskedBytes := append([]byte(nil), artifact.Bytes...)
	digest := sha256.Sum256(maskedBytes)
	if hex.EncodeToString(digest[:]) != artifact.SHA256 {
		return nil, invalidArtifact("SHA-256 does not match the masked bytes")
	}
	return maskedBytes, nil
}

func safeFileName(name string) bool {
	if name == "" || name == "." || name == ".." || strings.TrimSpace(name) != name {
		return false
	}
	if strings.ContainsAny(name, `/\\:`) {
		return false
	}
	return !hasControl(name)
}

func opaqueSourceID(sourceID string) bool {
	if sourceID == "" || sourceID == "." || sourceID == ".." || strings.TrimSpace(sourceID) != sourceID {
		return false
	}
	if strings.ContainsAny(sourceID, `/\\:`) {
		return false
	}
	return !hasControl(sourceID)
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func hasControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}

func invalidArtifact(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidArtifact, reason)
}

func validateRetrieveRequest(request RetrieveRequest) error {
	if strings.TrimSpace(request.Question) == "" {
		return fmt.Errorf("%w: question is required", ErrInvalidInput)
	}
	if strings.TrimSpace(request.PublicationID) == "" {
		return fmt.Errorf("%w: publication ID is required", ErrInvalidInput)
	}
	if request.PublicationGeneration < 1 {
		return fmt.Errorf("%w: publication generation must be positive", ErrInvalidInput)
	}
	return nil
}

func validateDocumentIDs(documentIDs []string) error {
	if len(documentIDs) == 0 {
		return fmt.Errorf("%w: at least one document ID is required", ErrInvalidInput)
	}
	for _, documentID := range documentIDs {
		if !safePathID(documentID) {
			return fmt.Errorf("%w: document IDs must contain only ASCII letters, digits, underscores, or hyphens", ErrInvalidInput)
		}
	}
	return nil
}
