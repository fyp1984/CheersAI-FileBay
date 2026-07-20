// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Package indexer runs durable FileBay-to-RAGFlow index operations.
package indexer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"code.gitea.io/gitea/models/db"
	knowledge_model "code.gitea.io/gitea/models/knowledge"
	"code.gitea.io/gitea/services/knowledge/ragflow"
)

// SourceProvider opens a masked artifact at an exact immutable Git revision.
type SourceProvider interface {
	Open(ctx context.Context, repoID int64, commitSHA, repoPath string) (io.ReadCloser, error)
}

// Engine is the derived-index contract used by the processor.
type Engine interface {
	Upload(ctx context.Context, upload ragflow.UploadRequest) (*ragflow.UploadedDocument, error)
	SetMetadata(ctx context.Context, documentID string, metadata ragflow.GovernanceMetadata) error
	StartParsing(ctx context.Context, documentIDs []string) error
	GetDocumentStatus(ctx context.Context, documentID string) (*ragflow.DocumentStatus, error)
	DeleteDocuments(ctx context.Context, documentIDs []string) error
}

// Config defines one bounded processor instance.
type Config struct {
	Source     SourceProvider
	Engine     Engine
	EngineName string
	DatasetID  string
}

// Processor claims and executes one durable index job at a time.
type Processor struct {
	source     SourceProvider
	engine     Engine
	engineName string
	datasetID  string
}

// NewProcessor validates all collaborators up front.
func NewProcessor(config Config) (*Processor, error) {
	if config.Source == nil || config.Engine == nil ||
		strings.TrimSpace(config.EngineName) == "" || strings.TrimSpace(config.DatasetID) == "" {
		return nil, errors.New("invalid knowledge indexer configuration")
	}
	return &Processor{
		source:     config.Source,
		engine:     config.Engine,
		engineName: config.EngineName,
		datasetID:  config.DatasetID,
	}, nil
}

// Process claims one queued/retry job and performs the exact derived operation.
func (p *Processor) Process(ctx context.Context, jobID int64) error {
	job, claimed, err := claimJob(ctx, jobID)
	if err != nil || !claimed {
		return err
	}
	switch job.JobType {
	case knowledge_model.IndexJobTypeUpsert:
		return p.processUpsert(ctx, job)
	case knowledge_model.IndexJobTypePoll:
		return p.processPoll(ctx, job)
	case knowledge_model.IndexJobTypeDelete:
		return p.processDelete(ctx, job)
	default:
		return failJob(ctx, job.ID, job.PublicationID, "unsupported job type", true)
	}
}

type graph struct {
	job         *knowledge_model.IndexJob
	space       *knowledge_model.Space
	document    *knowledge_model.Document
	revision    *knowledge_model.Revision
	publication *knowledge_model.Publication
}

func claimJob(ctx context.Context, jobID int64) (*knowledge_model.IndexJob, bool, error) {
	if jobID <= 0 {
		return nil, false, errors.New("invalid knowledge index job")
	}
	job, exists, err := db.GetByID[knowledge_model.IndexJob](ctx, jobID)
	if err != nil {
		return nil, false, fmt.Errorf("load knowledge index job: %w", err)
	}
	if !exists {
		return nil, false, errors.New("knowledge index job not found")
	}
	if job.Status == knowledge_model.IndexJobStatusSucceeded || job.Status == knowledge_model.IndexJobStatusCancelled {
		return job, false, nil
	}
	if job.Status != knowledge_model.IndexJobStatusQueued && job.Status != knowledge_model.IndexJobStatusRetry {
		return nil, false, errors.New("knowledge index job is not claimable")
	}
	job.Status = knowledge_model.IndexJobStatusRunning
	job.Attempt++
	updated, err := db.GetEngine(ctx).
		Where("id = ? AND status IN (?, ?)", job.ID, knowledge_model.IndexJobStatusQueued, knowledge_model.IndexJobStatusRetry).
		Cols("status", "attempt").
		Update(job)
	if err != nil {
		return nil, false, fmt.Errorf("claim knowledge index job: %w", err)
	}
	if updated != 1 {
		return nil, false, errors.New("knowledge index job claim conflict")
	}
	return job, true, nil
}

