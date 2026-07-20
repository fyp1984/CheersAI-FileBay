// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package poc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Client is an isolated, default-off RAGFlow HTTP adapter.
type Client struct {
	baseURL        string
	apiKey         string
	datasetID      string
	networkEnabled bool
	httpClient     *http.Client
}

type responseEnvelope struct {
	Code *int            `json:"code"`
	Data json.RawMessage `json:"data"`
}

// NewClient validates configuration without contacting RAGFlow.
func NewClient(config Config) (*Client, error) {
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("invalid RAGFlow PoC base URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("invalid RAGFlow PoC base URL")
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return nil, fmt.Errorf("unencrypted RAGFlow PoC base URL must use a loopback host")
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, fmt.Errorf("RAGFlow PoC API key is required")
	}
	if !safePathID(config.DatasetID) {
		return nil, fmt.Errorf("RAGFlow PoC dataset ID must be a safe path segment")
	}

	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout:       DefaultHTTPTimeout,
			CheckRedirect: refuseRedirect,
		}
	} else {
		copy := *httpClient
		if copy.Timeout <= 0 || copy.Timeout > DefaultHTTPTimeout {
			copy.Timeout = DefaultHTTPTimeout
		}
		copy.CheckRedirect = refuseRedirect
		httpClient = &copy
	}

	return &Client{
		baseURL:        strings.TrimRight(config.BaseURL, "/"),
		apiKey:         config.APIKey,
		datasetID:      config.DatasetID,
		networkEnabled: config.NetworkEnabled,
		httpClient:     httpClient,
	}, nil
}

// Publish validates and uploads one client-masked artifact, then sets metadata and starts parsing.
func (c *Client) Publish(ctx context.Context, artifact MaskedArtifact) (PublishResult, error) {
	maskedBytes, err := validateArtifact(artifact)
	if err != nil {
		return PublishResult{}, err
	}
	if !c.networkEnabled {
		return PublishResult{}, ErrNetworkDisabled
	}

	documentID, err := c.upload(ctx, artifact.FileName, maskedBytes)
	if err != nil {
		return PublishResult{}, err
	}
	if err := c.setMetadata(ctx, documentID, artifact); err != nil {
		return PublishResult{}, c.cleanupPublishFailure(ctx, "metadata", documentID, err)
	}
	if err := c.startParse(ctx, documentID); err != nil {
		return PublishResult{}, c.cleanupPublishFailure(ctx, "parse", documentID, err)
	}
	return PublishResult{DocumentID: documentID}, nil
}

// Retrieve gets untrusted RAGFlow chunks constrained to one publication generation.
func (c *Client) Retrieve(ctx context.Context, request RetrieveRequest) (RetrieveResult, error) {
	if err := validateRetrieveRequest(request); err != nil {
		return RetrieveResult{}, err
	}
	if !c.networkEnabled {
		return RetrieveResult{}, ErrNetworkDisabled
	}

	pageSize := request.PageSize
	if pageSize <= 0 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100
	}
	body := map[string]any{
		"question":    request.Question,
		"dataset_ids": []string{c.datasetID},
		"page_size":   pageSize,
		"metadata_condition": map[string]any{
			"logic": "and",
			"conditions": []map[string]any{
				{"name": "filebay_publication_id", "comparison_operator": "=", "value": request.PublicationID},
				{"name": "filebay_publication_generation", "comparison_operator": "=", "value": request.PublicationGeneration},
			},
		},
	}
	data, err := c.doJSON(ctx, "retrieve", http.MethodPost, "/api/v1/retrieval", body)
	if err != nil {
		return RetrieveResult{}, err
	}
	var result struct {
		Chunks []RetrievedChunk `json:"chunks"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return RetrieveResult{}, fmt.Errorf("retrieve failed: invalid response data")
	}
	return RetrieveResult{Chunks: result.Chunks}, nil
}

// Delete removes only the explicitly supplied RAGFlow document IDs.
func (c *Client) Delete(ctx context.Context, documentIDs []string) error {
	if err := validateDocumentIDs(documentIDs); err != nil {
		return err
	}
	if !c.networkEnabled {
		return ErrNetworkDisabled
	}
	_, err := c.doJSON(ctx, "delete", http.MethodDelete, c.datasetPath("/documents"), map[string]any{
		"ids": append([]string(nil), documentIDs...),
	})
	return err
}

func (c *Client) upload(ctx context.Context, fileName string, maskedBytes []byte) (string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		return "", fmt.Errorf("upload failed: could not create multipart request")
	}
	if _, err := part.Write(maskedBytes); err != nil {
		return "", fmt.Errorf("upload failed: could not create multipart request")
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("upload failed: could not create multipart request")
	}

	data, err := c.doRequest(ctx, "upload", http.MethodPost, c.datasetPath("/documents"), &body, writer.FormDataContentType())
	if err != nil {
		return "", err
	}
	var documents []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &documents); err != nil || len(documents) != 1 || !safePathID(documents[0].ID) {
		return "", fmt.Errorf("upload failed: invalid document response")
	}
	return documents[0].ID, nil
}

func (c *Client) setMetadata(ctx context.Context, documentID string, artifact MaskedArtifact) error {
	_, err := c.doJSON(ctx, "metadata", http.MethodPut, c.datasetPath("/documents/"+documentID), map[string]any{
		"meta_fields": map[string]any{
			"filebay_masked":                 true,
			"filebay_masked_sha256":          artifact.SHA256,
			"filebay_mask_policy_version":    artifact.MaskPolicyVersion,
			"filebay_publication_id":         artifact.PublicationID,
			"filebay_publication_generation": artifact.PublicationGeneration,
		},
	})
	return err
}

func (c *Client) startParse(ctx context.Context, documentID string) error {
	_, err := c.doJSON(ctx, "parse", http.MethodPost, c.datasetPath("/chunks"), map[string]any{
		"document_ids": []string{documentID},
	})
	return err
}

func (c *Client) cleanupPublishFailure(ctx context.Context, operation, documentID string, original error) error {
	cleanupErr := c.Delete(ctx, []string{documentID})
	return &PublishError{
		Operation:        operation,
		Err:              original,
		CleanupAttempted: true,
		CleanupErr:       cleanupErr,
	}
}

func (c *Client) doJSON(ctx context.Context, operation, method, path string, value any) (json.RawMessage, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%s failed: could not encode request", operation)
	}
	return c.doRequest(ctx, operation, method, path, bytes.NewReader(body), "application/json")
}

func (c *Client) doRequest(ctx context.Context, operation, method, path string, body io.Reader, contentType string) (json.RawMessage, error) {
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("%s failed: could not create request", operation)
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Accept", "application/json")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("%s request failed: %w", operation, ctxErr)
		}
		return nil, fmt.Errorf("%s request failed", operation)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%s failed: could not read response", operation)
	}
	if len(responseBody) > MaxResponseBytes {
		return nil, fmt.Errorf("%s failed: response exceeds size limit", operation)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("%s failed: HTTP status %d", operation, response.StatusCode)
	}

	var envelope responseEnvelope
	if err := json.Unmarshal(responseBody, &envelope); err != nil || envelope.Code == nil {
		return nil, fmt.Errorf("%s failed: invalid response", operation)
	}
	if *envelope.Code != 0 {
		return nil, fmt.Errorf("%s failed: RAGFlow code %d", operation, *envelope.Code)
	}
	return envelope.Data, nil
}

func (c *Client) datasetPath(suffix string) string {
	return "/api/v1/datasets/" + c.datasetID + suffix
}

func safePathID(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func refuseRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
