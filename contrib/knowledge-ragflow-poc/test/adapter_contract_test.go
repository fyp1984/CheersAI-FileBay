// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package poc_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	poc "code.gitea.io/gitea/contrib/knowledge-ragflow-poc"
)

func TestPublishPerformsOrderedUploadMetadataAndParse(t *testing.T) {
	artifact := validArtifact()
	localPathSentinel := `C:\Users\alice\source\payroll.txt`
	log := &requestLog{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := log.capture(t, r)
		if got.Authorization != "Bearer "+testAPIKey {
			t.Errorf("Authorization = %q, want bearer token", got.Authorization)
		}

		switch len(log.snapshot()) {
		case 1:
			if got.Method != http.MethodPost || got.Path != "/api/v1/datasets/"+testDatasetID+"/documents" {
				t.Errorf("upload request = %s %s", got.Method, got.Path)
			}
			if !strings.HasPrefix(got.ContentType, "multipart/form-data;") {
				t.Errorf("upload Content-Type = %q, want multipart/form-data", got.ContentType)
			}
			if !bytes.Contains(got.Body, artifact.Bytes) {
				t.Error("upload body does not contain the masked fixture")
			}
			if !bytes.Contains(got.Body, []byte(artifact.FileName)) {
				t.Error("upload body does not contain the safe base file name")
			}
			writeJSON(t, w, http.StatusOK, map[string]any{
				"code": 0,
				"data": []map[string]any{{"id": testDocument}},
			})
		case 2:
			if got.Method != http.MethodPut || got.Path != "/api/v1/datasets/"+testDatasetID+"/documents/"+testDocument {
				t.Errorf("metadata request = %s %s", got.Method, got.Path)
			}
			body := decodeObject(t, got.Body)
			metadata := objectField(t, body, "meta_fields")
			want := map[string]any{
				"filebay_masked":                 true,
				"filebay_masked_sha256":          artifact.SHA256,
				"filebay_mask_policy_version":    artifact.MaskPolicyVersion,
				"filebay_publication_id":         artifact.PublicationID,
				"filebay_publication_generation": float64(artifact.PublicationGeneration),
			}
			for key, wantValue := range want {
				if gotValue := metadata[key]; !reflect.DeepEqual(gotValue, wantValue) {
					t.Errorf("metadata[%q] = %#v, want %#v", key, gotValue, wantValue)
				}
			}
			writeJSON(t, w, http.StatusOK, map[string]any{"code": 0})
		case 3:
			if got.Method != http.MethodPost || got.Path != "/api/v1/datasets/"+testDatasetID+"/chunks" {
				t.Errorf("parse request = %s %s", got.Method, got.Path)
			}
			body := decodeObject(t, got.Body)
			assertDocumentIDs(t, body, []string{testDocument})
			writeJSON(t, w, http.StatusOK, map[string]any{"code": 0})
		default:
			t.Errorf("unexpected extra request: %s %s", got.Method, got.Path)
			writeJSON(t, w, http.StatusInternalServerError, map[string]any{"code": 1})
		}
	}))
	defer server.Close()

	client := newClient(t, server.URL, true, server.Client())
	result, err := client.Publish(context.Background(), artifact)
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if result.DocumentID != testDocument {
		t.Errorf("Publish() DocumentID = %q, want %q", result.DocumentID, testDocument)
	}

	requests := log.snapshot()
	if len(requests) != 3 {
		t.Fatalf("request count = %d, want 3", len(requests))
	}
	for _, req := range requests {
		if bytes.Contains(req.Body, []byte(localPathSentinel)) {
			t.Errorf("request %s %s leaked local source path", req.Method, req.Path)
		}
	}
}

