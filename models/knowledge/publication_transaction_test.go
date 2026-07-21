// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package knowledge_test

import (
	"testing"

	"code.gitea.io/gitea/models/db"
	knowledge_model "code.gitea.io/gitea/models/knowledge"
	"code.gitea.io/gitea/models/unittest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestActivatePublicationAtomicallySwitchesCurrentPointer(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	space, document, oldCurrent, candidate := insertActivationFixture(t)

	err := knowledge_model.ActivatePublication(t.Context(), knowledge_model.ActivatePublicationOptions{
		PublicationID:                 candidate.ID,
		ActorID:                       1,
		ExpectedPublicationGeneration: candidate.Generation,
		ExpectedRevocationGeneration:  space.RevocationGeneration,
		TraceID:                       "trace-activate-001",
	})
	require.NoError(t, err)

	loadedDocument := loadDocument(t, document.ID)
	assert.Equal(t, candidate.ID, loadedDocument.CurrentPublicationID)

	loadedCandidate := loadPublication(t, candidate.ID)
	assert.True(t, loadedCandidate.IsCurrent)
	assert.Equal(t, knowledge_model.ValidityStatusCurrent, loadedCandidate.ValidityStatus)
	assert.Equal(t, knowledge_model.IndexStatusSearchable, loadedCandidate.IndexStatus)
	assert.True(t, loadedCandidate.IsPublished(loadedCandidate.EffectiveUnix, loadedDocument.CurrentPublicationID, space.RevocationGeneration))

	loadedOld := loadPublication(t, oldCurrent.ID)
	assert.False(t, loadedOld.IsCurrent)
	assert.Equal(t, knowledge_model.IndexStatusSuperseded, loadedOld.IndexStatus)
	assert.False(t, loadedOld.IsPublished(loadedCandidate.EffectiveUnix, loadedDocument.CurrentPublicationID, space.RevocationGeneration))
}

func TestActivatePublicationAtomicallyPromotesIndexBinding(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	space, document, oldCurrent, candidate := insertActivationFixture(t)
	binding := &knowledge_model.IndexBinding{
		SpaceID:              space.ID,
		DocumentID:           document.ID,
		RevisionID:           candidate.RevisionID,
		PublicationID:        candidate.ID,
		Engine:               "ragflow",
		EngineProfileVersion: "ragflow-test-profile",
		DatasetID:            "dataset-test",
		EngineDocumentID:     "document-test",
		ContentSHA256:        "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Status:               knowledge_model.IndexStatusEvaluation,
	}
	require.NoError(t, db.Insert(t.Context(), binding))

	err := knowledge_model.ActivatePublication(t.Context(), knowledge_model.ActivatePublicationOptions{
		PublicationID:                 candidate.ID,
		ActorID:                       1,
		ExpectedPublicationGeneration: candidate.Generation,
		ExpectedRevocationGeneration:  space.RevocationGeneration,
		EngineProfileVersion:          binding.EngineProfileVersion,
		TraceID:                       "trace-activate-binding-001",
	})
	require.NoError(t, err)

	loadedBinding := &knowledge_model.IndexBinding{ID: binding.ID}
	has, err := db.GetEngine(t.Context()).Get(loadedBinding)
	require.NoError(t, err)
	require.True(t, has)
	assert.Equal(t, knowledge_model.IndexStatusSearchable, loadedBinding.Status)
	assert.True(t, loadPublication(t, candidate.ID).IsCurrent)
	assert.False(t, loadPublication(t, oldCurrent.ID).IsCurrent)
}

