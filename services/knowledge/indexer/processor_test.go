// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package indexer_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"testing"

	"code.gitea.io/gitea/models/db"
	knowledge_model "code.gitea.io/gitea/models/knowledge"
	"code.gitea.io/gitea/models/unittest"
	"code.gitea.io/gitea/services/knowledge/indexer"
	"code.gitea.io/gitea/services/knowledge/ragflow"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "code.gitea.io/gitea/models"
)

const (
	testEngineName    = "ragflow"
	testDatasetID     = "private-dataset-1"
	testEngineProfile = "ragflow-v0.26.4-profile-1"
	testEngineDocID   = "engine-document-1"
)

var maskedArtifact = []byte("masked production knowledge\n")

func TestMain(m *testing.M) {
	unittest.MainTest(m)
}

func TestProcessorUpsertUsesExactRevisionAndPersistsBindingBeforeMetadata(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	fixture := insertIndexerFixture(t, knowledge_model.IndexJobTypeUpsert)
	source := &fakeSource{content: maskedArtifact}
	engine := &fakeEngine{uploadID: testEngineDocID}
	engine.onMetadata = func(documentID string, metadata ragflow.GovernanceMetadata) {
		binding := loadBinding(t, fixture.publication.ID)
		assert.Equal(t, testEngineDocID, binding.EngineDocumentID, "binding must be durable before the next external mutation")
		assert.Equal(t, testEngineDocID, documentID)
		assert.Equal(t, fixture.space.ID, metadata.SpaceID)
		assert.Equal(t, fixture.document.ID, metadata.DocumentID)
		assert.Equal(t, fixture.revision.ID, metadata.RevisionID)
		assert.Equal(t, fixture.publication.ID, metadata.PublicationID)
		assert.Equal(t, fixture.publication.Generation, metadata.PublicationGeneration)
		assert.Equal(t, fixture.revision.ContentSHA256, metadata.RevisionSHA256)
		assert.Equal(t, fixture.publication.ACLPolicyVersion, metadata.ACLPolicyVersion)
		assert.Equal(t, fixture.publication.RevocationGeneration, metadata.RevocationGeneration)
	}

	processor := newProcessor(t, source, engine)
	require.NoError(t, processor.Process(t.Context(), fixture.job.ID))

	assert.Equal(t, 1, source.calls)
	assert.Equal(t, fixture.space.RepoID, source.repoID)
	assert.Equal(t, fixture.revision.GitCommitSHA, source.commitSHA)
	assert.Equal(t, fixture.revision.RepoPath, source.repoPath)
	assert.Equal(t, 1, engine.uploadCalls)
	assert.Equal(t, fixture.revision.FileName, engine.upload.FileName)
	assert.Equal(t, fixture.revision.Size, engine.upload.Size)
	assert.Equal(t, maskedArtifact, engine.uploadBytes)
	assert.Equal(t, 1, engine.metadataCalls)
	assert.Equal(t, 1, engine.parseCalls)
	assert.Equal(t, []string{testEngineDocID}, engine.parseIDs)

	binding := loadBinding(t, fixture.publication.ID)
	assert.Equal(t, fixture.revision.ContentSHA256, binding.ContentSHA256)
	assert.Equal(t, testDatasetID, binding.DatasetID)
	assert.Equal(t, testEngineProfile, binding.EngineProfileVersion)
	assert.Equal(t, knowledge_model.IndexStatusIndexing, binding.Status)
	assert.Equal(t, knowledge_model.IndexStatusIndexing, loadPublication(t, fixture.publication.ID).IndexStatus)
	assert.Equal(t, knowledge_model.IndexJobStatusSucceeded, loadJob(t, fixture.job.ID).Status)
}

func TestProcessorRetryReusesDurableBindingWithoutDuplicateUpload(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	fixture := insertIndexerFixture(t, knowledge_model.IndexJobTypeUpsert)
	source := &fakeSource{content: maskedArtifact}
	engine := &fakeEngine{uploadID: testEngineDocID, metadataErr: errors.New("temporary metadata failure containing masked body")}
	processor := newProcessor(t, source, engine)

	require.Error(t, processor.Process(t.Context(), fixture.job.ID))
	assert.Equal(t, 1, engine.uploadCalls)
	assert.Equal(t, testEngineDocID, loadBinding(t, fixture.publication.ID).EngineDocumentID)

	job := loadJob(t, fixture.job.ID)
	job.Status = knowledge_model.IndexJobStatusQueued
	_, err := db.GetEngine(t.Context()).ID(job.ID).Cols("status").Update(job)
	require.NoError(t, err)
	engine.metadataErr = nil
	require.NoError(t, processor.Process(t.Context(), fixture.job.ID))
	assert.Equal(t, 1, engine.uploadCalls, "retry must reuse the persisted engine document")
	assert.Equal(t, 2, engine.metadataCalls)
	assert.Equal(t, 1, engine.parseCalls)
}

