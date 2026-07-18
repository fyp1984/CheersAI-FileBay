// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package ragflow_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"code.gitea.io/gitea/services/knowledge/ragflow"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testAPIKey   = "ragflow-secret-must-never-leak"
	testDataset  = "dataset-enterprise-private"
	testDocument = "ragflow-document-42"
)

func TestPinnedRAGFlowContractConstants(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "v0.26.4", ragflow.APIVersion)
	assert.Equal(t, 10*time.Second, ragflow.DefaultHTTPTimeout)
	assert.EqualValues(t, 1<<20, ragflow.MaxResponseBytes)
}

func TestRAGFlowV0264WriteAndStatusHTTPContract(t *testing.T) {
	maskedContent := []byte("masked production knowledge")
	requestIndex := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestIndex++
		assert.Equal(t, "Bearer "+testAPIKey, r.Header.Get("Authorization"))

		switch requestIndex {
		case 1:
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "/api/v1/datasets/"+testDataset+"/documents", r.URL.Path)
			fileName, content := readMultipartFile(t, r)
			assert.Equal(t, "masked-policy.md", fileName)
			assert.Equal(t, maskedContent, content)
			writeJSON(t, w, http.StatusOK, map[string]any{
				"code": 0,
				"data": []map[string]any{{"id": testDocument}},
			})
		case 2:
			assert.Equal(t, http.MethodPut, r.Method)
			assert.Equal(t, "/api/v1/datasets/"+testDataset+"/documents/"+testDocument, r.URL.Path)
			body := readJSONObject(t, r.Body)
			meta := objectField(t, body, "meta_fields")
			assert.EqualValues(t, 11, meta["filebay_space_id"])
			assert.EqualValues(t, 22, meta["filebay_document_id"])
			assert.EqualValues(t, 33, meta["filebay_revision_id"])
			assert.EqualValues(t, 44, meta["filebay_publication_id"])
			assert.EqualValues(t, 7, meta["filebay_publication_generation"])
			assert.Equal(t, "5bb1b064554ad5b0c665e1a437b62d85a1ce28e5e54b593f4f874b3b84d6e60b", meta["filebay_revision_sha256"])
			assert.EqualValues(t, 5, meta["filebay_acl_policy_version"])
			assert.EqualValues(t, 3, meta["filebay_revocation_generation"])
			writeJSON(t, w, http.StatusOK, map[string]any{"code": 0})
		case 3:
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "/api/v1/datasets/"+testDataset+"/chunks", r.URL.Path)
			body := readJSONObject(t, r.Body)
			assert.Equal(t, []any{testDocument}, body["document_ids"])
			writeJSON(t, w, http.StatusOK, map[string]any{"code": 0})
		case 4:
			assert.Equal(t, http.MethodGet, r.Method)
			assert.Equal(t, "/api/v1/datasets/"+testDataset+"/documents", r.URL.Path)
			assert.Equal(t, testDocument, r.URL.Query().Get("id"))
			writeJSON(t, w, http.StatusOK, map[string]any{
				"code": 0,
				"data": map[string]any{
					"docs":  []map[string]any{{"id": testDocument, "run": "RUNNING", "chunk_count": 4}},
					"total": 1,
				},
			})
		case 5:
			assert.Equal(t, http.MethodDelete, r.Method)
			assert.Equal(t, "/api/v1/datasets/"+testDataset+"/documents", r.URL.Path)
			body := readJSONObject(t, r.Body)
			assert.Equal(t, []any{testDocument}, body["ids"])
			_, hasDeleteAll := body["delete_all"]
			assert.False(t, hasDeleteAll)
			writeJSON(t, w, http.StatusOK, map[string]any{"code": 0})
		default:
			t.Fatalf("unexpected request %d: %s %s", requestIndex, r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	client := newClient(t, server.URL, server.Client(), 0)
	uploaded, err := client.Upload(t.Context(), ragflow.UploadRequest{
		FileName: "masked-policy.md",
		Content:  bytes.NewReader(maskedContent),
		Size:     int64(len(maskedContent)),
	})
	require.NoError(t, err)
	assert.Equal(t, testDocument, uploaded.ID)

	require.NoError(t, client.SetMetadata(t.Context(), testDocument, ragflow.GovernanceMetadata{
		SpaceID:               11,
		DocumentID:            22,
		RevisionID:            33,
		PublicationID:         44,
		PublicationGeneration: 7,
		RevisionSHA256:        "5bb1b064554ad5b0c665e1a437b62d85a1ce28e5e54b593f4f874b3b84d6e60b",
		ACLPolicyVersion:      5,
		RevocationGeneration:  3,
	}))
	require.NoError(t, client.StartParsing(t.Context(), []string{testDocument}))

	status, err := client.GetDocumentStatus(t.Context(), testDocument)
	require.NoError(t, err)
	assert.Equal(t, testDocument, status.DocumentID)
	assert.Equal(t, ragflow.DocumentStateRunning, status.State)
	assert.EqualValues(t, 4, status.ChunkCount)

	require.NoError(t, client.DeleteDocuments(t.Context(), []string{testDocument}))
	assert.Equal(t, 5, requestIndex)
}

func TestRAGFlowDocumentStatusMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		run  string
		want ragflow.DocumentState
	}{
		{"UNSTART", ragflow.DocumentStateUnstarted},
		{"RUNNING", ragflow.DocumentStateRunning},
		{"DONE", ragflow.DocumentStateDone},
		{"FAIL", ragflow.DocumentStateFailed},
		{"CANCEL", ragflow.DocumentStateFailed},
		{"CANCELED", ragflow.DocumentStateFailed},
	}

	for _, tt := range tests {
		t.Run(tt.run, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, testDocument, r.URL.Query().Get("id"))
				writeJSON(t, w, http.StatusOK, map[string]any{
					"code": 0,
					"data": map[string]any{
						"docs":  []map[string]any{{"id": testDocument, "run": tt.run, "chunk_count": 0}},
						"total": 1,
					},
				})
			}))
			defer server.Close()

			status, err := newClient(t, server.URL, server.Client(), 0).GetDocumentStatus(t.Context(), testDocument)
			require.NoError(t, err)
			assert.Equal(t, tt.want, status.State)
		})
	}
}

func TestRAGFlowClientTimesOutWithinConfiguredBound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(150 * time.Millisecond)
		writeJSON(t, w, http.StatusOK, map[string]any{"code": 0, "data": []map[string]any{{"id": testDocument}}})
	}))
	defer server.Close()

	started := time.Now()
	_, err := newClient(t, server.URL, server.Client(), 25*time.Millisecond).Upload(t.Context(), ragflow.UploadRequest{
		FileName: "masked-policy.md",
		Content:  strings.NewReader("masked"),
		Size:     int64(len("masked")),
	})
	require.Error(t, err)
	assert.Less(t, time.Since(started), 500*time.Millisecond)
}

func TestRAGFlowClientRejectsRedirectWithoutLeakingCredentials(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
		writeJSON(t, w, http.StatusOK, map[string]any{"code": 0, "data": []map[string]any{{"id": testDocument}}})
	}))
	defer target.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", target.URL+"/credential-trap")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	_, err := newClient(t, origin.URL, origin.Client(), 0).Upload(t.Context(), ragflow.UploadRequest{
		FileName: "masked-policy.md",
		Content:  strings.NewReader("masked"),
		Size:     int64(len("masked")),
	})
	require.Error(t, err)
	assert.Zero(t, targetHits.Load())
	assert.NotContains(t, err.Error(), testAPIKey)
}

func TestRAGFlowClientFailsClosedOnOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(bytes.Repeat([]byte("x"), ragflow.MaxResponseBytes+1))
	}))
	defer server.Close()

	_, err := newClient(t, server.URL, server.Client(), 0).Upload(t.Context(), ragflow.UploadRequest{
		FileName: "masked-policy.md",
		Content:  strings.NewReader("masked"),
		Size:     int64(len("masked")),
	})
	require.Error(t, err)
}

