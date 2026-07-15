// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package poc_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	poc "code.gitea.io/gitea/contrib/knowledge-ragflow-poc"
)

func TestPublishRejectsInvalidArtifactsBeforeTransport(t *testing.T) {
	tooLarge := make([]byte, poc.MaxArtifactBytes+1)
	tooLargeHash := sha256.Sum256(tooLarge)

	tests := []struct {
		name   string
		mutate func(*poc.MaskedArtifact)
	}{
		{name: "unmasked", mutate: func(a *poc.MaskedArtifact) { a.Masked = false }},
		{name: "empty data", mutate: func(a *poc.MaskedArtifact) { a.Bytes = nil }},
		{name: "larger than 32 MiB", mutate: func(a *poc.MaskedArtifact) {
			a.Bytes = tooLarge
			a.SHA256 = hex.EncodeToString(tooLargeHash[:])
		}},
		{name: "empty file name", mutate: func(a *poc.MaskedArtifact) { a.FileName = "" }},
		{name: "dot file name", mutate: func(a *poc.MaskedArtifact) { a.FileName = "." }},
		{name: "dot-dot file name", mutate: func(a *poc.MaskedArtifact) { a.FileName = ".." }},
		{name: "POSIX traversal file name", mutate: func(a *poc.MaskedArtifact) { a.FileName = "../secret.txt" }},
		{name: "Windows traversal file name", mutate: func(a *poc.MaskedArtifact) { a.FileName = `..\secret.txt` }},
		{name: "nested POSIX file name", mutate: func(a *poc.MaskedArtifact) { a.FileName = "folder/file.txt" }},
		{name: "nested Windows file name", mutate: func(a *poc.MaskedArtifact) { a.FileName = `folder\file.txt` }},
		{name: "empty source ID", mutate: func(a *poc.MaskedArtifact) { a.SourceID = "" }},
		{name: "dot source ID", mutate: func(a *poc.MaskedArtifact) { a.SourceID = "." }},
		{name: "dot-dot source ID", mutate: func(a *poc.MaskedArtifact) { a.SourceID = ".." }},
		{name: "Windows absolute source", mutate: func(a *poc.MaskedArtifact) { a.SourceID = `C:\Users\alice\secret.txt` }},
		{name: "Windows drive-qualified source", mutate: func(a *poc.MaskedArtifact) { a.SourceID = `C:secret.txt` }},
		{name: "POSIX absolute source", mutate: func(a *poc.MaskedArtifact) { a.SourceID = "/home/alice/secret.txt" }},
		{name: "UNC source", mutate: func(a *poc.MaskedArtifact) { a.SourceID = `\\server\share\secret.txt` }},
		{name: "source with forward slash", mutate: func(a *poc.MaskedArtifact) { a.SourceID = "opaque/child" }},
		{name: "source with backslash", mutate: func(a *poc.MaskedArtifact) { a.SourceID = `opaque\child` }},
		{name: "short hash", mutate: func(a *poc.MaskedArtifact) { a.SHA256 = "abcd" }},
		{name: "uppercase hash", mutate: func(a *poc.MaskedArtifact) { a.SHA256 = strings.ToUpper(a.SHA256) }},
		{name: "non-hex hash", mutate: func(a *poc.MaskedArtifact) { a.SHA256 = strings.Repeat("z", 64) }},
		{name: "mismatched hash", mutate: func(a *poc.MaskedArtifact) { a.SHA256 = strings.Repeat("0", 64) }},
		{name: "empty mask policy", mutate: func(a *poc.MaskedArtifact) { a.MaskPolicyVersion = "" }},
		{name: "empty publication ID", mutate: func(a *poc.MaskedArtifact) { a.PublicationID = "" }},
		{name: "zero generation", mutate: func(a *poc.MaskedArtifact) { a.PublicationGeneration = 0 }},
		{name: "negative generation", mutate: func(a *poc.MaskedArtifact) { a.PublicationGeneration = -1 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls.Add(1)
				return nil, errors.New("transport must not be called")
			})}
			client := newClient(t, "http://127.0.0.1", true, httpClient)
			artifact := validArtifact()
			tt.mutate(&artifact)

			_, err := client.Publish(context.Background(), artifact)
			if !errors.Is(err, poc.ErrInvalidArtifact) {
				t.Fatalf("Publish() error = %v, want errors.Is(ErrInvalidArtifact)", err)
			}
			if got := calls.Load(); got != 0 {
				t.Fatalf("transport calls = %d, want 0", got)
			}
		})
	}
}

func TestNetworkDisabledStopsEveryOperationBeforeTransport(t *testing.T) {
	var calls atomic.Int32
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("transport must not be called")
	})}
	client := newClient(t, "http://127.0.0.1", false, httpClient)

	_, publishErr := client.Publish(context.Background(), validArtifact())
	if !errors.Is(publishErr, poc.ErrNetworkDisabled) {
		t.Errorf("Publish() error = %v, want ErrNetworkDisabled", publishErr)
	}

	_, retrieveErr := client.Retrieve(context.Background(), poc.RetrieveRequest{
		Question:              "What is masked?",
		PublicationID:         "publication-abc",
		PublicationGeneration: 7,
		PageSize:              10,
	})
	if !errors.Is(retrieveErr, poc.ErrNetworkDisabled) {
		t.Errorf("Retrieve() error = %v, want ErrNetworkDisabled", retrieveErr)
	}

	deleteErr := client.Delete(context.Background(), []string{testDocument})
	if !errors.Is(deleteErr, poc.ErrNetworkDisabled) {
		t.Errorf("Delete() error = %v, want ErrNetworkDisabled", deleteErr)
	}

	if got := calls.Load(); got != 0 {
		t.Fatalf("transport calls = %d, want 0", got)
	}
}