func TestActivatePublicationRollsBackWhenConfiguredBindingIsMissing(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	space, document, oldCurrent, candidate := insertActivationFixture(t)

	err := knowledge_model.ActivatePublication(t.Context(), knowledge_model.ActivatePublicationOptions{
		PublicationID:                 candidate.ID,
		ActorID:                       1,
		ExpectedPublicationGeneration: candidate.Generation,
		ExpectedRevocationGeneration:  space.RevocationGeneration,
		EngineProfileVersion:          "ragflow-missing-binding",
		TraceID:                       "trace-activate-binding-missing",
	})
	require.ErrorIs(t, err, knowledge_model.ErrPublicationConflict)
	assert.Equal(t, oldCurrent.ID, loadDocument(t, document.ID).CurrentPublicationID)
	assert.True(t, loadPublication(t, oldCurrent.ID).IsCurrent)
	assert.False(t, loadPublication(t, candidate.ID).IsCurrent)
}

func TestActivatePublicationRejectsStaleGenerationWithoutPartialWrites(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		publicationGenerationDelta int64
		revocationGenerationDelta  int64
	}{
		{name: "publication generation mismatch", publicationGenerationDelta: -1},
		{name: "revocation generation mismatch", revocationGenerationDelta: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, unittest.PrepareTestDatabase())
			space, document, oldCurrent, candidate := insertActivationFixture(t)

			err := knowledge_model.ActivatePublication(t.Context(), knowledge_model.ActivatePublicationOptions{
				PublicationID:                 candidate.ID,
				ActorID:                       1,
				ExpectedPublicationGeneration: candidate.Generation + tc.publicationGenerationDelta,
				ExpectedRevocationGeneration:  space.RevocationGeneration + tc.revocationGenerationDelta,
				TraceID:                       "trace-stale-activate",
			})
			require.Error(t, err)
			assert.Equal(t, oldCurrent.ID, loadDocument(t, document.ID).CurrentPublicationID)
			assert.True(t, loadPublication(t, oldCurrent.ID).IsCurrent)
			assert.False(t, loadPublication(t, candidate.ID).IsCurrent)
			assert.Equal(t, knowledge_model.IndexStatusEvaluation, loadPublication(t, candidate.ID).IndexStatus)
		})
	}
}

func TestUnpublishPublicationCreatesOneDenyTombstoneAndDeleteOperation(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	space, document, current := insertCurrentPublicationFixture(t)

	options := knowledge_model.UnpublishPublicationOptions{
		PublicationID:                 current.ID,
		ActorID:                       1,
		ExpectedPublicationGeneration: current.Generation,
		ExpectedRevocationGeneration:  space.RevocationGeneration,
		EngineProfileVersion:          "ragflow-v0.26.4-profile-1",
		TraceID:                       "trace-unpublish-001",
		Reason:                        "governance withdrawal",
	}
	require.NoError(t, knowledge_model.UnpublishPublication(t.Context(), options))
	require.NoError(t, knowledge_model.UnpublishPublication(t.Context(), options), "replay must be idempotent")

	loadedSpace := loadSpace(t, space.ID)
	assert.Equal(t, space.RevocationGeneration, loadedSpace.RevocationGeneration)
	assert.Zero(t, loadDocument(t, document.ID).CurrentPublicationID, "current pointer is synchronously revoked")

	loadedPublication := loadPublication(t, current.ID)
	assert.False(t, loadedPublication.IsCurrent)
	assert.Equal(t, knowledge_model.ValidityStatusUnpublished, loadedPublication.ValidityStatus)
	assert.False(t, loadedPublication.IsPublished(loadedPublication.EffectiveUnix, 0, loadedSpace.RevocationGeneration))

	tombstoneCount, err := db.GetEngine(t.Context()).Where("publication_id = ?", current.ID).Count(new(knowledge_model.RevocationTombstone))
	require.NoError(t, err)
	assert.EqualValues(t, 1, tombstoneCount)
	tombstone := new(knowledge_model.RevocationTombstone)
	has, err := db.GetEngine(t.Context()).Where("publication_id = ?", current.ID).Get(tombstone)
	require.NoError(t, err)
	require.True(t, has)
	assert.Equal(t, loadedSpace.RevocationGeneration, tombstone.RevocationGeneration)
	assert.Equal(t, current.Generation, tombstone.PublicationGeneration)
	assert.Equal(t, options.EngineProfileVersion, tombstone.EngineProfileVersion)
	assert.Equal(t, options.TraceID, tombstone.TraceID)

	jobCount, err := db.GetEngine(t.Context()).Where("publication_id = ? AND job_type = ?", current.ID, knowledge_model.IndexJobTypeDelete).Count(new(knowledge_model.IndexJob))
	require.NoError(t, err)
	assert.EqualValues(t, 1, jobCount)
	job := new(knowledge_model.IndexJob)
	has, err = db.GetEngine(t.Context()).Where("publication_id = ? AND job_type = ?", current.ID, knowledge_model.IndexJobTypeDelete).Get(job)
	require.NoError(t, err)
	require.True(t, has)
	assert.Equal(t, knowledge_model.IndexJobStatusQueued, job.Status)

	outboxCount, err := db.GetEngine(t.Context()).Where("idempotency_key = ?", job.IdempotencyKey).Count(new(knowledge_model.Outbox))
	require.NoError(t, err)
	assert.EqualValues(t, 1, outboxCount)
}

