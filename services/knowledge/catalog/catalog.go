// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Package catalog owns the production entry points that turn an explicitly
// selected, already-masked user artifact into governed FileBay knowledge state.
package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"code.gitea.io/gitea/models/db"
	knowledge_model "code.gitea.io/gitea/models/knowledge"
	repo_model "code.gitea.io/gitea/models/repo"
	user_model "code.gitea.io/gitea/models/user"
	"code.gitea.io/gitea/modules/setting"
	"code.gitea.io/gitea/modules/timeutil"
	"code.gitea.io/gitea/services/knowledge/indexer"
	"code.gitea.io/gitea/services/knowledge/ingest"
	"code.gitea.io/gitea/services/knowledge/ragflow"
	repo_service "code.gitea.io/gitea/services/repository"
	files_service "code.gitea.io/gitea/services/repository/files"
)

const (
	defaultBranchName       = "main"
	defaultACLPolicyVersion = 4
)

// RequiredReviewTypes are the six mandatory enterprise knowledge checks.
// Values are stable identifiers; Chinese labels belong to the web layer.
var RequiredReviewTypes = []string{"source", "content", "permission", "sensitive", "format", "release"}

var slugInvalid = regexp.MustCompile(`[^a-z0-9._-]+`)

// ErrSpaceAlreadyExists indicates that a user has already created a knowledge
// space with this display name. Callers should guide the user to the workspace
// register instead of exposing the internal repository name.
var ErrSpaceAlreadyExists = errors.New("knowledge space already exists")

const bindingReconciliationBatchSize = 20

// CreateSpaceOptions defines one governed knowledge space request.
type CreateSpaceOptions struct {
	OwnerID     int64
	ActorID     int64
	Name        string
	Description string
}

// UploadRevisionOptions contains a single already-masked artifact and its
// client declaration. The server never accepts or stores a client local path.
type UploadRevisionOptions struct {
	// DocumentID is optional. A positive value appends an immutable revision to
	// an existing logical document in the same governed space.
	DocumentID   int64
	SpaceID      int64
	DataSourceID int64
	ActorID      int64
	Title        string
	VersionNo    string
	Relation     string
	FileName     string
	Content      io.Reader
	Manifest     ingest.UploadManifest
}

// UploadRevisionResult returns the governance records created for an upload.
type UploadRevisionResult struct {
	Document *knowledge_model.Document
	Revision *knowledge_model.Revision
	Approval *knowledge_model.Approval
}

// CreateSpace creates a private FileBay repository and binds it to a knowledge
// governance space.
func CreateSpace(ctx context.Context, opts CreateSpaceOptions) (*knowledge_model.Space, error) {
	name := strings.TrimSpace(opts.Name)
	if opts.OwnerID <= 0 || opts.ActorID <= 0 || !validDisplayText(name, false) || !validDisplayText(opts.Description, true) {
		return nil, errors.New("invalid knowledge space input")
	}
	owner, err := user_model.GetUserByID(ctx, opts.OwnerID)
	if err != nil {
		return nil, fmt.Errorf("load knowledge owner: %w", err)
	}
	actor, err := user_model.GetUserByID(ctx, opts.ActorID)
	if err != nil {
		return nil, fmt.Errorf("load knowledge actor: %w", err)
	}
	slug := makeSlug(name)
	existingSpace := new(knowledge_model.Space)
	// The display name is the business identity. Checking it as well as the
	// internal slug also catches trial spaces created by the older slug rule.
	found, err := db.GetEngine(ctx).Where("owner_id = ? AND (name = ? OR slug = ?)", owner.ID, name, slug).Get(existingSpace)
	if err != nil {
		return nil, fmt.Errorf("find existing knowledge space: %w", err)
	}
	if found {
		return nil, ErrSpaceAlreadyExists
	}
	repoName := "kb-" + slug
	if err := repo_model.IsUsableRepoName(repoName); err != nil {
		return nil, fmt.Errorf("invalid knowledge repository name: %w", err)
	}
	repo, err := repo_service.CreateRepository(ctx, actor, owner, repo_service.CreateRepoOptions{
		Name:          repoName,
		Description:   "由企业知识库管理：" + name,
		IsPrivate:     true,
		AutoInit:      true,
		Readme:        "Default",
		DefaultBranch: defaultBranchName,
	})
	if err != nil {
		return nil, fmt.Errorf("create knowledge repository: %w", err)
	}

	space := &knowledge_model.Space{
		OwnerID:              owner.ID,
		RepoID:               repo.ID,
		Name:                 name,
		Slug:                 slug,
		Description:          strings.TrimSpace(opts.Description),
		Status:               knowledge_model.SpaceStatusActive,
		RevocationGeneration: 1,
		CreatedBy:            actor.ID,
	}
	if err := db.Insert(ctx, space); err != nil {
		return nil, fmt.Errorf("create knowledge space: %w", err)
	}
	return space, nil
}

