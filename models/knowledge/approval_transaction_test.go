// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package knowledge_test

import (
	"fmt"
	"testing"

	"code.gitea.io/gitea/models/db"
	knowledge_model "code.gitea.io/gitea/models/knowledge"
	"code.gitea.io/gitea/models/unittest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "code.gitea.io/gitea/models"
)

func TestMain(m *testing.M) {
	unittest.MainTest(m)
}

func TestApproveRevisionIsTransactionalAndIdempotent(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	space, document, revision, approval := insertPendingApproval(t, 2)
	options := knowledge_model.ApproveRevisionOptions{
		ApprovalID:           approval.ID,
		DecidedBy:            1,
		Comment:              "approved for controlled indexing",
		EngineProfileVersion: "ragflow-v0.26.4-profile-1",
		ACLPolicyVersion:     4,
		TraceID:              "trace-approve-001",
	}

	first, err := knowledge_model.ApproveRevision(t.Context(), options)
	require.NoError(t, err)
	require.NotNil(t, first)
	require.NotNil(t, first.Publication)
	require.NotNil(t, first.Outbox)
	require.NotNil(t, first.IndexJob)
	assert.True(t, first.Created)

	second, err := knowledge_model.ApproveRevision(t.Context(), options)
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.False(t, second.Created)
	assert.Equal(t, first.Publication.ID, second.Publication.ID)
	assert.Equal(t, first.Outbox.ID, second.Outbox.ID)
	assert.Equal(t, first.IndexJob.ID, second.IndexJob.ID)

	publicationCount, err := db.GetEngine(t.Context()).Where("approval_id = ?", approval.ID).Count(new(knowledge_model.Publication))
	require.NoError(t, err)
	assert.EqualValues(t, 1, publicationCount)

	outboxCount, err := db.GetEngine(t.Context()).Where("idempotency_key = ?", first.Outbox.IdempotencyKey).Count(new(knowledge_model.Outbox))
	require.NoError(t, err)
	assert.EqualValues(t, 1, outboxCount)

	jobCount, err := db.GetEngine(t.Context()).Where("idempotency_key = ?", first.IndexJob.IdempotencyKey).Count(new(knowledge_model.IndexJob))
	require.NoError(t, err)
	assert.EqualValues(t, 1, jobCount)

	loadedSpace := &knowledge_model.Space{ID: space.ID}
	has, err := db.GetEngine(t.Context()).Get(loadedSpace)
	require.NoError(t, err)
	require.True(t, has)
	assert.EqualValues(t, 1, loadedSpace.PublicationGeneration)

	loadedDocument := &knowledge_model.Document{ID: document.ID}
	has, err = db.GetEngine(t.Context()).Get(loadedDocument)
	require.NoError(t, err)
	require.True(t, has)
	assert.Equal(t, knowledge_model.GovernanceStatusApproved, loadedDocument.GovernanceStatus)
	assert.Equal(t, revision.ID, loadedDocument.CurrentRevisionID)
	assert.Zero(t, loadedDocument.CurrentPublicationID, "approval creates a candidate and must not activate it")

	assert.Equal(t, knowledge_model.GovernanceStatusApproved, first.Publication.GovernanceStatus)
	assert.Equal(t, knowledge_model.ValidityStatusScheduled, first.Publication.ValidityStatus)
	assert.Equal(t, knowledge_model.IndexStatusQueued, first.Publication.IndexStatus)
	assert.False(t, first.Publication.IsCurrent, "evaluation and explicit activation are required before publication becomes current")
	assert.EqualValues(t, 1, first.Publication.Generation)
	assert.Equal(t, options.ACLPolicyVersion, first.Publication.ACLPolicyVersion)
	assert.Equal(t, revision.ContentSHA256, first.IndexJob.RevisionSHA256)
	assert.Equal(t, options.EngineProfileVersion, first.IndexJob.EngineProfileVersion)
	assert.Equal(t, knowledge_model.IndexJobTypeUpsert, first.IndexJob.JobType)

	wantKey := knowledge_model.BuildIndexIdempotencyKey(knowledge_model.IndexIdempotencyKeyInput{
		OwnerID:              space.OwnerID,
		SpaceID:              space.ID,
		PublicationID:        first.Publication.ID,
		Generation:           first.Publication.Generation,
		RevisionSHA256:       revision.ContentSHA256,
		EngineProfileVersion: options.EngineProfileVersion,
		JobType:              knowledge_model.IndexJobTypeUpsert,
	})
	assert.NotEmpty(t, wantKey)
	assert.Equal(t, wantKey, first.IndexJob.IdempotencyKey)
	assert.Equal(t, wantKey, first.Outbox.IdempotencyKey)
}