func TestUniqueKnowledgeConstraintsRejectDeterministicDuplicates(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	space, document, revision, approval := insertPendingApproval(t, 2)

	assert.Error(t, db.Insert(t.Context(), &knowledge_model.Space{OwnerID: space.OwnerID, RepoID: space.RepoID + 1, Name: "duplicate slug", Slug: space.Slug, Status: knowledge_model.SpaceStatusActive}))
	assert.Error(t, db.Insert(t.Context(), &knowledge_model.Space{OwnerID: space.OwnerID + 1, RepoID: space.RepoID, Name: "duplicate repo", Slug: "other-slug", Status: knowledge_model.SpaceStatusActive}))
	assert.Error(t, db.Insert(t.Context(), &knowledge_model.Document{SpaceID: document.SpaceID, RepoPath: document.RepoPath, GovernanceStatus: knowledge_model.GovernanceStatusDraft}))
	assert.Error(t, db.Insert(t.Context(), &knowledge_model.Revision{DocumentID: revision.DocumentID, RevisionNo: revision.RevisionNo, ContentSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}))
	assert.Error(t, db.Insert(t.Context(), &knowledge_model.Revision{DocumentID: revision.DocumentID, RevisionNo: revision.RevisionNo + 1, ContentSHA256: revision.ContentSHA256}))
	assert.Error(t, db.Insert(t.Context(), &knowledge_model.Approval{RevisionID: approval.RevisionID, Status: knowledge_model.ApprovalStatusPending, RequestedBy: 4}), "one revision has at most one active approval")
}

func insertActivationFixture(t *testing.T) (*knowledge_model.Space, *knowledge_model.Document, *knowledge_model.Publication, *knowledge_model.Publication) {
	t.Helper()
	space, document, oldCurrent := insertCurrentPublicationFixture(t)
	revision := &knowledge_model.Revision{DocumentID: document.ID, RevisionNo: 2, FileName: "candidate.md", RepoPath: document.RepoPath, ContentSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Size: 10, GitCommitSHA: "1123456789abcdef0123456789abcdef01234567", MaskPolicyVersion: "mask-policy-2026-01", MaskManifestJSON: `{"schema_version":1,"masked":true,"findings_count":0,"categories":[]}`, SourceAuthorization: "repo-write-grant:2", CreatedBy: 2}
	require.NoError(t, db.Insert(t.Context(), revision))
	approval := &knowledge_model.Approval{RevisionID: revision.ID, Status: knowledge_model.ApprovalStatusApproved, RequestedBy: 2, DecidedBy: 1}
	require.NoError(t, db.Insert(t.Context(), approval))
	candidate := &knowledge_model.Publication{SpaceID: space.ID, DocumentID: document.ID, RevisionID: revision.ID, ApprovalID: approval.ID, Generation: 2, GovernanceStatus: knowledge_model.GovernanceStatusApproved, ValidityStatus: knowledge_model.ValidityStatusScheduled, IndexStatus: knowledge_model.IndexStatusEvaluation, ACLPolicyVersion: 4, RevocationGeneration: space.RevocationGeneration, IsCurrent: false}
	require.NoError(t, db.Insert(t.Context(), candidate))
	space.PublicationGeneration = candidate.Generation
	_, err := db.GetEngine(t.Context()).ID(space.ID).Cols("publication_generation").Update(space)
	require.NoError(t, err)
	return space, document, oldCurrent, candidate
}

