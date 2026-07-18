// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Package ragflow implements the pinned RAGFlow v0.26.4 write/status contract.
package ragflow

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const (
	APIVersion         = "v0.26.4"
	DefaultHTTPTimeout = 10 * time.Second
	MaxResponseBytes   = 1 << 20
	defaultUploadBytes = 32 << 20
)

var safeIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,254}$`)

// Config defines a server-held RAGFlow client configuration.
type Config struct {
	BaseURL           string
	APIKey            string
	DatasetID         string
	AllowLoopbackHTTP bool
	AllowInternalHTTP bool
	InternalHTTPHost  string
	HTTPClient        *http.Client
	Timeout           time.Duration
	MaxUploadBytes    int64
}

// Client is a bounded client for one private RAGFlow dataset.
type Client struct {
	baseURL        string
	apiKey         string
	datasetID      string
	http           *http.Client
	maxUploadBytes int64
}

// UploadRequest contains an explicitly masked artifact stream.
type UploadRequest struct {
	FileName string
	Content  io.Reader
	Size     int64
}

// UploadedDocument identifies the derived RAGFlow document.
type UploadedDocument struct {
	ID string
}

// GovernanceMetadata links a derived document to authoritative FileBay state.
type GovernanceMetadata struct {
	SpaceID               int64
	DocumentID            int64
	RevisionID            int64
	PublicationID         int64
	PublicationGeneration int64
	RevisionSHA256        string
	ACLPolicyVersion      int64
	RevocationGeneration  int64
}

// DocumentState is the normalized RAGFlow parsing state.
type DocumentState string

const (
	DocumentStateUnstarted DocumentState = "unstarted"
	DocumentStateRunning   DocumentState = "running"
	DocumentStateDone      DocumentState = "done"
	DocumentStateFailed    DocumentState = "failed"
)

// DocumentStatus is the bounded status projection used by FileBay workers.
type DocumentStatus struct {
	DocumentID string
	State      DocumentState
	ChunkCount int64
}

// RetrievalRequest scopes an untrusted retrieval to one already-authorized
// FileBay publication generation. It never carries local paths or raw files.
type RetrievalRequest struct {
	Question              string
	PublicationID         int64
	PublicationGeneration int64
	TopK                  int
}

// RetrievedChunk is the bounded RAGFlow result shape consumed by FileBay's
// authorization gateway. It remains untrusted until FileBay validates it.
type RetrievedChunk struct {
	ID         string  `json:"id"`
	DocumentID string  `json:"document_id"`
	Content    string  `json:"content"`
	Similarity float64 `json:"similarity"`
}

// NewClient validates transport security and copies the supplied HTTP client so
// caller-owned settings are never mutated.
func NewClient(config Config) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || parsed.Host == "" || parsed.Scheme == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("invalid RAGFlow base URL")
	}
	if parsed.Scheme != "https" {
		loopbackAllowed := config.AllowLoopbackHTTP && isLoopbackHost(parsed.Hostname())
		internalAllowed := config.AllowInternalHTTP && isInternalDockerHost(parsed.Hostname(), config.InternalHTTPHost)
		if parsed.Scheme != "http" || (!loopbackAllowed && !internalAllowed) {
			return nil, errors.New("RAGFlow requires HTTPS")
		}
	}
	if strings.TrimSpace(config.APIKey) == "" || !validIdentifier(config.DatasetID) {
		return nil, errors.New("invalid RAGFlow client configuration")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return nil, errors.New("invalid RAGFlow base URL")
	}

	timeout := config.Timeout
	if timeout <= 0 {
		timeout = DefaultHTTPTimeout
	}
	maxUploadBytes := config.MaxUploadBytes
	if maxUploadBytes == 0 {
		maxUploadBytes = defaultUploadBytes
	}
	if maxUploadBytes < 0 || maxUploadBytes > defaultUploadBytes {
		return nil, errors.New("invalid RAGFlow upload limit")
	}
	baseClient := config.HTTPClient
	if baseClient == nil {
		baseClient = http.DefaultClient
	}
	httpClient := *baseClient
	httpClient.Timeout = timeout
	httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return errors.New("RAGFlow redirect rejected")
	}

	return &Client{
		baseURL:        strings.TrimRight(parsed.String(), "/"),
		apiKey:         config.APIKey,
		datasetID:      config.DatasetID,
		http:           &httpClient,
		maxUploadBytes: maxUploadBytes,
	}, nil
}

// Upload streams a masked artifact to the configured dataset.
func (c *Client) Upload(ctx context.Context, upload UploadRequest) (*UploadedDocument, error) {
	if upload.Content == nil || !safeFileName(upload.FileName) || upload.Size <= 0 || upload.Size > c.maxUploadBytes {
		return nil, errors.New("upload: invalid masked artifact")
	}

	var requestBody bytes.Buffer
	multipartWriter := multipart.NewWriter(&requestBody)
	contentType := multipartWriter.FormDataContentType()
	part, err := multipartWriter.CreateFormFile("file", upload.FileName)
	if err != nil {
		return nil, errors.New("upload: build multipart request")
	}
	written, err := io.Copy(part, io.LimitReader(upload.Content, upload.Size+1))
	if err != nil || written != upload.Size {
		return nil, errors.New("upload: masked artifact size mismatch")
	}
	if err := multipartWriter.Close(); err != nil {
		return nil, errors.New("upload: build multipart request")
	}

	var envelope responseEnvelope
	if err := c.do(ctx, http.MethodPost, c.datasetPath("documents"), &requestBody, contentType, "upload", &envelope); err != nil {
		return nil, err
	}
	var documents []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(envelope.Data, &documents); err != nil || len(documents) != 1 || !validIdentifier(documents[0].ID) {
		return nil, errors.New("upload: invalid RAGFlow response")
	}
	return &UploadedDocument{ID: documents[0].ID}, nil
}

// SetMetadata writes the immutable FileBay governance identifiers.
func (c *Client) SetMetadata(ctx context.Context, documentID string, metadata GovernanceMetadata) error {
	if !validIdentifier(documentID) {
		return errors.New("metadata: invalid document identifier")
	}
	if !validGovernanceMetadata(metadata) {
		return errors.New("metadata: invalid governance values")
	}
	body := map[string]any{
		"meta_fields": map[string]any{
			"filebay_space_id":               metadata.SpaceID,
			"filebay_document_id":            metadata.DocumentID,
			"filebay_revision_id":            metadata.RevisionID,
			"filebay_publication_id":         metadata.PublicationID,
			"filebay_publication_generation": metadata.PublicationGeneration,
			"filebay_revision_sha256":        metadata.RevisionSHA256,
			"filebay_acl_policy_version":     metadata.ACLPolicyVersion,
			"filebay_revocation_generation":  metadata.RevocationGeneration,
		},
	}
	return c.doJSON(ctx, http.MethodPut, c.datasetPath("documents", documentID), body, "metadata", nil)
}

// StartParsing starts parsing for the exact derived document identifiers.
func (c *Client) StartParsing(ctx context.Context, documentIDs []string) error {
	if !validIdentifiers(documentIDs) {
		return errors.New("parse: invalid document identifiers")
	}
	return c.doJSON(ctx, http.MethodPost, c.datasetPath("chunks"), map[string]any{"document_ids": documentIDs}, "parse", nil)
}

// GetDocumentStatus returns the normalized state of one derived document.
func (c *Client) GetDocumentStatus(ctx context.Context, documentID string) (*DocumentStatus, error) {
	if !validIdentifier(documentID) {
		return nil, errors.New("status: invalid document identifier")
	}
	query := url.Values{"id": []string{documentID}}
	path := c.datasetPath("documents") + "?" + query.Encode()
	var envelope responseEnvelope
	if err := c.do(ctx, http.MethodGet, path, nil, "", "status", &envelope); err != nil {
		return nil, err
	}
	var data struct {
		Docs []struct {
			ID         string `json:"id"`
			Run        string `json:"run"`
			ChunkCount int64  `json:"chunk_count"`
		} `json:"docs"`
	}
	if err := json.Unmarshal(envelope.Data, &data); err != nil || len(data.Docs) != 1 || data.Docs[0].ID != documentID || data.Docs[0].ChunkCount < 0 {
		return nil, errors.New("status: invalid RAGFlow response")
	}
	state, ok := mapDocumentState(data.Docs[0].Run)
	if !ok {
		return nil, errors.New("status: unknown RAGFlow document state")
	}
	return &DocumentStatus{DocumentID: documentID, State: state, ChunkCount: data.Docs[0].ChunkCount}, nil
}

// DeleteDocuments removes only the explicitly listed derived documents.
func (c *Client) DeleteDocuments(ctx context.Context, documentIDs []string) error {
	if !validIdentifiers(documentIDs) {
		return errors.New("delete: invalid document identifiers")
	}
	return c.doJSON(ctx, http.MethodDelete, c.datasetPath("documents"), map[string]any{"ids": documentIDs}, "delete", nil)
}

// RetrievePublication calls the pinned RAGFlow retrieval endpoint with a
// metadata filter that is tied to one immutable FileBay publication. Callers
// must still validate every returned chunk against authoritative state.
func (c *Client) RetrievePublication(ctx context.Context, request RetrievalRequest) ([]RetrievedChunk, error) {
	question := strings.TrimSpace(request.Question)
	if question == "" || len(question) > 16*1024 || request.PublicationID <= 0 || request.PublicationGeneration <= 0 {
		return nil, errors.New("retrieve: invalid request")
	}
	topK := request.TopK
	if topK <= 0 {
		topK = 10
	}
	if topK > 50 {
		topK = 50
	}
	// RAGFlow's metadata-condition contract has changed across releases and
	// currently returns an empty result set for document-level meta fields.
	// Do not make authorization depend on that unstable upstream filter:
	// FileBay validates the exact RAGFlow document ID, active publication,
	// revision and caller permissions again before exposing every chunk.
	body := map[string]any{
		"question":    question,
		"dataset_ids": []string{c.datasetID},
		"page_size":   topK,
	}
	var envelope responseEnvelope
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/retrieval", body, "retrieve", &envelope); err != nil {
		return nil, err
	}
	var data struct {
		Chunks []RetrievedChunk `json:"chunks"`
	}
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return nil, errors.New("retrieve: invalid RAGFlow response")
	}
	if len(data.Chunks) > topK {
		return nil, errors.New("retrieve: RAGFlow response exceeds limit")
	}
	for _, chunk := range data.Chunks {
		if !validIdentifier(chunk.ID) || !validIdentifier(chunk.DocumentID) || len(chunk.Content) == 0 || len(chunk.Content) > 64*1024 {
			return nil, errors.New("retrieve: invalid RAGFlow chunk")
		}
	}
	return data.Chunks, nil
}

type responseEnvelope struct {
	Code int             `json:"code"`
	Data json.RawMessage `json:"data"`
}

func (c *Client) doJSON(ctx context.Context, method, path string, payload any, operation string, envelope *responseEnvelope) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("%s: encode request", operation)
	}
	return c.do(ctx, method, path, bytes.NewReader(body), "application/json", operation, envelope)
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader, contentType, operation string, result *responseEnvelope) error {
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("%s: build request", operation)
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("%s: RAGFlow transport failed", operation)
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, MaxResponseBytes+1)
	responseBody, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("%s: read RAGFlow response", operation)
	}
	if len(responseBody) > MaxResponseBytes {
		return fmt.Errorf("%s: RAGFlow response exceeds limit", operation)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("%s: RAGFlow HTTP status %d", operation, response.StatusCode)
	}

	envelope := responseEnvelope{}
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return fmt.Errorf("%s: invalid RAGFlow response", operation)
	}
	if envelope.Code != 0 {
		return fmt.Errorf("%s: RAGFlow business code %d", operation, envelope.Code)
	}
	if result != nil {
		*result = envelope
	}
	return nil
}

func (c *Client) datasetPath(segments ...string) string {
	path := "/api/v1/datasets/" + c.datasetID
	for _, segment := range segments {
		path += "/" + segment
	}
	return path
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isInternalDockerHost(host, configuredHost string) bool {
	configuredHost = strings.TrimSpace(configuredHost)
	return configuredHost != "" && strings.EqualFold(host, configuredHost) && !strings.ContainsAny(configuredHost, ".:/\\") && !strings.EqualFold(configuredHost, "localhost")
}

func validIdentifier(identifier string) bool {
	return safeIdentifier.MatchString(identifier)
}

func validIdentifiers(identifiers []string) bool {
	if len(identifiers) == 0 {
		return false
	}
	for _, identifier := range identifiers {
		if !validIdentifier(identifier) {
			return false
		}
	}
	return true
}

func safeFileName(fileName string) bool {
	return fileName != "" && len(fileName) <= 255 && fileName == filepath.Base(fileName) &&
		!strings.ContainsAny(fileName, "/\\:\x00") && strings.IndexFunc(fileName, unicode.IsControl) < 0 &&
		fileName == strings.TrimSpace(fileName)
}

func mapDocumentState(run string) (DocumentState, bool) {
	switch strings.ToUpper(run) {
	case "UNSTART":
		return DocumentStateUnstarted, true
	case "RUNNING":
		return DocumentStateRunning, true
	case "DONE":
		return DocumentStateDone, true
	case "FAIL", "CANCEL", "CANCELED":
		return DocumentStateFailed, true
	default:
		return "", false
	}
}

func validGovernanceMetadata(metadata GovernanceMetadata) bool {
	if metadata.SpaceID <= 0 || metadata.DocumentID <= 0 || metadata.RevisionID <= 0 || metadata.PublicationID <= 0 ||
		metadata.PublicationGeneration <= 0 || metadata.ACLPolicyVersion <= 0 || metadata.RevocationGeneration <= 0 ||
		len(metadata.RevisionSHA256) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(metadata.RevisionSHA256)
	return err == nil && len(decoded) == 32
}