func TestRAGFlowClientFailsOnNonzeroBusinessCodeAndRedactsUpstreamBody(t *testing.T) {
	maskedBody := "masked-body-must-not-be-copied-into-errors"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]any{
			"code":    91,
			"message": "upstream echoed " + testAPIKey + " and " + maskedBody,
		})
	}))
	defer server.Close()

	_, err := newClient(t, server.URL, server.Client(), 0).Upload(t.Context(), ragflow.UploadRequest{
		FileName: "masked-policy.md",
		Content:  strings.NewReader(maskedBody),
		Size:     int64(len(maskedBody)),
	})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), testAPIKey)
	assert.NotContains(t, err.Error(), maskedBody)
	assert.Contains(t, strings.ToLower(err.Error()), "upload")
}

func TestRAGFlowClientDoesNotMutateSuppliedHTTPClient(t *testing.T) {
	t.Parallel()

	supplied := &http.Client{Timeout: time.Hour}
	_, err := ragflow.NewClient(ragflow.Config{
		BaseURL:           "http://127.0.0.1:8080",
		APIKey:            testAPIKey,
		DatasetID:         testDataset,
		AllowLoopbackHTTP: true,
		HTTPClient:        supplied,
		Timeout:           25 * time.Millisecond,
	})
	require.NoError(t, err)
	assert.Equal(t, time.Hour, supplied.Timeout)
}

func TestRAGFlowClientRejectsRemotePlainHTTPBeforeTransport(t *testing.T) {
	t.Parallel()

	transportCalls := 0
	client, err := ragflow.NewClient(ragflow.Config{
		BaseURL:   "http://ragflow.example.com",
		APIKey:    testAPIKey,
		DatasetID: testDataset,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			transportCalls++
			return nil, errors.New("must not be called")
		})},
	})
	assert.Error(t, err)
	assert.Nil(t, client)
	assert.Zero(t, transportCalls)
}

func TestRAGFlowMetadataValidationFailsBeforeTransport(t *testing.T) {
	t.Parallel()

	valid := ragflow.GovernanceMetadata{
		SpaceID:               11,
		DocumentID:            22,
		RevisionID:            33,
		PublicationID:         44,
		PublicationGeneration: 7,
		RevisionSHA256:        "5bb1b064554ad5b0c665e1a437b62d85a1ce28e5e54b593f4f874b3b84d6e60b",
		ACLPolicyVersion:      5,
		RevocationGeneration:  3,
	}
	tests := []struct {
		name   string
		mutate func(*ragflow.GovernanceMetadata)
	}{
		{"space id zero", func(value *ragflow.GovernanceMetadata) { value.SpaceID = 0 }},
		{"document id negative", func(value *ragflow.GovernanceMetadata) { value.DocumentID = -1 }},
		{"revision id zero", func(value *ragflow.GovernanceMetadata) { value.RevisionID = 0 }},
		{"publication id zero", func(value *ragflow.GovernanceMetadata) { value.PublicationID = 0 }},
		{"publication generation zero", func(value *ragflow.GovernanceMetadata) { value.PublicationGeneration = 0 }},
		{"acl policy version zero", func(value *ragflow.GovernanceMetadata) { value.ACLPolicyVersion = 0 }},
		{"revocation generation negative", func(value *ragflow.GovernanceMetadata) { value.RevocationGeneration = -1 }},
		{"sha too short", func(value *ragflow.GovernanceMetadata) { value.RevisionSHA256 = strings.Repeat("a", 63) }},
		{"sha is not hexadecimal", func(value *ragflow.GovernanceMetadata) { value.RevisionSHA256 = strings.Repeat("z", 64) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transportCalls := 0
			client := newClient(t, "http://127.0.0.1:8080", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				transportCalls++
				return nil, errors.New("must not be called")
			})}, 0)
			metadata := valid
			tt.mutate(&metadata)
			assert.Error(t, client.SetMetadata(context.Background(), testDocument, metadata))
			assert.Zero(t, transportCalls)
		})
	}
}