func TestProcessorPollDoneStopsAtEvaluationAndFailureIsRedacted(t *testing.T) {
	t.Run("done awaits explicit activation", func(t *testing.T) {
		require.NoError(t, unittest.PrepareTestDatabase())
		fixture := insertIndexerFixture(t, knowledge_model.IndexJobTypePoll)
		insertBinding(t, fixture, knowledge_model.IndexStatusIndexing)
		engine := &fakeEngine{status: &ragflow.DocumentStatus{DocumentID: testEngineDocID, State: ragflow.DocumentStateDone, ChunkCount: 4}}
		require.NoError(t, newProcessor(t, &fakeSource{}, engine).Process(t.Context(), fixture.job.ID))

		publication := loadPublication(t, fixture.publication.ID)
		assert.Equal(t, knowledge_model.IndexStatusEvaluation, publication.IndexStatus)
		assert.Equal(t, knowledge_model.ValidityStatusScheduled, publication.ValidityStatus)
		assert.False(t, publication.IsCurrent)
		assert.Equal(t, knowledge_model.IndexStatusEvaluation, loadBinding(t, publication.ID).Status)
		assert.Equal(t, knowledge_model.IndexJobStatusSucceeded, loadJob(t, fixture.job.ID).Status)
	})

	t.Run("failed engine state fails closed", func(t *testing.T) {
		require.NoError(t, unittest.PrepareTestDatabase())
		fixture := insertIndexerFixture(t, knowledge_model.IndexJobTypePoll)
		insertBinding(t, fixture, knowledge_model.IndexStatusIndexing)
		engine := &fakeEngine{status: &ragflow.DocumentStatus{DocumentID: testEngineDocID, State: ragflow.DocumentStateFailed}}
		_ = newProcessor(t, &fakeSource{}, engine).Process(t.Context(), fixture.job.ID)
		assert.Equal(t, knowledge_model.IndexStatusFailed, loadPublication(t, fixture.publication.ID).IndexStatus)
		assert.Equal(t, knowledge_model.IndexStatusFailed, loadBinding(t, fixture.publication.ID).Status)
		assert.Equal(t, knowledge_model.IndexJobStatusFailed, loadJob(t, fixture.job.ID).Status)
	})

	t.Run("transport failure never stores upstream body", func(t *testing.T) {
		require.NoError(t, unittest.PrepareTestDatabase())
		fixture := insertIndexerFixture(t, knowledge_model.IndexJobTypePoll)
		insertBinding(t, fixture, knowledge_model.IndexStatusIndexing)
		secret := "API_KEY=secret raw masked document body"
		engine := &fakeEngine{statusErr: errors.New(secret)}
		require.Error(t, newProcessor(t, &fakeSource{}, engine).Process(t.Context(), fixture.job.ID))

		assert.Equal(t, knowledge_model.IndexStatusFailed, loadPublication(t, fixture.publication.ID).IndexStatus)
		job := loadJob(t, fixture.job.ID)
		assert.Equal(t, knowledge_model.IndexJobStatusFailed, job.Status)
		assert.NotContains(t, job.LastError, secret)
		assert.NotContains(t, job.LastError, "API_KEY")
		assert.LessOrEqual(t, len(job.LastError), 128)
	})
}

func TestProcessorRejectsStaleGenerationRevocationAndSHAWithoutExternalCalls(t *testing.T) {
	tests := []struct {
		name              string
		mutate            func(*testing.T, *indexerFixture, *fakeSource)
		want              knowledge_model.IndexJobStatus
		wantExternalCalls bool
	}{
		{
			name: "unrelated space publication generation does not stale target publication",
			mutate: func(t *testing.T, fixture *indexerFixture, _ *fakeSource) {
				space := fixture.space
				space.PublicationGeneration++
				_, err := db.GetEngine(t.Context()).ID(space.ID).Cols("publication_generation").Update(space)
				require.NoError(t, err)
			},
			want:              knowledge_model.IndexJobStatusSucceeded,
			wantExternalCalls: true,
		},
		{
			name: "revocation generation is stale",
			mutate: func(t *testing.T, fixture *indexerFixture, _ *fakeSource) {
				space := fixture.space
				space.RevocationGeneration++
				_, err := db.GetEngine(t.Context()).ID(space.ID).Cols("revocation_generation").Update(space)
				require.NoError(t, err)
			},
			want: knowledge_model.IndexJobStatusCancelled,
		},
		{
			name: "repository bytes do not match immutable revision sha",
			mutate: func(_ *testing.T, _ *indexerFixture, source *fakeSource) {
				source.content = []byte("different masked bytes")
			},
			want: knowledge_model.IndexJobStatusFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, unittest.PrepareTestDatabase())
			fixture := insertIndexerFixture(t, knowledge_model.IndexJobTypeUpsert)
			source := &fakeSource{content: maskedArtifact}
			engine := &fakeEngine{uploadID: testEngineDocID}
			tt.mutate(t, fixture, source)
			_ = newProcessor(t, source, engine).Process(t.Context(), fixture.job.ID)
			assert.Equal(t, tt.want, loadJob(t, fixture.job.ID).Status)
			if tt.wantExternalCalls {
				assert.Positive(t, engine.totalCalls())
			} else {
				assert.Zero(t, engine.totalCalls())
			}
		})
	}
}