func TestRetrieveScopesDatasetAndPublicationMetadataAndCapsPageSize(t *testing.T) {
	var request capturedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := readRequestBody(r)
		if err != nil {
			t.Fatalf("read retrieval body: %v", err)
		}
		request = capturedRequest{Method: r.Method, Path: r.URL.Path, Authorization: r.Header.Get("Authorization"), Body: body}
		writeJSON(t, w, http.StatusOK, map[string]any{
			"code": 0,
			"data": map[string]any{
				"chunks": []map[string]any{{
					"id":          "chunk-1",
					"document_id": testDocument,
					"content":     "masked answer",
					"similarity":  0.875,
				}},
			},
		})
	}))
	defer server.Close()

	client := newClient(t, server.URL, true, server.Client())
	result, err := client.Retrieve(context.Background(), poc.RetrieveRequest{
		Question:              "What is the approved answer?",
		PublicationID:         "publication-abc",
		PublicationGeneration: 7,
		PageSize:              1000,
	})
	if err != nil {
		t.Fatalf("Retrieve() error = %v", err)
	}
	if len(result.Chunks) != 1 {
		t.Fatalf("chunk count = %d, want 1", len(result.Chunks))
	}
	chunk := result.Chunks[0]
	if chunk.ID != "chunk-1" || chunk.DocumentID != testDocument || chunk.Content != "masked answer" || chunk.Similarity != 0.875 {
		t.Errorf("unexpected chunk: %#v", chunk)
	}

	if request.Method != http.MethodPost || request.Path != "/api/v1/retrieval" {
		t.Errorf("retrieval request = %s %s", request.Method, request.Path)
	}
	if request.Authorization != "Bearer "+testAPIKey {
		t.Errorf("Authorization = %q, want bearer token", request.Authorization)
	}
	body := decodeObject(t, request.Body)
	if got := body["question"]; got != "What is the approved answer?" {
		t.Errorf("question = %#v", got)
	}
	if got := stringSliceField(t, body, "dataset_ids"); !reflect.DeepEqual(got, []string{testDatasetID}) {
		t.Errorf("dataset_ids = %#v, want [%q]", got, testDatasetID)
	}
	if got := body["page_size"]; got != float64(100) {
		t.Errorf("page_size = %#v, want 100", got)
	}

	condition := objectField(t, body, "metadata_condition")
	if logic := condition["logic"]; !strings.EqualFold(fmt.Sprint(logic), "and") {
		t.Errorf("metadata condition logic = %#v, want AND", logic)
	}
	conditions := objectSliceField(t, condition, "conditions")
	assertMetadataCondition(t, conditions, "filebay_publication_id", "publication-abc")
	assertMetadataCondition(t, conditions, "filebay_publication_generation", float64(7))
}

func TestDeleteSendsOnlyExplicitDocumentIDs(t *testing.T) {
	var request capturedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := readRequestBody(r)
		if err != nil {
			t.Fatalf("read delete body: %v", err)
		}
		request = capturedRequest{Method: r.Method, Path: r.URL.Path, Body: body}
		writeJSON(t, w, http.StatusOK, map[string]any{"code": 0})
	}))
	defer server.Close()

	client := newClient(t, server.URL, true, server.Client())
	wantIDs := []string{"document-created-by-poc", "document-created-by-poc-2"}
	if err := client.Delete(context.Background(), wantIDs); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if request.Method != http.MethodDelete || request.Path != "/api/v1/datasets/"+testDatasetID+"/documents" {
		t.Errorf("delete request = %s %s", request.Method, request.Path)
	}
	body := decodeObject(t, request.Body)
	assertDeleteIDs(t, body, wantIDs)
	if value, exists := body["delete_all"]; exists {
		t.Errorf("delete request contains forbidden delete_all field: %#v", value)
	}
}