func insertCurrentPublicationFixture(t *testing.T) (*knowledge_model.Space, *knowledge_model.Document, *knowledge_model.Publication) {
	t.Helper()
	space := &knowledge_model.Space{OwnerID: 2, RepoID: 12001, Name: "Current knowledge", Slug: "current-knowledge", Status: knowledge_model.SpaceStatusActive, PublicationGeneration: 1, RevocationGeneration: 3, CreatedBy: 2}
	require.NoError(t, db.Insert(t.Context(), space))
	document := &knowledge_model.Document{SpaceID: space.ID, Title: "Policy", RepoPath: "knowledge/policy.md", MIMEType: "text/markdown", GovernanceStatus: knowledge_model.GovernanceStatusApproved, CreatedBy: 2}
	require.NoError(t, db.Insert(t.Context(), document))
	revision := &knowledge_model.Revision{DocumentID: document.ID, RevisionNo: 1, FileName: "policy.md", RepoPath: document.RepoPath, ContentSHA256: "5bb1b064554ad5b0c665e1a437b62d85a1ce28e5e54b593f4f874b3b84d6e60b", Size: 29, GitCommitSHA: "0123456789abcdef0123456789abcdef01234567", MaskPolicyVersion: "mask-policy-2026-01", MaskManifestJSON: `{"schema_version":1,"masked":true,"findings_count":2,"categories":["person_name"]}`, SourceAuthorization: "repo-write-grant:2", CreatedBy: 2}
	require.NoError(t, db.Insert(t.Context(), revision))
	approval := &knowledge_model.Approval{RevisionID: revision.ID, Status: knowledge_model.ApprovalStatusApproved, RequestedBy: 2, DecidedBy: 1}
	require.NoError(t, db.Insert(t.Context(), approval))
	publication := &knowledge_model.Publication{SpaceID: space.ID, DocumentID: document.ID, RevisionID: revision.ID, ApprovalID: approval.ID, Generation: 1, GovernanceStatus: knowledge_model.GovernanceStatusApproved, ValidityStatus: knowledge_model.ValidityStatusCurrent, IndexStatus: knowledge_model.IndexStatusSearchable, ACLPolicyVersion: 4, RevocationGeneration: space.RevocationGeneration, IsCurrent: true, EffectiveUnix: 1}
	require.NoError(t, db.Insert(t.Context(), publication))
	document.CurrentRevisionID = revision.ID
	document.CurrentPublicationID = publication.ID
	_, err := db.GetEngine(t.Context()).ID(document.ID).Cols("current_revision_id", "current_publication_id").Update(document)
	require.NoError(t, err)
	return space, document, publication
}

func loadSpace(t *testing.T, id int64) *knowledge_model.Space {
	t.Helper()
	value := &knowledge_model.Space{ID: id}
	has, err := db.GetEngine(t.Context()).Get(value)
	require.NoError(t, err)
	require.True(t, has)
	return value
}

func loadDocument(t *testing.T, id int64) *knowledge_model.Document {
	t.Helper()
	value := &knowledge_model.Document{ID: id}
	has, err := db.GetEngine(t.Context()).Get(value)
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