func TestProcessorDeleteIsIdempotentAndJobCASPreventsDuplicateProcessing(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	fixture := insertIndexerFixture(t, knowledge_model.IndexJobTypeDelete)
	fixture.publication.ValidityStatus = knowledge_model.ValidityStatusUnpublished
	fixture.publication.IndexStatus = knowledge_model.IndexStatusDeleting
	_, err := db.GetEngine(t.Context()).ID(fixture.publication.ID).Cols("validity_status", "index_status").Update(fixture.publication)
	require.NoError(t, err)
	insertBinding(t, fixture, knowledge_model.IndexStatusDeleting)
	engine := &fakeEngine{}
	processor := newProcessor(t, &fakeSource{}, engine)

	require.NoError(t, processor.Process(t.Context(), fixture.job.ID))
	require.NoError(t, processor.Process(t.Context(), fixture.job.ID))
	assert.Equal(t, 1, engine.deleteCalls)
	assert.Equal(t, []string{testEngineDocID}, engine.deleteIDs)
	assert.Equal(t, knowledge_model.IndexStatusDeleted, loadBinding(t, fixture.publication.ID).Status)
	assert.Equal(t, knowledge_model.IndexStatusDeleted, loadPublication(t, fixture.publication.ID).IndexStatus)
	assert.Equal(t, knowledge_model.IndexJobStatusSucceeded, loadJob(t, fixture.job.ID).Status)
}

func newProcessor(t *testing.T, source indexer.SourceProvider, engine indexer.Engine) *indexer.Processor {
	t.Helper()
	processor, err := indexer.NewProcessor(indexer.Config{
		Source:     source,
		Engine:     engine,
		EngineName: testEngineName,
		DatasetID:  testDatasetID,
	})
	require.NoError(t, err)
	return processor
}

type indexerFixture struct {
	space       *knowledge_model.Space
	document    *knowledge_model.Document
	revision    *knowledge_model.Revision
	publication *knowledge_model.Publication
	job         *knowledge_model.IndexJob
}

func insertIndexerFixture(t *testing.T, jobType knowledge_model.IndexJobType) *indexerFixture {
	t.Helper()
	sha := sha256.Sum256(maskedArtifact)
	space := &knowledge_model.Space{OwnerID: 2, RepoID: 13001, Name: "Indexer space", Slug: "indexer-space", Status: knowledge_model.SpaceStatusActive, PublicationGeneration: 1, RevocationGeneration: 3, CreatedBy: 2}
	require.NoError(t, db.Insert(t.Context(), space))
	document := &knowledge_model.Document{SpaceID: space.ID, Title: "Policy", RepoPath: "knowledge/policy.md", MIMEType: "text/markdown", GovernanceStatus: knowledge_model.GovernanceStatusApproved, CreatedBy: 2}
	require.NoError(t, db.Insert(t.Context(), document))
	revision := &knowledge_model.Revision{DocumentID: document.ID, RevisionNo: 1, FileName: "policy.md", RepoPath: document.RepoPath, ContentSHA256: hex.EncodeToString(sha[:]), Size: int64(len(maskedArtifact)), GitCommitSHA: "0123456789abcdef0123456789abcdef01234567", MaskPolicyVersion: "mask-policy-2026-01", MaskManifestJSON: `{"schema_version":1,"masked":true,"findings_count":1,"categories":["person_name"]}`, SourceAuthorization: "repo-write-grant:2", CreatedBy: 2}
	require.NoError(t, db.Insert(t.Context(), revision))
	approval := &knowledge_model.Approval{RevisionID: revision.ID, Status: knowledge_model.ApprovalStatusApproved, RequestedBy: 2, DecidedBy: 1}
	require.NoError(t, db.Insert(t.Context(), approval))
	indexStatus := knowledge_model.IndexStatusQueued
	if jobType == knowledge_model.IndexJobTypePoll {
		indexStatus = knowledge_model.IndexStatusIndexing
	}
	publication := &knowledge_model.Publication{SpaceID: space.ID, DocumentID: document.ID, RevisionID: revision.ID, ApprovalID: approval.ID, Generation: 1, GovernanceStatus: knowledge_model.GovernanceStatusApproved, ValidityStatus: knowledge_model.ValidityStatusScheduled, IndexStatus: indexStatus, ACLPolicyVersion: 4, RevocationGeneration: space.RevocationGeneration}
	require.NoError(t, db.Insert(t.Context(), publication))
	job := &knowledge_model.IndexJob{PublicationID: publication.ID, JobType: jobType, IdempotencyKey: "indexer-test:" + string(jobType), Status: knowledge_model.IndexJobStatusQueued, RevisionSHA256: revision.ContentSHA256, EngineProfileVersion: testEngineProfile, MaxAttempts: 5}
	require.NoError(t, db.Insert(t.Context(), job))
	return &indexerFixture{space: space, document: document, revision: revision, publication: publication, job: job}
}