func loadGraph(ctx context.Context, job *knowledge_model.IndexJob) (*graph, error) {
	publication, exists, err := db.GetByID[knowledge_model.Publication](ctx, job.PublicationID)
	if err != nil {
		return nil, fmt.Errorf("load knowledge publication: %w", err)
	}
	if !exists {
		return nil, errors.New("knowledge publication not found")
	}
	revision, exists, err := db.GetByID[knowledge_model.Revision](ctx, publication.RevisionID)
	if err != nil {
		return nil, fmt.Errorf("load knowledge revision: %w", err)
	}
	if !exists {
		return nil, errors.New("knowledge revision not found")
	}
	document, exists, err := db.GetByID[knowledge_model.Document](ctx, publication.DocumentID)
	if err != nil {
		return nil, fmt.Errorf("load knowledge document: %w", err)
	}
	if !exists || document.ID != revision.DocumentID {
		return nil, errors.New("knowledge document mismatch")
	}
	space, exists, err := db.GetByID[knowledge_model.Space](ctx, publication.SpaceID)
	if err != nil {
		return nil, fmt.Errorf("load knowledge space: %w", err)
	}
	if !exists || space.ID != document.SpaceID {
		return nil, errors.New("knowledge space mismatch")
	}
	return &graph{job: job, space: space, document: document, revision: revision, publication: publication}, nil
}

func (p *Processor) processUpsert(ctx context.Context, job *knowledge_model.IndexJob) error {
	graph, err := loadGraph(ctx, job)
	if err != nil {
		return failJob(ctx, job.ID, job.PublicationID, "load graph failed", true)
	}
	if !graph.validForUpsert() {
		return cancelJob(ctx, job.ID)
	}

	binding, exists, err := p.loadBinding(ctx, graph.publication.ID)
	if err != nil {
		return failJob(ctx, job.ID, job.PublicationID, "load binding failed", true)
	}
	if !exists {
		content, err := p.readRevision(ctx, graph)
		if err != nil {
			return failJob(ctx, job.ID, job.PublicationID, "source integrity check failed", true)
		}
		uploaded, err := p.engine.Upload(ctx, ragflow.UploadRequest{
			FileName: graph.revision.FileName,
			Content:  bytes.NewReader(content),
			Size:     graph.revision.Size,
		})
		if err != nil || uploaded == nil || strings.TrimSpace(uploaded.ID) == "" {
			return failJob(ctx, job.ID, job.PublicationID, "upload failed", true)
		}
		binding = &knowledge_model.IndexBinding{
			SpaceID:              graph.space.ID,
			DocumentID:           graph.document.ID,
			RevisionID:           graph.revision.ID,
			PublicationID:        graph.publication.ID,
			Engine:               p.engineName,
			EngineProfileVersion: job.EngineProfileVersion,
			DatasetID:            p.datasetID,
			EngineDocumentID:     uploaded.ID,
			ContentSHA256:        graph.revision.ContentSHA256,
			Status:               knowledge_model.IndexStatusIndexing,
		}
		if err := db.Insert(ctx, binding); err != nil {
			return failJob(ctx, job.ID, job.PublicationID, "persist binding failed", true)
		}
	}

	metadata := ragflow.GovernanceMetadata{
		SpaceID:               graph.space.ID,
		DocumentID:            graph.document.ID,
		RevisionID:            graph.revision.ID,
		PublicationID:         graph.publication.ID,
		PublicationGeneration: graph.publication.Generation,
		RevisionSHA256:        graph.revision.ContentSHA256,
		ACLPolicyVersion:      graph.publication.ACLPolicyVersion,
		RevocationGeneration:  graph.publication.RevocationGeneration,
	}
	if err := p.engine.SetMetadata(ctx, binding.EngineDocumentID, metadata); err != nil {
		return failJob(ctx, job.ID, job.PublicationID, "metadata failed", true)
	}
	if err := p.engine.StartParsing(ctx, []string{binding.EngineDocumentID}); err != nil {
		return failJob(ctx, job.ID, job.PublicationID, "parse start failed", true)
	}
	return db.WithTx(ctx, func(txCtx context.Context) error {
		if err := updateBindingStatus(txCtx, binding.ID, knowledge_model.IndexStatusIndexing); err != nil {
			return err
		}
		if err := updatePublicationIndexStatus(txCtx, graph.publication.ID, knowledge_model.IndexStatusIndexing); err != nil {
			return err
		}
		if err := markJobSucceeded(txCtx, job.ID); err != nil {
			return err
		}
		if err := markOutboxDispatched(txCtx, job.IdempotencyKey); err != nil {
			return err
		}
		return enqueuePollJob(txCtx, graph, job)
	})
}