func TestApproveRevisionRejectsSelfApprovalWithoutPartialWrites(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	_, _, _, approval := insertPendingApproval(t, 1)
	result, err := knowledge_model.ApproveRevision(t.Context(), knowledge_model.ApproveRevisionOptions{
		ApprovalID:           approval.ID,
		DecidedBy:            approval.RequestedBy,
		Comment:              "must not approve own revision",
		EngineProfileVersion: "ragflow-v0.26.4-profile-1",
		ACLPolicyVersion:     4,
		TraceID:              "trace-self-approval-001",
	})
	require.Error(t, err)
	assert.Nil(t, result)

	publicationCount, countErr := db.GetEngine(t.Context()).Where("approval_id = ?", approval.ID).Count(new(knowledge_model.Publication))
	require.NoError(t, countErr)
	assert.EqualValues(t, 0, publicationCount)

	loadedApproval := &knowledge_model.Approval{ID: approval.ID}
	has, getErr := db.GetEngine(t.Context()).Get(loadedApproval)
	require.NoError(t, getErr)
	require.True(t, has)
	assert.Equal(t, knowledge_model.ApprovalStatusPending, loadedApproval.Status)
	assert.Zero(t, loadedApproval.DecidedBy)
}

func TestRejectRevisionRecordsTerminalDecision(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	_, document, revision, approval := insertPendingApproval(t, 1)

	err := knowledge_model.RejectRevision(t.Context(), knowledge_model.RejectRevisionOptions{
		ApprovalID: approval.ID,
		DecidedBy:  2,
		Comment:    "缺少脱敏依据",
		TraceID:    "trace-reject-approval-001",
	})
	require.NoError(t, err)

	loadedApproval := new(knowledge_model.Approval)
	found, err := db.GetEngine(t.Context()).ID(approval.ID).Get(loadedApproval)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, knowledge_model.ApprovalStatusRejected, loadedApproval.Status)
	assert.EqualValues(t, 2, loadedApproval.DecidedBy)

	loadedDocument := new(knowledge_model.Document)
	found, err = db.GetEngine(t.Context()).ID(document.ID).Get(loadedDocument)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, knowledge_model.GovernanceStatusRejected, loadedDocument.GovernanceStatus)
	assert.EqualValues(t, revision.ID, loadedDocument.CurrentRevisionID)

	publicationCount, err := db.GetEngine(t.Context()).Where("approval_id = ?", approval.ID).Count(new(knowledge_model.Publication))
	require.NoError(t, err)
	assert.Zero(t, publicationCount)
}

func insertPendingApproval(t *testing.T, requestedBy int64) (*knowledge_model.Space, *knowledge_model.Document, *knowledge_model.Revision, *knowledge_model.Approval) {
	t.Helper()

	suffix := fmt.Sprintf("%d", requestedBy)
	space := &knowledge_model.Space{
		OwnerID:              2,
		RepoID:               10000 + requestedBy,
		Name:                 "Production knowledge " + suffix,
		Slug:                 "production-knowledge-" + suffix,
		Status:               knowledge_model.SpaceStatusActive,
		RevocationGeneration: 1,
		CreatedBy:            requestedBy,
	}
	require.NoError(t, db.Insert(t.Context(), space))

	document := &knowledge_model.Document{
		SpaceID:          space.ID,
		Title:            "Masked policy " + suffix,
		RepoPath:         "knowledge/masked-policy-" + suffix + ".md",
		MIMEType:         "text/markdown",
		GovernanceStatus: knowledge_model.GovernanceStatusPending,
		CreatedBy:        requestedBy,
	}
	require.NoError(t, db.Insert(t.Context(), document))

	revision := &knowledge_model.Revision{
		DocumentID:          document.ID,
		RevisionNo:          1,
		FileName:            "masked-policy-" + suffix + ".md",
		RepoPath:            document.RepoPath,
		ContentSHA256:       "5bb1b064554ad5b0c665e1a437b62d85a1ce28e5e54b593f4f874b3b84d6e60b",
		Size:                29,
		GitCommitSHA:        "0123456789abcdef0123456789abcdef01234567",
		MaskPolicyVersion:   "mask-policy-2026-01",
		MaskManifestJSON:    `{"masked":true,"findings":2}`,
		SourceAuthorization: "repo-write-grant:2",
		CreatedBy:           requestedBy,
	}
	require.NoError(t, db.Insert(t.Context(), revision))

	document.CurrentRevisionID = revision.ID
	_, err := db.GetEngine(t.Context()).ID(document.ID).Cols("current_revision_id").Update(document)
	require.NoError(t, err)

	approval := &knowledge_model.Approval{
		RevisionID:  revision.ID,
		Status:      knowledge_model.ApprovalStatusPending,
		RequestedBy: requestedBy,
	}
	require.NoError(t, db.Insert(t.Context(), approval))
	return space, document, revision, approval
}