func insertBinding(t *testing.T, fixture *indexerFixture, status knowledge_model.IndexStatus) {
	t.Helper()
	require.NoError(t, db.Insert(t.Context(), &knowledge_model.IndexBinding{SpaceID: fixture.space.ID, DocumentID: fixture.document.ID, RevisionID: fixture.revision.ID, PublicationID: fixture.publication.ID, Engine: testEngineName, EngineProfileVersion: testEngineProfile, DatasetID: testDatasetID, EngineDocumentID: testEngineDocID, ContentSHA256: fixture.revision.ContentSHA256, Status: status}))
}

func loadBinding(t *testing.T, publicationID int64) *knowledge_model.IndexBinding {
	t.Helper()
	value := new(knowledge_model.IndexBinding)
	has, err := db.GetEngine(t.Context()).Where("publication_id = ?", publicationID).Get(value)
	require.NoError(t, err)
	require.True(t, has)
	return value
}

func loadPublication(t *testing.T, id int64) *knowledge_model.Publication {
	t.Helper()
	value := &knowledge_model.Publication{ID: id}
	has, err := db.GetEngine(t.Context()).Get(value)
	require.NoError(t, err)
	require.True(t, has)
	return value
}

func loadJob(t *testing.T, id int64) *knowledge_model.IndexJob {
	t.Helper()
	value := &knowledge_model.IndexJob{ID: id}
	has, err := db.GetEngine(t.Context()).Get(value)
	require.NoError(t, err)
	require.True(t, has)
	return value
}

type fakeSource struct {
	content   []byte
	calls     int
	repoID    int64
	commitSHA string
	repoPath  string
	err       error
}

func (source *fakeSource) Open(_ context.Context, repoID int64, commitSHA, repoPath string) (io.ReadCloser, error) {
	source.calls++
	source.repoID, source.commitSHA, source.repoPath = repoID, commitSHA, repoPath
	if source.err != nil {
		return nil, source.err
	}
	return io.NopCloser(bytes.NewReader(source.content)), nil
}

type fakeEngine struct {
	uploadID      string
	upload        ragflow.UploadRequest
	uploadBytes   []byte
	uploadCalls   int
	metadataCalls int
	parseCalls    int
	statusCalls   int
	deleteCalls   int
	metadataErr   error
	statusErr     error
	status        *ragflow.DocumentStatus
	parseIDs      []string
	deleteIDs     []string
	onMetadata    func(string, ragflow.GovernanceMetadata)
}

func (engine *fakeEngine) Upload(_ context.Context, upload ragflow.UploadRequest) (*ragflow.UploadedDocument, error) {
	engine.uploadCalls++
	engine.upload = upload
	content, err := io.ReadAll(upload.Content)
	engine.uploadBytes = content
	if err != nil {
		return nil, err
	}
	return &ragflow.UploadedDocument{ID: engine.uploadID}, nil
}

func (engine *fakeEngine) SetMetadata(_ context.Context, documentID string, metadata ragflow.GovernanceMetadata) error {
	engine.metadataCalls++
	if engine.onMetadata != nil {
		engine.onMetadata(documentID, metadata)
	}
	return engine.metadataErr
}

func (engine *fakeEngine) StartParsing(_ context.Context, documentIDs []string) error {
	engine.parseCalls++
	engine.parseIDs = append([]string(nil), documentIDs...)
	return nil
}

func (engine *fakeEngine) GetDocumentStatus(_ context.Context, _ string) (*ragflow.DocumentStatus, error) {
	engine.statusCalls++
	return engine.status, engine.statusErr
}

func (engine *fakeEngine) DeleteDocuments(_ context.Context, documentIDs []string) error {
	engine.deleteCalls++
	engine.deleteIDs = append([]string(nil), documentIDs...)
	return nil
}

func (engine *fakeEngine) totalCalls() int {
	return engine.uploadCalls + engine.metadataCalls + engine.parseCalls + engine.statusCalls + engine.deleteCalls
}

var _ indexer.SourceProvider = (*fakeSource)(nil)
var _ indexer.Engine = (*fakeEngine)(nil)