func TestMetadataAndParseFailuresTriggerOneCleanupWithoutRetry(t *testing.T) {
	tests := []struct {
		name         string
		failureIndex int
		operation    string
		wantPaths    []string
	}{
		{
			name:         "metadata failure",
			failureIndex: 2,
			operation:    "metadata",
			wantPaths: []string{
				"/api/v1/datasets/" + testDatasetID + "/documents",
				"/api/v1/datasets/" + testDatasetID + "/documents/" + testDocument,
				"/api/v1/datasets/" + testDatasetID + "/documents",
			},
		},
		{
			name:         "parse failure",
			failureIndex: 3,
			operation:    "parse",
			wantPaths: []string{
				"/api/v1/datasets/" + testDatasetID + "/documents",
				"/api/v1/datasets/" + testDatasetID + "/documents/" + testDocument,
				"/api/v1/datasets/" + testDatasetID + "/chunks",
				"/api/v1/datasets/" + testDatasetID + "/documents",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := &requestLog{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got := log.capture(t, r)
				index := len(log.snapshot())
				if index == tt.failureIndex {
					writeJSON(t, w, http.StatusOK, map[string]any{"code": 23, "message": "injected contract failure"})
					return
				}
				if index == len(tt.wantPaths) {
					body := decodeObject(t, got.Body)
					assertDeleteIDs(t, body, []string{testDocument})
					if _, exists := body["delete_all"]; exists {
						t.Error("cleanup delete contains delete_all")
					}
				}
				if index == 1 {
					writeJSON(t, w, http.StatusOK, map[string]any{"code": 0, "data": []map[string]any{{"id": testDocument}}})
					return
				}
				writeJSON(t, w, http.StatusOK, map[string]any{"code": 0})
			}))
			defer server.Close()

			client := newClient(t, server.URL, true, server.Client())
			_, err := publish(t, client)
			if err == nil {
				t.Fatal("Publish() error = nil, want failure")
			}
			var publishErr *poc.PublishError
			if !errors.As(err, &publishErr) {
				t.Fatalf("Publish() error type = %T, want *PublishError", err)
			}
			if publishErr.Operation != tt.operation {
				t.Errorf("PublishError.Operation = %q, want %q", publishErr.Operation, tt.operation)
			}
			if !publishErr.CleanupAttempted {
				t.Error("PublishError.CleanupAttempted = false, want true")
			}
			if publishErr.CleanupErr != nil {
				t.Errorf("PublishError.CleanupErr = %v, want nil", publishErr.CleanupErr)
			}

			requests := log.snapshot()
			paths := make([]string, 0, len(requests))
			for _, req := range requests {
				paths = append(paths, req.Path)
			}
			if !reflect.DeepEqual(paths, tt.wantPaths) {
				t.Errorf("request paths = %#v, want %#v", paths, tt.wantPaths)
			}
		})
	}
}

func TestCleanupFailureIsReportedWithoutReplacingOriginalFailure(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		switch requestCount {
		case 1:
			writeJSON(t, w, http.StatusOK, map[string]any{"code": 0, "data": []map[string]any{{"id": testDocument}}})
		case 2:
			writeJSON(t, w, http.StatusOK, map[string]any{"code": 91, "message": "metadata-original-failure"})
		case 3:
			writeJSON(t, w, http.StatusInternalServerError, map[string]any{"code": 92, "message": "cleanup-secondary-failure"})
		default:
			t.Errorf("unexpected retry request %d", requestCount)
		}
	}))
	defer server.Close()

	client := newClient(t, server.URL, true, server.Client())
	_, err := publish(t, client)
	var publishErr *poc.PublishError
	if !errors.As(err, &publishErr) {
		t.Fatalf("Publish() error = %v, want *PublishError", err)
	}
	if publishErr.Operation != "metadata" {
		t.Errorf("original operation = %q, want metadata", publishErr.Operation)
	}
	if !publishErr.CleanupAttempted || publishErr.CleanupErr == nil {
		t.Errorf("cleanup outcome = attempted %t, error %v; want attempted failure", publishErr.CleanupAttempted, publishErr.CleanupErr)
	}
	if requestCount != 3 {
		t.Errorf("request count = %d, want upload + metadata + one cleanup", requestCount)
	}
}

func TestRAGFlowNonzeroCodeFailsOnHTTP2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]any{"code": 17, "message": "logical failure"})
	}))
	defer server.Close()

	client := newClient(t, server.URL, true, server.Client())
	_, err := client.Retrieve(context.Background(), poc.RetrieveRequest{
		Question:              "question",
		PublicationID:         "publication-abc",
		PublicationGeneration: 7,
		PageSize:              5,
	})
	if err == nil {
		t.Fatal("Retrieve() error = nil for nonzero RAGFlow code")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "retrieve") {
		t.Errorf("error %q lacks operation context", err)
	}
}

func TestPublishNeverFollows307Or308Redirects(t *testing.T) {
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var originHits atomic.Int32
			var redirectTargetHits atomic.Int32
			redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				redirectTargetHits.Add(1)
				writeJSON(t, w, http.StatusOK, map[string]any{
					"code": 0,
					"data": []map[string]any{{"id": "redirected-document"}},
				})
			}))
			defer redirectTarget.Close()

			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				originHits.Add(1)
				w.Header().Set("Location", redirectTarget.URL+"/must-not-be-reached")
				w.WriteHeader(status)
			}))
			defer origin.Close()

			client := newClient(t, origin.URL, true, origin.Client())
			_, err := publish(t, client)
			if err == nil {
				t.Fatal("Publish() error = nil for redirect response")
			}
			if got := originHits.Load(); got != 1 {
				t.Errorf("origin request count = %d, want exactly one upload", got)
			}
			if got := redirectTargetHits.Load(); got != 0 {
				t.Errorf("redirect target request count = %d, want 0", got)
			}
		})
	}
}

