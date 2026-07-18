// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Package retrieval is FileBay's authoritative retrieval gateway. RAGFlow is
// treated as an untrusted derived index: every candidate is re-authorized from
// FileBay state before it can become a citation or Dify record.
package retrieval

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"code.gitea.io/gitea/models/db"
	knowledge_model "code.gitea.io/gitea/models/knowledge"
	perm_model "code.gitea.io/gitea/models/perm"
	access_model "code.gitea.io/gitea/models/perm/access"
	repo_model "code.gitea.io/gitea/models/repo"
	"code.gitea.io/gitea/models/unit"
	user_model "code.gitea.io/gitea/models/user"
	"code.gitea.io/gitea/modules/log"
	"code.gitea.io/gitea/modules/setting"
	"code.gitea.io/gitea/modules/timeutil"
	"code.gitea.io/gitea/modules/util"
	"code.gitea.io/gitea/services/knowledge/ragflow"
)

const (
	maxPublicationsPerRequest = 40
	maxCommentBytes           = 1024
)

var allowedFeedbackCategories = map[string]struct{}{
	"知识正确性": {}, "检索相关性": {}, "权限问题": {}, "使用体验": {},
}

// Citation is the safe, FileBay-authorized projection of one RAGFlow chunk.
type Citation struct {
	PublicationID int64
	DocumentID    int64
	RevisionID    int64
	Title         string
	Content       string
	Score         float64
}

// SearchResult contains only currently authorized citations.
type SearchResult struct {
	Citations []Citation
}

// CreateDifyBindingOptions defines a fixed-scope, application-level Dify grant.
type CreateDifyBindingOptions struct {
	ActorID          int64
	Name             string
	DifyKnowledgeID  string
	SpaceID          int64
	MaxSecurityLevel knowledge_model.SecurityLevel
}

// CreateDifyBinding generates a token once. Only its SHA-256 digest is kept.
func CreateDifyBinding(ctx context.Context, opts CreateDifyBindingOptions) (*knowledge_model.DifyBinding, string, error) {
	actor, err := user_model.GetUserByID(ctx, opts.ActorID)
	if err != nil || !actor.IsAdmin {
		return nil, "", errors.New("Dify binding requires a site administrator")
	}
	if !validBindingText(opts.Name) || !validBindingIdentifier(opts.DifyKnowledgeID) || !validDifySecurityLevel(opts.MaxSecurityLevel) {
		return nil, "", errors.New("invalid Dify binding input")
	}
	space, exists, err := db.GetByID[knowledge_model.Space](ctx, opts.SpaceID)
	if err != nil || !exists || space.Status != knowledge_model.SpaceStatusActive {
		return nil, "", errors.New("knowledge space is not active")
	}
	randomPart, err := util.CryptoRandomString(48)
	if err != nil {
		return nil, "", fmt.Errorf("generate Dify binding token: %w", err)
	}
	token := "dify_kb_" + randomPart
	digest := sha256.Sum256([]byte(token))
	binding := &knowledge_model.DifyBinding{
		Name:             strings.TrimSpace(opts.Name),
		DifyKnowledgeID:  strings.TrimSpace(opts.DifyKnowledgeID),
		SpaceID:          space.ID,
		MaxSecurityLevel: opts.MaxSecurityLevel,
		TokenSHA256:      hex.EncodeToString(digest[:]),
		TokenHint:        token[len(token)-6:],
		Enabled:          true,
		CreatedBy:        actor.ID,
	}
	if err := db.Insert(ctx, binding); err != nil {
		return nil, "", fmt.Errorf("create Dify binding: %w", err)
	}
	if err := appendAudit(ctx, actor.ID, binding.SpaceID, "knowledge.dify_binding.created", "dify_binding", binding.ID, "succeeded", "binding_created"); err != nil {
		return nil, "", err
	}
	return binding, token, nil
}

// LookupDifyBinding authenticates a Bearer credential without ever storing or
// returning the raw credential.
func LookupDifyBinding(ctx context.Context, token, knowledgeID string) (*knowledge_model.DifyBinding, error) {
	if !validBindingIdentifier(knowledgeID) || len(token) < 24 || len(token) > 128 {
		return nil, errors.New("invalid external knowledge credential")
	}
	digest := sha256.Sum256([]byte(token))
	want := hex.EncodeToString(digest[:])
	binding := new(knowledge_model.DifyBinding)
	found, err := db.GetEngine(ctx).Where("dify_knowledge_id = ? AND enabled = ?", knowledgeID, true).Get(binding)
	if err != nil || !found || subtle.ConstantTimeCompare([]byte(binding.TokenSHA256), []byte(want)) != 1 {
		return nil, errors.New("external knowledge credential rejected")
	}
	return binding, nil
}