// enqueuePollJob keeps parsing status under FileBay control. The job is
// idempotent and can be manually processed or later picked up by a worker.
func enqueuePollJob(ctx context.Context, graph *graph, completedJob *knowledge_model.IndexJob) error {
	key := knowledge_model.BuildIndexIdempotencyKey(knowledge_model.IndexIdempotencyKeyInput{
		OwnerID:              graph.space.OwnerID,
		SpaceID:              graph.space.ID,
		PublicationID:        graph.publication.ID,
		Generation:           graph.publication.Generation,
		RevisionSHA256:       graph.revision.ContentSHA256,
		EngineProfileVersion: completedJob.EngineProfileVersion,
		JobType:              knowledge_model.IndexJobTypePoll,
	})
	existing := new(knowledge_model.IndexJob)
	exists, err := db.GetEngine(ctx).Where("idempotency_key = ?", key).Get(existing)
	if err != nil || exists {
		return err
	}
	job := &knowledge_model.IndexJob{PublicationID: graph.publication.ID, JobType: knowledge_model.IndexJobTypePoll, IdempotencyKey: key, Status: knowledge_model.IndexJobStatusQueued, RevisionSHA256: graph.revision.ContentSHA256, EngineProfileVersion: completedJob.EngineProfileVersion, MaxAttempts: completedJob.MaxAttempts}
	if err := db.Insert(ctx, job); err != nil {
		return err
	}
	return db.Insert(ctx, &knowledge_model.Outbox{AggregateType: "knowledge_publication", AggregateID: graph.publication.ID, EventType: "knowledge.index.poll.requested", IdempotencyKey: key, PayloadJSON: fmt.Sprintf(`{"publication_id":%d,"job_type":"poll"}`, graph.publication.ID), Status: knowledge_model.OutboxStatusPending})
}

func (g *graph) validForUpsert() bool {
	return g.space.Status == knowledge_model.SpaceStatusActive &&
		g.publication.GovernanceStatus == knowledge_model.GovernanceStatusApproved &&
		g.publication.ValidityStatus == knowledge_model.ValidityStatusScheduled &&
		g.publication.Generation > 0 &&
		g.publication.RevocationGeneration == g.space.RevocationGeneration &&
		g.revision.ID == g.publication.RevisionID &&
		g.revision.ContentSHA256 == g.job.RevisionSHA256
}