func TestInvalidRetrieveAndDeleteInputsDoNotReachTransport(t *testing.T) {
	tests := []struct {
		name string
		call func(*poc.Client) error
	}{
		{
			name: "empty retrieval question",
			call: func(client *poc.Client) error {
				_, err := client.Retrieve(context.Background(), poc.RetrieveRequest{
					PublicationID:         "publication-abc",
					PublicationGeneration: 7,
					PageSize:              5,
				})
				return err
			},
		},
		{
			name: "empty retrieval publication ID",
			call: func(client *poc.Client) error {
				_, err := client.Retrieve(context.Background(), poc.RetrieveRequest{
					Question:              "question",
					PublicationGeneration: 7,
					PageSize:              5,
				})
				return err
			},
		},
		{
			name: "non-positive retrieval generation",
			call: func(client *poc.Client) error {
				_, err := client.Retrieve(context.Background(), poc.RetrieveRequest{
					Question:      "question",
					PublicationID: "publication-abc",
					PageSize:      5,
				})
				return err
			},
		},
		{
			name: "empty delete ID list",
			call: func(client *poc.Client) error {
				return client.Delete(context.Background(), nil)
			},
		},
		{
			name: "delete list containing empty ID",
			call: func(client *poc.Client) error {
				return client.Delete(context.Background(), []string{""})
			},
		},
		{
			name: "delete traversal document ID",
			call: func(client *poc.Client) error {
				return client.Delete(context.Background(), []string{"../doc"})
			},
		},
		{
			name: "delete document ID containing slash",
			call: func(client *poc.Client) error {
				return client.Delete(context.Background(), []string{"doc/id"})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return nil, errors.New("transport must not be called")
			})}
			client := newClient(t, "http://127.0.0.1", true, httpClient)
			if err := tt.call(client); err == nil {
				t.Fatal("operation error = nil, want validation failure")
			}
			if calls != 0 {
				t.Errorf("transport calls = %d, want 0", calls)
			}
		})
	}
}

func TestNewClientRejectsUnsafeDatasetIDs(t *testing.T) {
	for _, datasetID := range []string{"..", "dataset/id", "dataset%2fid"} {
		t.Run(datasetID, func(t *testing.T) {
			calls := 0
			httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return nil, errors.New("transport must not be called")
			})}

			client, err := poc.NewClient(poc.Config{
				BaseURL:        "http://127.0.0.1",
				APIKey:         testAPIKey,
				DatasetID:      datasetID,
				NetworkEnabled: true,
				HTTPClient:     httpClient,
			})
			if err == nil {
				t.Fatalf("NewClient() = %#v, nil; want unsafe dataset ID error", client)
			}
			if calls != 0 {
				t.Errorf("transport calls = %d, want 0", calls)
			}
		})
	}
}

func TestNewClientRejectsPlainHTTPForNonLoopbackHosts(t *testing.T) {
	for _, baseURL := range []string{
		"http://example.com",
		"http://example.com:8080/ragflow",
		"http://192.0.2.1",
		"http://192.0.2.1:8080/ragflow",
	} {
		t.Run(baseURL, func(t *testing.T) {
			calls := 0
			httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return nil, errors.New("transport must not be called")
			})}

			client, err := poc.NewClient(poc.Config{
				BaseURL:        baseURL,
				APIKey:         testAPIKey,
				DatasetID:      testDatasetID,
				NetworkEnabled: true,
				HTTPClient:     httpClient,
			})
			if err == nil {
				t.Fatalf("NewClient() = %#v, nil; want insecure remote HTTP error", client)
			}
			if calls != 0 {
				t.Errorf("transport calls = %d, want 0", calls)
			}
		})
	}
}

func TestNewClientAllowsHTTPSAndLoopbackHTTP(t *testing.T) {
	for _, baseURL := range []string{
		"https://example.com",
		"https://192.0.2.1:8443/ragflow",
		"http://localhost:8080",
		"http://127.0.0.1:8080/ragflow",
		"http://[::1]:8080",
	} {
		t.Run(baseURL, func(t *testing.T) {
			calls := 0
			httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return nil, errors.New("transport must not be called")
			})}

			client, err := poc.NewClient(poc.Config{
				BaseURL:        baseURL,
				APIKey:         testAPIKey,
				DatasetID:      testDatasetID,
				NetworkEnabled: true,
				HTTPClient:     httpClient,
			})
			if err != nil {
				t.Fatalf("NewClient() error = %v, want URL allowed", err)
			}
			if client == nil {
				t.Fatal("NewClient() client = nil, want configured client")
			}
			if calls != 0 {
				t.Errorf("transport calls = %d, want 0", calls)
			}
		})
	}
}