// RetrieveForDify executes an application-scoped search. The Dify binding is
// intentionally not a FileBay user identity and cannot expand its own scope.
func RetrieveForDify(ctx context.Context, binding *knowledge_model.DifyBinding, question string, topK int) (*SearchResult, error) {
	if binding == nil || !binding.Enabled || binding.ID <= 0 {
		return nil, errors.New("Dify binding is disabled")
	}
	result, err := retrieveSpace(ctx, binding.SpaceID, binding.MaxSecurityLevel, question, topK, 0, binding.ID)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// RetrieveForUser searches spaces the signed-in FileBay account can read.
func RetrieveForUser(ctx context.Context, user *user_model.User, spaceID int64, question string, topK int) (*SearchResult, error) {
	if user == nil || user.ID <= 0 {
		return nil, errors.New("knowledge retrieval requires a signed-in user")
	}
	if spaceID > 0 {
		allowed, err := canReadSpace(ctx, user, spaceID)
		if err != nil {
			return nil, err
		}
		if !allowed {
			_ = recordRetrieval(ctx, user.ID, 0, spaceID, question, 0, "denied", "space_access")
			return nil, errors.New("knowledge space not found")
		}
		return retrieveSpace(ctx, spaceID, knowledge_model.SecurityLevelRestricted, question, topK, user.ID, 0)
	}
	var spaces []knowledge_model.Space
	if err := db.GetEngine(ctx).Where("status = ?", knowledge_model.SpaceStatusActive).Limit(maxPublicationsPerRequest).Find(&spaces); err != nil {
		return nil, fmt.Errorf("list knowledge spaces: %w", err)
	}
	combined := &SearchResult{}
	for _, space := range spaces {
		allowed, err := canReadSpace(ctx, user, space.ID)
		if err != nil || !allowed {
			continue
		}
		partial, err := retrieveSpace(ctx, space.ID, knowledge_model.SecurityLevelRestricted, question, topK, user.ID, 0)
		if err != nil {
			continue
		}
		combined.Citations = append(combined.Citations, partial.Citations...)
	}
	if len(combined.Citations) > clampTopK(topK) {
		combined.Citations = combined.Citations[:clampTopK(topK)]
	}
	return combined, nil
}

// RecordFeedback validates and stores structured, content-minimized feedback.
func RecordFeedback(ctx context.Context, actorID, spaceID, publicationID int64, category string, rating int, comment string) error {
	if actorID <= 0 || spaceID <= 0 || rating < 1 || rating > 5 || !validFeedbackComment(comment) {
		return errors.New("invalid knowledge feedback")
	}
	if _, ok := allowedFeedbackCategories[strings.TrimSpace(category)]; !ok {
		return errors.New("invalid knowledge feedback category")
	}
	return db.Insert(ctx, &knowledge_model.Feedback{ActorID: actorID, SpaceID: spaceID, PublicationID: publicationID, Category: category, Rating: rating, Comment: strings.TrimSpace(comment)})
}

func retrieveSpace(ctx context.Context, spaceID int64, maxSecurity knowledge_model.SecurityLevel, question string, topK int, actorID, bindingID int64) (*SearchResult, error) {
	question = strings.TrimSpace(question)
	if !validQuestion(question) {
		return nil, errors.New("invalid retrieval question")
	}
	if err := setting.ValidateKnowledgeSettings(); err != nil {
		return nil, err
	}
	space, exists, err := db.GetByID[knowledge_model.Space](ctx, spaceID)
	if err != nil || !exists || space.Status != knowledge_model.SpaceStatusActive {
		return nil, errors.New("knowledge space not found")
	}
	client, err := ragflow.NewClient(ragflow.Config{BaseURL: setting.Knowledge.RAGFlowBaseURL, APIKey: setting.Knowledge.RAGFlowAPIKey, DatasetID: setting.Knowledge.RAGFlowDatasetID, AllowLoopbackHTTP: setting.Knowledge.AllowLoopbackHTTP, AllowInternalHTTP: setting.Knowledge.AllowInternalHTTP, InternalHTTPHost: setting.Knowledge.InternalHTTPHost, Timeout: setting.Knowledge.HTTPTimeout, MaxUploadBytes: setting.Knowledge.MaxFileSize})
	if err != nil {
		return nil, err
	}
	publications, err := loadRetrievablePublications(ctx, space, maxSecurity)
	if err != nil {
		return nil, err
	}
	log.Info("Knowledge retrieval candidates for space %d: %d", spaceID, len(publications))
	result := &SearchResult{}
	limit := clampTopK(topK)
	for _, candidate := range publications {
		chunks, err := client.RetrievePublication(ctx, ragflow.RetrievalRequest{Question: question, PublicationID: candidate.publication.ID, PublicationGeneration: candidate.publication.Generation, TopK: limit})
		if err != nil {
			log.Warn("Knowledge retrieval failed for publication %d: %v", candidate.publication.ID, err)
			continue
		}
		log.Info("Knowledge retrieval returned %d chunks for publication %d", len(chunks), candidate.publication.ID)
		for _, chunk := range chunks {
			if chunk.DocumentID != candidate.binding.EngineDocumentID || !candidateStillPublished(ctx, candidate) {
				continue
			}
			result.Citations = append(result.Citations, Citation{PublicationID: candidate.publication.ID, DocumentID: candidate.document.ID, RevisionID: candidate.revision.ID, Title: candidate.document.Title, Content: chunk.Content, Score: chunk.Similarity})
			if len(result.Citations) == limit {
				break
			}
		}
		if len(result.Citations) == limit {
			break
		}
	}
	outcome, reason := "succeeded", "results"
	if len(result.Citations) == 0 {
		outcome, reason = "empty", "no_authorized_citation"
	}
	_ = recordRetrieval(ctx, actorID, bindingID, spaceID, question, len(result.Citations), outcome, reason)
	return result, nil
}

type retrievablePublication struct {
	publication *knowledge_model.Publication
	document    *knowledge_model.Document
	revision    *knowledge_model.Revision
	binding     *knowledge_model.IndexBinding
	source      *knowledge_model.DataSource
	space       *knowledge_model.Space
}

func loadRetrievablePublications(ctx context.Context, space *knowledge_model.Space, maxSecurity knowledge_model.SecurityLevel) ([]retrievablePublication, error) {
	var publications []knowledge_model.Publication
	if err := db.GetEngine(ctx).Where("space_id = ? AND is_current = ?", space.ID, true).Limit(maxPublicationsPerRequest).Find(&publications); err != nil {
		return nil, fmt.Errorf("load knowledge publications: %w", err)
	}
	now := timeutil.TimeStamp(time.Now().Unix())
	items := make([]retrievablePublication, 0, len(publications))
	for i := range publications {
		publication := &publications[i]
		document, exists, err := db.GetByID[knowledge_model.Document](ctx, publication.DocumentID)
		if err != nil || !exists || !publication.IsPublished(now, document.CurrentPublicationID, space.RevocationGeneration) {
			continue
		}
		revision, exists, err := db.GetByID[knowledge_model.Revision](ctx, publication.RevisionID)
		if err != nil || !exists || revision.DocumentID != document.ID || !securityAllowed(revision.SecurityLevel, maxSecurity) {
			continue
		}
		source, exists, err := db.GetByID[knowledge_model.DataSource](ctx, document.DataSourceID)
		if err != nil || !exists || source.Status != knowledge_model.DataSourceStatusEnabled || source.SecurityLevel == knowledge_model.SecurityLevelProhibited || (source.ExpiresUnix > 0 && now >= source.ExpiresUnix) {
			continue
		}
		binding := new(knowledge_model.IndexBinding)
		// The publication state is authoritative. Earlier trial builds recorded
		// an evaluated binding before activation but did not mirror the binding
		// status during activation. Accept that durable predecessor state while
		// the publication itself is current and searchable.
		found, err := db.GetEngine(ctx).
			Where("publication_id = ? AND engine_profile_version = ?", publication.ID, setting.Knowledge.EngineProfileVersion).
			In("status", knowledge_model.IndexStatusSearchable, knowledge_model.IndexStatusEvaluation).
			Get(binding)
		if err != nil || !found || binding.DatasetID != setting.Knowledge.RAGFlowDatasetID {
			continue
		}
		items = append(items, retrievablePublication{publication: publication, document: document, revision: revision, binding: binding, source: source, space: space})
	}
	return items, nil
}

func candidateStillPublished(ctx context.Context, candidate retrievablePublication) bool {
	publication, exists, err := db.GetByID[knowledge_model.Publication](ctx, candidate.publication.ID)
	if err != nil || !exists {
		return false
	}
	document, exists, err := db.GetByID[knowledge_model.Document](ctx, candidate.document.ID)
	if err != nil || !exists {
		return false
	}
	space, exists, err := db.GetByID[knowledge_model.Space](ctx, candidate.space.ID)
	if err != nil || !exists {
		return false
	}
	source, exists, err := db.GetByID[knowledge_model.DataSource](ctx, candidate.source.ID)
	if err != nil || !exists || source.Status != knowledge_model.DataSourceStatusEnabled ||
		source.SecurityLevel == knowledge_model.SecurityLevelProhibited ||
		(source.ExpiresUnix > 0 && timeutil.TimeStampNow() >= source.ExpiresUnix) {
		return false
	}
	return publication.IsPublished(timeutil.TimeStamp(time.Now().Unix()), document.CurrentPublicationID, space.RevocationGeneration)
}

func canReadSpace(ctx context.Context, user *user_model.User, spaceID int64) (bool, error) {
	space, exists, err := db.GetByID[knowledge_model.Space](ctx, spaceID)
	if err != nil || !exists || space.Status != knowledge_model.SpaceStatusActive {
		return false, err
	}
	if user.IsAdmin || user.ID == space.OwnerID {
		return true, nil
	}
	repo, err := repo_model.GetRepositoryByID(ctx, space.RepoID)
	if err != nil {
		return false, err
	}
	permission, err := access_model.GetUserRepoPermission(ctx, repo, user)
	return err == nil && permission.CanAccess(perm_model.AccessModeRead, unit.TypeCode), err
}

func securityAllowed(actual, maximum knowledge_model.SecurityLevel) bool {
	levels := map[knowledge_model.SecurityLevel]int{knowledge_model.SecurityLevelPublic: 1, knowledge_model.SecurityLevelInternal: 2, knowledge_model.SecurityLevelRestricted: 3, knowledge_model.SecurityLevelProhibited: 4}
	return actual != knowledge_model.SecurityLevelProhibited && levels[actual] > 0 && levels[actual] <= levels[maximum]
}

func clampTopK(value int) int {
	maxTopK := setting.Knowledge.RetrieveMaxTopK
	if maxTopK <= 0 {
		maxTopK = 10
	}
	if value <= 0 {
		return maxTopK
	}
	if value > maxTopK {
		return maxTopK
	}
	return value
}

func validQuestion(value string) bool {
	maxBytes := setting.Knowledge.RetrieveQueryMaxByte
	if maxBytes <= 0 {
		maxBytes = 2048
	}
	return value != "" && len(value) <= maxBytes && strings.IndexFunc(value, unicode.IsControl) < 0
}

func validBindingText(value string) bool {
	return value == strings.TrimSpace(value) && value != "" && len(value) <= 255 && strings.IndexFunc(value, unicode.IsControl) < 0
}

func validBindingIdentifier(value string) bool {
	if !validBindingText(value) || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.') {
			return false
		}
	}
	return true
}

