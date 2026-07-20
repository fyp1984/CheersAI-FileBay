// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package poc

import (
	"net/http"
	"time"
)

const (
	// MaxArtifactBytes is the largest masked artifact accepted by the PoC.
	MaxArtifactBytes = 32 << 20
	// MaxResponseBytes is the largest RAGFlow response accepted by the PoC.
	MaxResponseBytes = 1 << 20
	// DefaultHTTPTimeout bounds every HTTP client created or supplied to the PoC.
	DefaultHTTPTimeout = 10 * time.Second
)

// Config configures the isolated RAGFlow PoC client.
type Config struct {
	BaseURL        string
	APIKey         string
	DatasetID      string
	NetworkEnabled bool
	HTTPClient     *http.Client
}

// MaskedArtifact is a client-masked file and the non-path manifest required to publish it.
type MaskedArtifact struct {
	Bytes                 []byte
	FileName              string
	SourceID              string
	SHA256                string
	MaskPolicyVersion     string
	PublicationID         string
	PublicationGeneration int
	Masked                bool
}

// PublishResult identifies the RAGFlow document created by Publish.
type PublishResult struct {
	DocumentID string
}

// RetrieveRequest scopes a retrieval to one FileBay publication generation.
type RetrieveRequest struct {
	Question              string
	PublicationID         string
	PublicationGeneration int
	PageSize              int
}

// RetrievedChunk is untrusted content returned by RAGFlow.
type RetrievedChunk struct {
	ID         string  `json:"id"`
	DocumentID string  `json:"document_id"`
	Content    string  `json:"content"`
	Similarity float64 `json:"similarity"`
}

// RetrieveResult contains the untrusted chunks returned by RAGFlow.
type RetrieveResult struct {
	Chunks []RetrievedChunk
}