// DeleteEmptySpace removes an unused knowledge-space trial record together
// with its private implementation repository. Spaces with any governance or
// retrieval record are deliberately rejected and must follow the lifecycle
// archive and destruction process instead.
func DeleteEmptySpace(ctx context.Context, spaceID, actorID int64) error {
	if spaceID <= 0 || actorID <= 0 {
		return errors.New("invalid knowledge space deletion request")
	}
	space, exists, err := db.GetByID[knowledge_model.Space](ctx, spaceID)
	if err != nil || !exists {
		return errors.New("knowledge space is not available")
	}
	actor, err := user_model.GetUserByID(ctx, actorID)
	if err != nil {
		return fmt.Errorf("load knowledge deletion actor: %w", err)
	}
	if !actor.IsAdmin && actor.ID != space.OwnerID {
		return errors.New("knowledge space deletion is not authorized")
	}

	for _, record := range []any{
		&knowledge_model.DataSource{SpaceID: space.ID},
		&knowledge_model.Document{SpaceID: space.ID},
		&knowledge_model.ACL{SpaceID: space.ID},
		&knowledge_model.Publication{SpaceID: space.ID},
		&knowledge_model.DifyBinding{SpaceID: space.ID},
		&knowledge_model.RetrievalEvent{SpaceID: space.ID},
		&knowledge_model.Feedback{SpaceID: space.ID},
		&knowledge_model.AuditEvent{SpaceID: space.ID},
		&knowledge_model.IndexBinding{SpaceID: space.ID},
		&knowledge_model.RevocationTombstone{SpaceID: space.ID},
	} {
		count, err := db.GetEngine(ctx).Count(record)
		if err != nil {
			return fmt.Errorf("check knowledge space deletion state: %w", err)
		}
		if count > 0 {
			return errors.New("only an empty knowledge space can be deleted")
		}
	}

	repo, err := repo_model.GetRepositoryByID(ctx, space.RepoID)
	if err != nil {
		return fmt.Errorf("load knowledge repository for deletion: %w", err)
	}
	if err := repo_service.DeleteRepository(ctx, actor, repo, false); err != nil {
		return fmt.Errorf("delete empty knowledge repository: %w", err)
	}
	if _, err := db.GetEngine(ctx).ID(space.ID).Delete(new(knowledge_model.Space)); err != nil {
		return fmt.Errorf("delete empty knowledge space: %w", err)
	}
	return nil
}