func TestRAGFlowUploadSizeLimitRejectsBeforeTransport(t *testing.T) {
	t.Parallel()

	transportCalls := 0
	client, err := ragflow.NewClient(ragflow.Config{
		BaseURL:           "http://127.0.0.1:8080",
		APIKey:            testAPIKey,
		DatasetID:         testDataset,
		AllowLoopbackHTTP: true,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			transportCalls++
			return nil, errors.New("must not be called")
		})},
		MaxUploadBytes: 4,
	})
	require.NoError(t, err)

	_, err = client.Upload(context.Background(), ragflow.UploadRequest{
		FileName: "masked.md",
		Content:  strings.NewReader("12345"),
		Size:     5,
	})
	assert.Error(t, err)
	assert.Zero(t, transportCalls)
}

func TestRAGFlowOperationsRejectUnsafeIdentifiersBeforeTransport(t *testing.T) {
	t.Parallel()

	transportCalls := 0
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		transportCalls++
		return nil, errors.New("must not be called")
	})}
	client := newClient(t, "http://127.0.0.1:8080", httpClient, 0)

	tests := []func() error{
		func() error {
			return client.SetMetadata(context.Background(), "../document", ragflow.GovernanceMetadata{})
		},
		func() error { return client.StartParsing(context.Background(), []string{""}) },
		func() error { _, err := client.GetDocumentStatus(context.Background(), "document/id"); return err },
		func() error { return client.DeleteDocuments(context.Background(), nil) },
	}
	for _, call := range tests {
		assert.Error(t, call())
	}
	assert.Zero(t, transportCalls)
}

func TestRAGFlowRetrieveUsesDatasetQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/retrieval", r.URL.Path)
		body := readJSONObject(t, r.Body)
		assert.Equal(t, "客户投诉处理时限", body["question"])
		assert.Equal(t, []any{testDataset}, body["dataset_ids"])
		assert.NotContains(t, body, "metadata_condition")
		writeJSON(t, w, http.StatusOK, map[string]any{"code": 0, "data": map[string]any{"chunks": []map[string]any{{"id": "chunk-1", "document_id": testDocument, "content": "脱敏知识片段", "similarity": 0.91}}}})
	}))
	defer server.Close()

	chunks, err := newClient(t, server.URL, server.Client(), 0).RetrievePublication(t.Context(), ragflow.RetrievalRequest{Question: "客户投诉处理时限", PublicationID: 42, PublicationGeneration: 7, TopK: 3})
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	assert.Equal(t, testDocument, chunks[0].DocumentID)
}

func newClient(t *testing.T, baseURL string, httpClient *http.Client, timeout time.Duration) *ragflow.Client {
	t.Helper()

	client, err := ragflow.NewClient(ragflow.Config{
		BaseURL:           baseURL,
		APIKey:            testAPIKey,
		DatasetID:         testDataset,
		AllowLoopbackHTTP: true,
		HTTPClient:        httpClient,
		Timeout:           timeout,
		MaxUploadBytes:    32 << 20,
	})
	require.NoError(t, err)
	return client
}

func readMultipartFile(t *testing.T, r *http.Request) (string, []byte) {
	t.Helper()

	require.NoError(t, r.ParseMultipartForm(1<<20))
	file, header, err := r.FormFile("file")
	require.NoError(t, err)
	defer file.Close()
	content, err := io.ReadAll(file)
	require.NoError(t, err)
	return header.Filename, content
}

func readJSONObject(t *testing.T, reader io.ReadCloser) map[string]any {
	t.Helper()
	defer reader.Close()
	var object map[string]any
	require.NoError(t, json.NewDecoder(reader).Decode(&object))
	return object
}

func objectField(t *testing.T, object map[string]any, field string) map[string]any {
	t.Helper()
	value, ok := object[field].(map[string]any)
	require.True(t, ok, "field %q must be an object, got %#v", field, object[field])
	return value
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	require.NoError(t, json.NewEncoder(w).Encode(value))
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

var _ http.RoundTripper = roundTripFunc(nil)
