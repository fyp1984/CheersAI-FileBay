// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	poc "code.gitea.io/gitea/contrib/knowledge-ragflow-poc"
)

func TestRunRejectsUnsetNetworkOptInBeforeReadingMaskedFile(t *testing.T) {
	unsetEnvironment(t, networkOptInEnvironment)

	documentID, err := run(context.Background(), validCLIArguments(filepath.Join(t.TempDir(), "does-not-exist.masked")))
	if documentID != "" {
		t.Errorf("run() document ID = %q, want empty", documentID)
	}
	if !errors.Is(err, poc.ErrNetworkDisabled) {
		t.Fatalf("run() error = %v, want errors.Is(ErrNetworkDisabled)", err)
	}
}

func TestRunRejectsEveryNonOneNetworkOptInBeforeReadingMaskedFile(t *testing.T) {
	for _, value := range []string{"", "0", "true", "01", " 1", "1 "} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(networkOptInEnvironment, value)
			missingFile := filepath.Join(t.TempDir(), "does-not-exist.masked")

			documentID, err := run(context.Background(), validCLIArguments(missingFile))
			if documentID != "" {
				t.Errorf("run() document ID = %q, want empty", documentID)
			}
			if !errors.Is(err, poc.ErrNetworkDisabled) {
				t.Fatalf("run() error = %v, want errors.Is(ErrNetworkDisabled)", err)
			}
		})
	}
}

func TestRunWithExplicitOptInPublishesOnlyToLoopbackServer(t *testing.T) {
	maskedBytes := []byte("MASKED_CLI_FIXTURE_2026")
	digest := sha256.Sum256(maskedBytes)
	maskedFile := filepath.Join(t.TempDir(), "safe.masked.txt")
	if err := os.WriteFile(maskedFile, maskedBytes, 0o600); err != nil {
		t.Fatalf("write masked fixture: %v", err)
	}

	var gotRequests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequests = append(gotRequests, r.Method+" "+r.URL.Path)
		if got := r.Header.Get("Authorization"); got != "Bearer cli-test-key" {
			t.Errorf("Authorization = %q, want test bearer token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		if len(gotRequests) == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": []map[string]any{{"id": "cli-document"}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0})
	}))
	defer server.Close()

	t.Setenv(networkOptInEnvironment, "1")
	t.Setenv(apiKeyEnvironment, "cli-test-key")
	arguments := []string{
		"-base-url", server.URL,
		"-dataset-id", "cli-dataset",
		"-masked-file", maskedFile,
		"-source-id", "opaque-cli-source",
		"-sha256", hex.EncodeToString(digest[:]),
		"-mask-policy-version", "cli-policy-v1",
		"-publication-id", "cli-publication",
		"-publication-generation", "1",
	}

	documentID, err := run(context.Background(), arguments)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if documentID != "cli-document" {
		t.Errorf("run() document ID = %q, want cli-document", documentID)
	}
	wantRequests := []string{
		"POST /api/v1/datasets/cli-dataset/documents",
		"PUT /api/v1/datasets/cli-dataset/documents/cli-document",
		"POST /api/v1/datasets/cli-dataset/chunks",
	}
	if !reflect.DeepEqual(gotRequests, wantRequests) {
		t.Errorf("request sequence = %#v, want %#v", gotRequests, wantRequests)
	}
}

func validCLIArguments(maskedFile string) []string {
	return []string{
		"-base-url", "http://127.0.0.1:1",
		"-dataset-id", "cli-dataset",
		"-masked-file", maskedFile,
		"-source-id", "opaque-cli-source",
		"-sha256", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"-mask-policy-version", "cli-policy-v1",
		"-publication-id", "cli-publication",
		"-publication-generation", "1",
	}
}

func unsetEnvironment(t *testing.T, key string) {
	t.Helper()
	oldValue, existed := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(key, oldValue)
			return
		}
		_ = os.Unsetenv(key)
	})
}