func (p *Processor) readRevision(ctx context.Context, graph *graph) ([]byte, error) {
	reader, err := p.source.Open(ctx, graph.space.RepoID, graph.revision.GitCommitSHA, graph.revision.RepoPath)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	content, err := io.ReadAll(io.LimitReader(reader, graph.revision.Size+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) != graph.revision.Size {
		return nil, errors.New("revision size mismatch")
	}
	sum := sha256.Sum256(content)
	if hex.EncodeToString(sum[:]) != graph.revision.ContentSHA256 {
		return nil, errors.New("revision sha mismatch")
	}
	return content, nil
}

func (p *Processor) processPoll(ctx context.Context, job *knowledge_model.IndexJob) error {
	graph, err := loadGraph(ctx, job)
	if err != nil {
		return failJob(ctx, job.ID, job.PublicationID, "load graph failed", true)
	}
	if graph.publication.RevocationGeneration != graph.space.RevocationGeneration {
		return cancelJob(ctx, job.ID)
	}
	binding, exists, err := p.loadBinding(ctx, graph.publication.ID)
	if err != nil || !exists {
		return failJob(ctx, job.ID, job.PublicationID, "binding missing", true)
	}
	status, err := p.engine.GetDocumentStatus(ctx, binding.EngineDocumentID)
	if err != nil {
		return failJob(ctx, job.ID, job.PublicationID, "status failed", true)
	}
	switch status.State {
	case ragflow.DocumentStateDone:
		return db.WithTx(ctx, func(txCtx context.Context) error {
			if err := updateBindingStatus(txCtx, binding.ID, knowledge_model.IndexStatusEvaluation); err != nil {
				return err
			}
			if err := updatePublicationIndexStatus(txCtx, graph.publication.ID, knowledge_model.IndexStatusEvaluation); err != nil {
				return err
			}
			return markJobSucceeded(txCtx, job.ID)
		})
	case ragflow.DocumentStateFailed:
		return failJob(ctx, job.ID, job.PublicationID, "engine failed", false)
	default:
		return markJobRetry(ctx, job.ID)
	}
}

func (p *Processor) processDelete(ctx context.Context, job *knowledge_model.IndexJob) error {
	graph, err := loadGraph(ctx, job)
	if err != nil {
		return failJob(ctx, job.ID, job.PublicationID, "load graph failed", true)
	}
	binding, exists, err := p.loadBinding(ctx, graph.publication.ID)
	if err != nil {
		return failJob(ctx, job.ID, job.PublicationID, "load binding failed", true)
	}
	if !exists || binding.Status == knowledge_model.IndexStatusDeleted {
		return db.WithTx(ctx, func(txCtx context.Context) error {
			if err := updatePublicationIndexStatus(txCtx, graph.publication.ID, knowledge_model.IndexStatusDeleted); err != nil {
				return err
			}
			return markJobSucceeded(txCtx, job.ID)
		})
	}
	if err := p.engine.DeleteDocuments(ctx, []string{binding.EngineDocumentID}); err != nil {
		return failJob(ctx, job.ID, job.PublicationID, "delete failed", true)
	}
	return db.WithTx(ctx, func(txCtx context.Context) error {
		if err := updateBindingStatus(txCtx, binding.ID, knowledge_model.IndexStatusDeleted); err != nil {
			return err
		}
		if err := updatePublicationIndexStatus(txCtx, graph.publication.ID, knowledge_model.IndexStatusDeleted); err != nil {
			return err
		}
		if err := markJobSucceeded(txCtx, job.ID); err != nil {
			return err
		}
		return markOutboxDispatched(txCtx, job.IdempotencyKey)
	})
}

func (p *Processor) loadBinding(ctx context.Context, publicationID int64) (*knowledge_model.IndexBinding, bool, error) {
	binding := new(knowledge_model.IndexBinding)
	exists, err := db.GetEngine(ctx).
		Where("publication_id = ? AND engine = ? AND dataset_id = ?", publicationID, p.engineName, p.datasetID).
		Get(binding)
	if err != nil {
		return nil, false, err
	}
	return binding, exists, nil
}

func failJob(ctx context.Context, jobID, publicationID int64, message string, returnError bool) error {
	err := db.WithTx(ctx, func(txCtx context.Context) error {
		job := &knowledge_model.IndexJob{ID: jobID, Status: knowledge_model.IndexJobStatusFailed, LastError: redactIndexError(message)}
		if _, err := db.GetEngine(txCtx).ID(jobID).Cols("status", "last_error").Update(job); err != nil {
			return err
		}
		if publicationID > 0 {
			if err := updatePublicationIndexStatus(txCtx, publicationID, knowledge_model.IndexStatusFailed); err != nil {
				return err
			}
			binding := new(knowledge_model.IndexBinding)
			exists, err := db.GetEngine(txCtx).Where("publication_id = ?", publicationID).Get(binding)
			if err != nil {
				return err
			}
			if exists {
				if err := updateBindingStatus(txCtx, binding.ID, knowledge_model.IndexStatusFailed); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if returnError {
		return errors.New(redactIndexError(message))
	}
	return nil
}

func cancelJob(ctx context.Context, jobID int64) error {
	job := &knowledge_model.IndexJob{ID: jobID, Status: knowledge_model.IndexJobStatusCancelled, LastError: "cancelled stale knowledge index job"}
	_, err := db.GetEngine(ctx).ID(jobID).Cols("status", "last_error").Update(job)
	return err
}

func markJobSucceeded(ctx context.Context, jobID int64) error {
	job := &knowledge_model.IndexJob{ID: jobID, Status: knowledge_model.IndexJobStatusSucceeded, LastError: ""}
	_, err := db.GetEngine(ctx).ID(jobID).Cols("status", "last_error").Update(job)
	return err
}

func markJobRetry(ctx context.Context, jobID int64) error {
	job := &knowledge_model.IndexJob{ID: jobID, Status: knowledge_model.IndexJobStatusRetry}
	_, err := db.GetEngine(ctx).ID(jobID).Cols("status").Update(job)
	return err
}

func updateBindingStatus(ctx context.Context, bindingID int64, status knowledge_model.IndexStatus) error {
	binding := &knowledge_model.IndexBinding{ID: bindingID, Status: status}
	_, err := db.GetEngine(ctx).ID(bindingID).Cols("status").Update(binding)
	return err
}

func updatePublicationIndexStatus(ctx context.Context, publicationID int64, status knowledge_model.IndexStatus) error {
	publication := &knowledge_model.Publication{ID: publicationID, IndexStatus: status}
	_, err := db.GetEngine(ctx).ID(publicationID).Cols("index_status").Update(publication)
	return err
}

func markOutboxDispatched(ctx context.Context, idempotencyKey string) error {
	if strings.TrimSpace(idempotencyKey) == "" {
		return nil
	}
	outbox := &knowledge_model.Outbox{Status: knowledge_model.OutboxStatusDispatched}
	_, err := db.GetEngine(ctx).Where("idempotency_key = ?", idempotencyKey).Cols("status").Update(outbox)
	return err
}

func redactIndexError(message string) string {
	if strings.TrimSpace(message) == "" {
		message = "knowledge index operation failed"
	}
	message = strings.TrimSpace(message)
	if len(message) > 96 {
		message = message[:96]
	}
	return "knowledge index failed: " + message
}