func TestErrorsNeverExposeBearerTokenOrArtifactBytes(t *testing.T) {
	artifact := validArtifact()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, http.StatusBadGateway, map[string]any{
			"code":    87,
			"message": "upstream echoed " + testAPIKey + " and " + string(artifact.Bytes),
		})
	}))
	defer server.Close()

	client := newClient(t, server.URL, true, server.Client())
	_, err := client.Publish(context.Background(), artifact)
	if err == nil {
		t.Fatal("Publish() error = nil, want HTTP failure")
	}
	message := err.Error()
	if strings.Contains(message, testAPIKey) {
		t.Errorf("error leaked bearer token: %q", message)
	}
	if strings.Contains(message, string(artifact.Bytes)) {
		t.Errorf("error leaked artifact bytes: %q", message)
	}
	if !strings.Contains(strings.ToLower(message), "upload") {
		t.Errorf("error %q lacks upload operation context", message)
	}
}

func TestResponseBodyLargerThanLimitFailsClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(bytes.Repeat([]byte("x"), poc.MaxResponseBytes+1))
	}))
	defer server.Close()

	client := newClient(t, server.URL, true, server.Client())
	_, err := publish(t, client)
	if err == nil {
		t.Fatal("Publish() error = nil for oversized response")
	}
}

func TestDefaultHTTPTimeoutIsBounded(t *testing.T) {
	if poc.DefaultHTTPTimeout <= 0 || poc.DefaultHTTPTimeout > 10*time.Second {
		t.Fatalf("DefaultHTTPTimeout = %s, want > 0 and <= 10s", poc.DefaultHTTPTimeout)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func readRequestBody(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	var body bytes.Buffer
	_, err := body.ReadFrom(r.Body)
	return body.Bytes(), err
}

func decodeObject(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("decode JSON body %q: %v", body, err)
	}
	return value
}

func objectField(t *testing.T, object map[string]any, field string) map[string]any {
	t.Helper()
	value, ok := object[field].(map[string]any)
	if !ok {
		t.Fatalf("field %q = %#v, want object", field, object[field])
	}
	return value
}

func objectSliceField(t *testing.T, object map[string]any, field string) []map[string]any {
	t.Helper()
	values, ok := object[field].([]any)
	if !ok {
		t.Fatalf("field %q = %#v, want array", field, object[field])
	}
	result := make([]map[string]any, 0, len(values))
	for i, value := range values {
		item, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("field %q item %d = %#v, want object", field, i, value)
		}
		result = append(result, item)
	}
	return result
}

func stringSliceField(t *testing.T, object map[string]any, field string) []string {
	t.Helper()
	values, ok := object[field].([]any)
	if !ok {
		t.Fatalf("field %q = %#v, want array", field, object[field])
	}
	result := make([]string, 0, len(values))
	for i, value := range values {
		item, ok := value.(string)
		if !ok {
			t.Fatalf("field %q item %d = %#v, want string", field, i, value)
		}
		result = append(result, item)
	}
	return result
}

func assertDocumentIDs(t *testing.T, body map[string]any, want []string) {
	t.Helper()
	if got := stringSliceField(t, body, "document_ids"); !reflect.DeepEqual(got, want) {
		t.Errorf("document_ids = %#v, want %#v", got, want)
	}
}

func assertDeleteIDs(t *testing.T, body map[string]any, want []string) {
	t.Helper()
	if got := stringSliceField(t, body, "ids"); !reflect.DeepEqual(got, want) {
		t.Errorf("ids = %#v, want %#v", got, want)
	}
}

func assertMetadataCondition(t *testing.T, conditions []map[string]any, name string, wantValue any) {
	t.Helper()
	for _, condition := range conditions {
		if condition["name"] == name {
			if got := condition["value"]; !reflect.DeepEqual(got, wantValue) {
				t.Errorf("metadata condition %q value = %#v, want %#v", name, got, wantValue)
			}
			return
		}
	}
	t.Errorf("metadata condition %q is missing from %#v", name, conditions)
}