func validDifySecurityLevel(level knowledge_model.SecurityLevel) bool {
	return level == knowledge_model.SecurityLevelPublic || level == knowledge_model.SecurityLevelInternal || level == knowledge_model.SecurityLevelRestricted
}

func validFeedbackComment(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) <= maxCommentBytes && strings.IndexFunc(value, unicode.IsControl) < 0 && !strings.Contains(strings.ToLower(value), "password")
}

func recordRetrieval(ctx context.Context, actorID, bindingID, spaceID int64, question string, count int, outcome, reason string) error {
	digest := sha256.Sum256([]byte(question))
	return db.Insert(ctx, &knowledge_model.RetrievalEvent{ActorID: actorID, BindingID: bindingID, SpaceID: spaceID, QuerySHA256: hex.EncodeToString(digest[:]), ResultCount: count, Outcome: outcome, ReasonCode: reason})
}

func appendAudit(ctx context.Context, actorID, spaceID int64, action, entityType string, entityID int64, result, reason string) error {
	return db.Insert(ctx, &knowledge_model.AuditEvent{ActorID: actorID, Action: action, EntityType: entityType, EntityID: entityID, SpaceID: spaceID, Result: result, ReasonCode: reason, TraceID: fmt.Sprintf("retrieval-%d-%d", entityID, time.Now().UnixNano()), MetadataJSON: "{}"})
}