// UploadRevision validates and stores an explicitly masked artifact in the
// space repository, then creates a pending approval for a second reviewer.
func UploadRevision(ctx context.Context, opts UploadRevisionOptions) (*UploadRevisionResult, error) {
	if opts.DocumentID < 0 || opts.SpaceID <= 0 || opts.DataSourceID <= 0 || opts.ActorID <= 0 || opts.Content == nil ||
		!validDisplayText(opts.Title, false) || !validDocumentVersion(opts.VersionNo) || !validRevisionRelation(opts.Relation) {
		return nil, errors.New("invalid knowledge upload input")
	}
	space, exists, err := db.GetByID[knowledge_model.Space](ctx, opts.SpaceID)
	if err != nil {
		return nil, fmt.Errorf("load knowledge space: %w", err)
	}
	if !exists || space.Status != knowledge_model.SpaceStatusActive {
		return nil, errors.New("knowledge space is not active")
	}
	source, exists, err := db.GetByID[knowledge_model.DataSource](ctx, opts.DataSourceID)
	if err != nil {
		return nil, fmt.Errorf("load knowledge data source: %w", err)
	}
	if !exists || source.SpaceID != space.ID || source.Status != knowledge_model.DataSourceStatusEnabled ||
		source.SecurityLevel == knowledge_model.SecurityLevelProhibited {
		return nil, errors.New("knowledge data source is not enabled for upload")
	}
	if source.ExpiresUnix > 0 && source.ExpiresUnix <= timeutil.TimeStamp(time.Now().Unix()) {
		return nil, errors.New("knowledge data source has expired")
	}
	if source.SearchabilityStatus != knowledge_model.SearchabilityStatusSearchable {
		return nil, errors.New("knowledge data source must complete OCR or searchability review before upload")
	}
	actor, err := user_model.GetUserByID(ctx, opts.ActorID)
	if err != nil {
		return nil, fmt.Errorf("load knowledge actor: %w", err)
	}
	if actor.ID != space.OwnerID && !actor.IsAdmin {
		return nil, errors.New("knowledge upload is not authorized")
	}
	repo, err := repo_model.GetRepositoryByID(ctx, space.RepoID)
	if err != nil {
		return nil, fmt.Errorf("load knowledge repository: %w", err)
	}
	repo.Owner = actor
	var existingDocument *knowledge_model.Document
	if opts.DocumentID > 0 {
		document, exists, err := db.GetByID[knowledge_model.Document](ctx, opts.DocumentID)
		if err != nil {
			return nil, fmt.Errorf("load knowledge document: %w", err)
		}
		if !exists || document.SpaceID != space.ID || document.DataSourceID != source.ID {
			return nil, errors.New("knowledge revision document is outside the selected data source")
		}
		existingDocument = document
	}

	maxBytes := setting.Knowledge.MaxFileSize
	if maxBytes <= 0 {
		maxBytes = ingest.DefaultUploadPolicy().MaxFileBytes
	}
	content, err := io.ReadAll(io.LimitReader(opts.Content, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read knowledge upload: %w", err)
	}
	if int64(len(content)) > maxBytes {
		return nil, ingest.ErrFileSize
	}
	sum := sha256.Sum256(content)
	contentSHA := hex.EncodeToString(sum[:])
	reader := bytes.NewReader(content)
	detectedMIME, err := ingest.DetectArtifactMIME(opts.FileName, reader, int64(len(content)))
	if err != nil {
		return nil, err
	}
	manifest := opts.Manifest
	// Repository-selected artifacts are already read from a fixed FileBay
	// revision, so the server can safely derive their MIME type from the same
	// signature check used for local masked uploads. This keeps both entry
	// points on one acceptance policy and never trusts a browser-supplied type.
	if strings.TrimSpace(manifest.MIMEType) == "" {
		manifest.MIMEType = detectedMIME
	}
	// The governed data source is the authority for masking policy. The web
	// workbench intentionally omits this implementation detail from the normal
	// upload flow, while API callers may still explicitly provide it.
	if strings.TrimSpace(manifest.MaskPolicyVersion) == "" {
		manifest.MaskPolicyVersion = source.MaskPolicyVersion
	}
	if len(bytes.TrimSpace(manifest.MaskManifestJSON)) == 0 {
		manifest.MaskManifestJSON = []byte(`{"schema_version":1,"masked":true,"findings_count":0,"categories":[]}`)
	}
	if source.MaskPolicyVersion != manifest.MaskPolicyVersion {
		return nil, errors.New("masked artifact policy does not match the governed data source")
	}
	manifest.FileName = opts.FileName
	manifest.SourceAuthorization = source.SourceAuthorizationRef
	manifest.ContentSHA256 = contentSHA
	manifest.Size = int64(len(content))
	facts := ingest.UploadFacts{
		ContentSHA256:    contentSHA,
		Size:             int64(len(content)),
		DetectedMIMEType: detectedMIME,
	}
	policy := ingest.DefaultUploadPolicy()
	policy.MaxFileBytes = maxBytes
	if err := ingest.ValidateUploadManifest(manifest, facts, policy); err != nil {
		return nil, err
	}

	repoPath := buildRevisionRepoPath(time.Now(), contentSHA, opts.FileName)
	response, err := files_service.ChangeRepoFiles(ctx, repo, actor, &files_service.ChangeRepoFilesOptions{
		OldBranch: repo.DefaultBranch,
		NewBranch: repo.DefaultBranch,
		Message:   "knowledge: upload masked artifact " + opts.FileName,
		Files: []*files_service.ChangeRepoFile{{
			Operation:     "upload",
			TreePath:      repoPath,
			ContentReader: bytes.NewReader(content),
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("commit knowledge revision: %w", err)
	}
	if response == nil || response.Commit == nil || strings.TrimSpace(response.Commit.SHA) == "" {
		return nil, errors.New("knowledge revision commit missing")
	}

	result, err := db.WithTx2(ctx, func(txCtx context.Context) (*UploadRevisionResult, error) {
		document := existingDocument
		if document == nil {
			document = &knowledge_model.Document{
				SpaceID:          space.ID,
				DataSourceID:     source.ID,
				Title:            strings.TrimSpace(opts.Title),
				RepoPath:         repoPath,
				MIMEType:         detectedMIME,
				GovernanceStatus: knowledge_model.GovernanceStatusPending,
				CreatedBy:        actor.ID,
			}
			if err := db.Insert(txCtx, document); err != nil {
				return nil, fmt.Errorf("create knowledge document: %w", err)
			}
		} else {
			document.GovernanceStatus = knowledge_model.GovernanceStatusPending
			if _, err := db.GetEngine(txCtx).ID(document.ID).Cols("governance_status").Update(document); err != nil {
				return nil, fmt.Errorf("mark knowledge document pending revision: %w", err)
			}
		}

		revisionNo := int64(1)
		if existingDocument != nil {
			latest := new(knowledge_model.Revision)
			found, err := db.GetEngine(txCtx).Where("document_id = ?", document.ID).Desc("revision_no").Get(latest)
			if err != nil {
				return nil, fmt.Errorf("load latest knowledge revision: %w", err)
			}
			if found {
				revisionNo = latest.RevisionNo + 1
			}
		}
		revision := &knowledge_model.Revision{
			DocumentID:          document.ID,
			RevisionNo:          revisionNo,
			FileName:            opts.FileName,
			RepoPath:            repoPath,
			ContentSHA256:       contentSHA,
			Size:                int64(len(content)),
			GitCommitSHA:        response.Commit.SHA,
			MaskPolicyVersion:   manifest.MaskPolicyVersion,
			MaskManifestJSON:    string(manifest.MaskManifestJSON),
			SourceAuthorization: manifest.SourceAuthorization,
			BusinessDomainCode:  source.BusinessDomainCode,
			ContentTypeCode:     source.ContentTypeCode,
			SecurityLevel:       source.SecurityLevel,
			ApplicableScopeJSON: source.ApplicableScopeJSON,
			VersionNo:           strings.TrimSpace(opts.VersionNo),
			RevisionRelation:    strings.TrimSpace(opts.Relation),
			SourceDepartment:    source.SourceDepartment,
			ContentOwnerID:      source.ContentOwnerID,
			MaintenanceOwnerID:  source.MaintenanceOwnerID,
			ApprovalBasisRef:    source.ApprovalBasisRef,
			EffectiveUnix:       source.EffectiveUnix,
			ExpiresUnix:         source.ExpiresUnix,
			SearchabilityStatus: source.SearchabilityStatus,
			CreatedBy:           actor.ID,
		}
		if err := db.Insert(txCtx, revision); err != nil {
			return nil, fmt.Errorf("create knowledge revision: %w", err)
		}
		document.CurrentRevisionID = revision.ID
		if _, err := db.GetEngine(txCtx).ID(document.ID).Cols("current_revision_id").Update(document); err != nil {
			return nil, fmt.Errorf("link knowledge current revision: %w", err)
		}
		approval := &knowledge_model.Approval{
			RevisionID:  revision.ID,
			Status:      knowledge_model.ApprovalStatusPending,
			RequestedBy: actor.ID,
		}
		if err := db.Insert(txCtx, approval); err != nil {
			return nil, fmt.Errorf("create knowledge approval: %w", err)
		}
		for _, reviewType := range RequiredReviewTypes {
			if err := db.Insert(txCtx, &knowledge_model.ReviewCheck{RevisionID: revision.ID, ReviewType: reviewType, Status: "pending"}); err != nil {
				return nil, fmt.Errorf("create knowledge review check: %w", err)
			}
		}
		return &UploadRevisionResult{Document: document, Revision: revision, Approval: approval}, nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Approve approves a pending revision using the production model transaction.
func Approve(ctx context.Context, approvalID, actorID int64, comment string) (*knowledge_model.ApproveRevisionResult, error) {
	actor, err := user_model.GetUserByID(ctx, actorID)
	if err != nil {
		return nil, fmt.Errorf("load knowledge approver: %w", err)
	}
	if !actor.IsAdmin {
		return nil, errors.New("knowledge approval requires an administrator")
	}
	approval, exists, err := db.GetByID[knowledge_model.Approval](ctx, approvalID)
	if err != nil || !exists || approval.RequestedBy == actorID {
		return nil, errors.New("knowledge approval is not available")
	}
	if err := requireAllReviewsPassed(ctx, approval.RevisionID); err != nil {
		return nil, err
	}
	return knowledge_model.ApproveRevision(ctx, knowledge_model.ApproveRevisionOptions{
		ApprovalID:           approvalID,
		DecidedBy:            actorID,
		Comment:              strings.TrimSpace(comment),
		EngineProfileVersion: setting.Knowledge.EngineProfileVersion,
		ACLPolicyVersion:     defaultACLPolicyVersion,
		TraceID:              fmt.Sprintf("web-approve-%d-%d", approvalID, time.Now().UnixNano()),
	})
}

// Reject records a terminal reviewer decision. The submitter cannot reject
// their own revision, matching the separation of duties used for approval.
func Reject(ctx context.Context, approvalID, actorID int64, comment string) error {
	actor, err := user_model.GetUserByID(ctx, actorID)
	if err != nil || !actor.IsAdmin {
		return errors.New("knowledge rejection requires an administrator")
	}
	approval, exists, err := db.GetByID[knowledge_model.Approval](ctx, approvalID)
	if err != nil || !exists || approval.RequestedBy == actorID {
		return errors.New("knowledge rejection is not available")
	}
	return knowledge_model.RejectRevision(ctx, knowledge_model.RejectRevisionOptions{
		ApprovalID: approvalID,
		DecidedBy:  actorID,
		Comment:    strings.TrimSpace(comment),
		TraceID:    fmt.Sprintf("web-reject-%d-%d", approvalID, time.Now().UnixNano()),
	})
}

// CompleteReview records one of the six required checks. The submitter cannot
// self-review, and only site administrators can complete trial review checks.
func CompleteReview(ctx context.Context, revisionID, actorID int64, reviewType, comment string) error {
	if revisionID <= 0 || !isRequiredReviewType(reviewType) || !validDisplayText(comment, true) {
		return errors.New("invalid knowledge review")
	}
	actor, err := user_model.GetUserByID(ctx, actorID)
	if err != nil || !actor.IsAdmin {
		return errors.New("knowledge review requires an administrator")
	}
	approval := new(knowledge_model.Approval)
	found, err := db.GetEngine(ctx).Where("revision_id = ?", revisionID).Get(approval)
	if err != nil || !found || approval.Status != knowledge_model.ApprovalStatusPending || approval.RequestedBy == actorID {
		return errors.New("knowledge review is not available")
	}
	check := new(knowledge_model.ReviewCheck)
	found, err = db.GetEngine(ctx).Where("revision_id = ? AND review_type = ?", revisionID, reviewType).Get(check)
	if err != nil || !found || check.Status != "pending" {
		return errors.New("knowledge review has already been completed")
	}
	check.Status = "passed"
	check.Comment = strings.TrimSpace(comment)
	check.CheckedBy = actorID
	check.CheckedUnix = timeutil.TimeStampNow()
	updated, err := db.GetEngine(ctx).Where("id = ? AND status = ?", check.ID, "pending").Cols("status", "comment", "checked_by", "checked_unix").Update(check)
	if err != nil || updated != 1 {
		return errors.New("knowledge review update conflict")
	}
	revision, exists, err := db.GetByID[knowledge_model.Revision](ctx, revisionID)
	if err == nil && exists {
		document, ok, loadErr := db.GetByID[knowledge_model.Document](ctx, revision.DocumentID)
		if loadErr == nil && ok {
			_ = db.Insert(ctx, &knowledge_model.AuditEvent{ActorID: actorID, Action: "knowledge.review.passed", EntityType: "knowledge_revision", EntityID: revisionID, SpaceID: document.SpaceID, Result: "succeeded", ReasonCode: reviewType, TraceID: fmt.Sprintf("review-%d-%s-%d", revisionID, reviewType, time.Now().UnixNano()), MetadataJSON: "{}"})
		}
	}
	return nil
}

func requireAllReviewsPassed(ctx context.Context, revisionID int64) error {
	var checks []knowledge_model.ReviewCheck
	if err := db.GetEngine(ctx).Where("revision_id = ?", revisionID).Find(&checks); err != nil {
		return fmt.Errorf("load knowledge reviews: %w", err)
	}
	if len(checks) != len(RequiredReviewTypes) {
		return errors.New("knowledge review checklist is incomplete")
	}
	passed := make(map[string]bool, len(checks))
	for _, check := range checks {
		if check.Status == "passed" {
			passed[check.ReviewType] = true
		}
	}
	for _, reviewType := range RequiredReviewTypes {
		if !passed[reviewType] {
			return errors.New("all six knowledge reviews must pass before approval")
		}
	}
	return nil
}

func isRequiredReviewType(value string) bool {
	for _, reviewType := range RequiredReviewTypes {
		if value == reviewType {
			return true
		}
	}
	return false
}

// ProcessIndexJob runs one durable RAGFlow index job using the configured
// production engine binding.
func ProcessIndexJob(ctx context.Context, jobID, actorID int64) error {
	actor, err := user_model.GetUserByID(ctx, actorID)
	if err != nil {
		return fmt.Errorf("load knowledge job actor: %w", err)
	}
	if !actor.IsAdmin {
		return errors.New("knowledge index job processing requires an administrator")
	}
	return ProcessIndexJobSystem(ctx, jobID)
}

// ProcessIndexJobSystem runs a queued knowledge index operation from FileBay's
// own trusted background worker. It deliberately has no HTTP entry point: user
// initiated processing must continue to go through ProcessIndexJob above.
func ProcessIndexJobSystem(ctx context.Context, jobID int64) error {
	if err := setting.ValidateKnowledgeSettings(); err != nil {
		return err
	}
	engine, err := ragflow.NewClient(ragflow.Config{
		BaseURL:           setting.Knowledge.RAGFlowBaseURL,
		APIKey:            setting.Knowledge.RAGFlowAPIKey,
		DatasetID:         setting.Knowledge.RAGFlowDatasetID,
		AllowLoopbackHTTP: setting.Knowledge.AllowLoopbackHTTP,
		AllowInternalHTTP: setting.Knowledge.AllowInternalHTTP,
		InternalHTTPHost:  setting.Knowledge.InternalHTTPHost,
		Timeout:           setting.Knowledge.HTTPTimeout,
		MaxUploadBytes:    setting.Knowledge.MaxFileSize,
	})
	if err != nil {
		return err
	}
	processor, err := indexer.NewProcessor(indexer.Config{
		Source:     indexer.GitSourceProvider{},
		Engine:     engine,
		EngineName: "ragflow",
		DatasetID:  setting.Knowledge.RAGFlowDatasetID,
	})
	if err != nil {
		return err
	}
	return processor.Process(ctx, jobID)
}

// RetryPublicationIndex returns every failed job for one approved publication
// to the durable queue after its engine configuration has been corrected.
func RetryPublicationIndex(ctx context.Context, publicationID, actorID int64) error {
	actor, err := user_model.GetUserByID(ctx, actorID)
	if err != nil || !actor.IsAdmin {
		return errors.New("knowledge index retry requires an administrator")
	}
	publication, exists, err := db.GetByID[knowledge_model.Publication](ctx, publicationID)
	if err != nil || !exists {
		return errors.New("knowledge publication not found")
	}
	if publication.GovernanceStatus != knowledge_model.GovernanceStatusApproved || publication.ValidityStatus != knowledge_model.ValidityStatusScheduled || publication.IndexStatus != knowledge_model.IndexStatusFailed {
		return errors.New("knowledge publication is not eligible for reindex")
	}
	return db.WithTx(ctx, func(txCtx context.Context) error {
		var jobs []knowledge_model.IndexJob
		if err := db.GetEngine(txCtx).Where("publication_id = ? AND job_type IN (?, ?)", publicationID, knowledge_model.IndexJobTypeUpsert, knowledge_model.IndexJobTypePoll).Find(&jobs); err != nil {
			return fmt.Errorf("list retryable knowledge index jobs: %w", err)
		}
		if len(jobs) == 0 {
			return errors.New("no retryable knowledge index jobs found")
		}
		for _, job := range jobs {
			job.Status = knowledge_model.IndexJobStatusRetry
			job.Attempt = 0
			job.LastError = ""
			if _, err := db.GetEngine(txCtx).ID(job.ID).Cols("status", "attempt", "last_error").Update(&job); err != nil {
				return fmt.Errorf("requeue knowledge index job: %w", err)
			}
		}
		publication.IndexStatus = knowledge_model.IndexStatusQueued
		if _, err := db.GetEngine(txCtx).ID(publication.ID).Cols("index_status").Update(publication); err != nil {
			return fmt.Errorf("requeue knowledge publication: %w", err)
		}
		return nil
	})
}

// ActivatePublication makes a successfully evaluated publication searchable.
func ActivatePublication(ctx context.Context, publicationID, actorID int64) error {
	actor, err := user_model.GetUserByID(ctx, actorID)
	if err != nil || !actor.IsAdmin {
		return errors.New("knowledge publication activation requires an administrator")
	}
	publication, exists, err := db.GetByID[knowledge_model.Publication](ctx, publicationID)
	if err != nil || !exists {
		return errors.New("knowledge publication not found")
	}
	document, exists, err := db.GetByID[knowledge_model.Document](ctx, publication.DocumentID)
	if err != nil || !exists {
		return errors.New("knowledge document not found")
	}
	source, exists, err := db.GetByID[knowledge_model.DataSource](ctx, document.DataSourceID)
	if err != nil || !exists || source.Status != knowledge_model.DataSourceStatusEnabled || source.SecurityLevel == knowledge_model.SecurityLevelProhibited || (source.ExpiresUnix > 0 && timeutil.TimeStampNow() >= source.ExpiresUnix) {
		return errors.New("knowledge data source is not eligible for publication")
	}
	if err := knowledge_model.ActivatePublication(ctx, knowledge_model.ActivatePublicationOptions{PublicationID: publication.ID, ActorID: actorID, ExpectedPublicationGeneration: publication.Generation, ExpectedRevocationGeneration: publication.RevocationGeneration, EngineProfileVersion: setting.Knowledge.EngineProfileVersion, TraceID: fmt.Sprintf("web-activate-%d-%d", publication.ID, time.Now().UnixNano())}); err != nil {
		return err
	}
	return nil
}

// ReconcileSearchableBindingsSystem repairs a legacy activation drift where an
// already-current FileBay publication was made searchable but its exact
// RAGFlow binding stayed at evaluation. It never promotes a non-current,
// revoked, expired, prohibited, or profile/dataset-mismatched record.
//
// New activations cannot create this condition because ActivatePublication now
// moves both records in one transaction. This bounded reconciliation exists
// only to safely recover trial data written by the earlier split operation.
func ReconcileSearchableBindingsSystem(ctx context.Context) (int, error) {
	if err := setting.ValidateKnowledgeSettings(); err != nil {
		return 0, err
	}
	var publications []knowledge_model.Publication
	if err := db.GetEngine(ctx).
		Where("is_current = ? AND validity_status = ? AND index_status = ?", true, knowledge_model.ValidityStatusCurrent, knowledge_model.IndexStatusSearchable).
		Asc("id").
		Limit(bindingReconciliationBatchSize).
		Find(&publications); err != nil {
		return 0, fmt.Errorf("list searchable knowledge publications for binding reconciliation: %w", err)
	}

	reconciled := 0
	for _, publication := range publications {
		if ctx.Err() != nil {
			return reconciled, ctx.Err()
		}
		updated, err := reconcileSearchableBinding(ctx, publication.ID)
		if err != nil {
			return reconciled, err
		}
		if updated {
			reconciled++
		}
	}
	return reconciled, nil
}

func reconcileSearchableBinding(ctx context.Context, publicationID int64) (bool, error) {
	updated := false
	err := db.WithTx(ctx, func(txCtx context.Context) error {
		publication, exists, err := db.GetByID[knowledge_model.Publication](txCtx, publicationID)
		if err != nil {
			return fmt.Errorf("load knowledge publication for binding reconciliation: %w", err)
		}
		if !exists {
			return nil
		}
		document, exists, err := db.GetByID[knowledge_model.Document](txCtx, publication.DocumentID)
		if err != nil || !exists {
			return err
		}
		space, exists, err := db.GetByID[knowledge_model.Space](txCtx, publication.SpaceID)
		if err != nil || !exists {
			return err
		}
		source, exists, err := db.GetByID[knowledge_model.DataSource](txCtx, document.DataSourceID)
		if err != nil || !exists {
			return err
		}
		now := timeutil.TimeStampNow()
		if !publication.IsPublished(now, document.CurrentPublicationID, space.RevocationGeneration) ||
			source.Status != knowledge_model.DataSourceStatusEnabled ||
			source.SecurityLevel == knowledge_model.SecurityLevelProhibited ||
			(source.ExpiresUnix > 0 && now >= source.ExpiresUnix) {
			return nil
		}
		binding := new(knowledge_model.IndexBinding)
		found, err := db.GetEngine(txCtx).
			Where("publication_id = ? AND engine_profile_version = ? AND status = ?", publication.ID, setting.Knowledge.EngineProfileVersion, knowledge_model.IndexStatusEvaluation).
			Get(binding)
		if err != nil {
			return fmt.Errorf("load legacy knowledge index binding: %w", err)
		}
		if !found || binding.DatasetID != setting.Knowledge.RAGFlowDatasetID || strings.TrimSpace(binding.EngineDocumentID) == "" {
			return nil
		}
		count, err := db.GetEngine(txCtx).
			Where("id = ? AND status = ?", binding.ID, knowledge_model.IndexStatusEvaluation).
			Cols("status", "last_success_unix").
			Update(&knowledge_model.IndexBinding{Status: knowledge_model.IndexStatusSearchable, LastSuccessUnix: now})
		if err != nil {
			return fmt.Errorf("reconcile legacy knowledge index binding: %w", err)
		}
		if count != 1 {
			return nil
		}
		updated = true
		return db.Insert(txCtx, &knowledge_model.AuditEvent{
			ActorID:      0,
			Action:       "knowledge.index_binding.reconciled",
			EntityType:   "knowledge_publication",
			EntityID:     publication.ID,
			SpaceID:      publication.SpaceID,
			Result:       "succeeded",
			ReasonCode:   "legacy_activation_drift",
			TraceID:      fmt.Sprintf("system-binding-reconcile-%d-%d", publication.ID, time.Now().UnixNano()),
			MetadataJSON: "{}",
		})
	})
	return updated, err
}

// UnpublishPublication synchronously revokes FileBay retrieval before an
// asynchronous worker removes the derived RAGFlow document.
func UnpublishPublication(ctx context.Context, publicationID, actorID int64, reason string) error {
	actor, err := user_model.GetUserByID(ctx, actorID)
	if err != nil || !actor.IsAdmin {
		return errors.New("knowledge unpublish requires an administrator")
	}
	publication, exists, err := db.GetByID[knowledge_model.Publication](ctx, publicationID)
	if err != nil || !exists {
		return errors.New("knowledge publication not found")
	}
	return knowledge_model.UnpublishPublication(ctx, knowledge_model.UnpublishPublicationOptions{PublicationID: publication.ID, ActorID: actorID, ExpectedPublicationGeneration: publication.Generation, ExpectedRevocationGeneration: publication.RevocationGeneration, EngineProfileVersion: setting.Knowledge.EngineProfileVersion, TraceID: fmt.Sprintf("web-unpublish-%d-%d", publication.ID, time.Now().UnixNano()), Reason: strings.TrimSpace(reason)})
}

// ExpireDueDataSources turns expired governed sources off and revokes every
// current publication they own. Retrieval independently checks expiry, so a
// source is already fail-closed before this maintenance operation runs.
func ExpireDueDataSources(ctx context.Context, actorID int64) (int, error) {
	actor, err := user_model.GetUserByID(ctx, actorID)
	if err != nil || !actor.IsAdmin {
		return 0, errors.New("knowledge expiry check requires an administrator")
	}
	now := timeutil.TimeStampNow()
	var sources []knowledge_model.DataSource
	if err := db.GetEngine(ctx).Where("status = ? AND expires_unix > 0 AND expires_unix <= ?", knowledge_model.DataSourceStatusEnabled, now).Find(&sources); err != nil {
		return 0, fmt.Errorf("load expired knowledge sources: %w", err)
	}

	expired := 0
	for _, source := range sources {
		updated, err := db.GetEngine(ctx).Where("id = ? AND status = ?", source.ID, knowledge_model.DataSourceStatusEnabled).
			Cols("status").Update(&knowledge_model.DataSource{Status: knowledge_model.DataSourceStatusExpired})
		if err != nil {
			return expired, fmt.Errorf("expire knowledge data source: %w", err)
		}
		if updated != 1 {
			continue
		}
		expired++

		var documents []knowledge_model.Document
		if err := db.GetEngine(ctx).Where("data_source_id = ? AND current_publication_id > 0", source.ID).Find(&documents); err != nil {
			return expired, fmt.Errorf("load expired knowledge documents: %w", err)
		}
		for _, document := range documents {
			if err := UnpublishPublication(ctx, document.CurrentPublicationID, actorID, "source expired"); err != nil {
				return expired, fmt.Errorf("revoke expired knowledge publication: %w", err)
			}
		}
		_ = db.Insert(ctx, &knowledge_model.AuditEvent{ActorID: actorID, Action: "knowledge.data_source.expired", EntityType: "knowledge_data_source", EntityID: source.ID, SpaceID: source.SpaceID, Result: "succeeded", ReasonCode: "expired", TraceID: fmt.Sprintf("expire-source-%d-%d", source.ID, time.Now().UnixNano()), MetadataJSON: "{}"})
	}
	return expired, nil
}

func makeSlug(value string) string {
	source := strings.ToLower(strings.TrimSpace(value))
	value = slugInvalid.ReplaceAllString(source, "-")
	value = strings.Trim(value, "-._")
	if value == "" {
		// Repository names accept only ASCII. Purely Chinese (or other
		// non-ASCII) display names would otherwise all collapse to
		// "knowledge" and contend for the same internal repository.
		sum := sha256.Sum256([]byte(source))
		value = "knowledge-" + hex.EncodeToString(sum[:4])
	}
	if len(value) > 48 {
		value = strings.Trim(value[:48], "-._")
	}
	if value == "" {
		value = "knowledge"
	}
	return value
}

func buildRevisionRepoPath(now time.Time, contentSHA, fileName string) string {
	stem := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	stem = makeSlug(stem)
	return path.Join("documents", fmt.Sprintf("%d-%s", now.UnixNano(), contentSHA[:12]), stem+strings.ToLower(filepath.Ext(fileName)))
}

func validDisplayText(value string, allowEmpty bool) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return allowEmpty
	}
	if len(value) > 255 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return false
	}
	lower := strings.ToLower(value)
	for _, marker := range []string{"api_key", "apikey", "secret", "password", "token=", "private key"} {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return true
}
