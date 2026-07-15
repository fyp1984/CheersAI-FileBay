// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package poc_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"sync"
	"testing"

	poc "code.gitea.io/gitea/contrib/knowledge-ragflow-poc"
)

const (
	testAPIKey    = "poc-test-token-never-log"
	testDatasetID = "dataset-contract"
	testDocument  = "document-created-by-poc"
)

func validArtifact() poc.MaskedArtifact {
	data := []byte("MASKED_FIXTURE_7f13d9")
	sum := sha256.Sum256(data)
	return poc.MaskedArtifact{
		Bytes:                 data,
		FileName:              "masked-notes.txt",
		SourceID:              "source-opaque-42",
		SHA256:                hex.EncodeToString(sum[:]),
		MaskPolicyVersion:     "mask-policy-v3",
		PublicationID:         "publication-abc",
		PublicationGeneration: 7,
		Masked:                true,
	}
}

func newClient(t *testing.T, baseURL string, networkEnabled bool, httpClient *http.Client) *poc.Client {
	t.Helper()
	client, err := poc.NewClient(poc.Config{
		BaseURL:        baseURL,
		APIKey:         testAPIKey,
		DatasetID:      testDatasetID,
		NetworkEnabled: networkEnabled,
		HTTPClient:     httpClient,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type capturedRequest struct {
	Method        string
	Path          string
	Authorization string
	ContentType   string
	Body          []byte
}

type requestLog struct {
	mu       sync.Mutex
	requests []capturedRequest
}

func (l *requestLog) capture(t *testing.T, req *http.Request) capturedRequest {
	t.Helper()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	got := capturedRequest{
		Method:        req.Method,
		Path:          req.URL.Path,
		Authorization: req.Header.Get("Authorization"),
		ContentType:   req.Header.Get("Content-Type"),
		Body:          body,
	}
	l.mu.Lock()
	l.requests = append(l.requests, got)
	l.mu.Unlock()
	return got
}

func (l *requestLog) snapshot() []capturedRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]capturedRequest(nil), l.requests...)
}

func publish(t *testing.T, client *poc.Client) (poc.PublishResult, error) {
	t.Helper()
	return client.Publish(context.Background(), validArtifact())
}
