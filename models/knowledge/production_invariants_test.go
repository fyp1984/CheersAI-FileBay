// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package knowledge_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"code.gitea.io/gitea/models/db"
	knowledge_model "code.gitea.io/gitea/models/knowledge"
	"code.gitea.io/gitea/models/unittest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicationGenerationIsScopedToTheTargetDocumentLifecycle(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	space, documentA, publicationA, documentB, publicationB := insertTwoDocumentPublications(t, false)

	err := knowledge_model.ActivatePublication(t.Context(), knowledge_model.ActivatePublicationOptions{
		PublicationID:                 publicationA.ID,
		ActorID:                       1,
		ExpectedPublicationGeneration: publicationA.Generation,
		ExpectedRevocationGeneration:  publicationA.RevocationGeneration,
		TraceID:                       "trace-activate-document-a",
	})
	require.NoError(t, err, "a newer generation for document B must not strand document A's evaluated candidate")

	loadedA := loadPublication(t, publicationA.ID)
	assert.True(t, loadedA.IsCurrent)
	assert.Equal(t, publicationA.ID, loadDocument(t, documentA.ID).CurrentPublicationID)
	assert.True(t, loadPublication(t, publicationB.ID).IsPublished(loadedA.EffectiveUnix, publicationB.ID, space.RevocationGeneration))
	assert.Equal(t, publicationB.ID, loadDocument(t, documentB.ID).CurrentPublicationID)
}

func TestUnpublishIsPublicationScopedAndDoesNotRevokeOtherDocuments(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	space, documentA, publicationA, documentB, publicationB := insertTwoDocumentPublications(t, true)

	err := knowledge_model.UnpublishPublication(t.Context(), knowledge_model.UnpublishPublicationOptions{
		PublicationID:                 publicationA.ID,
		ActorID:                       1,
		ExpectedPublicationGeneration: publicationA.Generation,
		ExpectedRevocationGeneration:  publicationA.RevocationGeneration,
		EngineProfileVersion:          "ragflow-v0.26.4-profile-1",
		TraceID:                       "trace-unpublish-document-a",
		Reason:                        "governance-withdrawal",
	})
	require.NoError(t, err)

	loadedSpace := loadSpace(t, space.ID)
	assert.Equal(t, space.RevocationGeneration, loadedSpace.RevocationGeneration, "publication-scoped unpublish must not advance the space-wide ACL revocation generation")
	assert.Zero(t, loadDocument(t, documentA.ID).CurrentPublicationID)
	assert.Equal(t, publicationB.ID, loadDocument(t, documentB.ID).CurrentPublicationID)
	assert.True(t, loadPublication(t, publicationB.ID).IsPublished(1, publicationB.ID, loadedSpace.RevocationGeneration), "unpublishing document A must not make document B unavailable")
}

func TestUnpublishReplayRejectsMismatchedOperationIdentity(t *testing.T) {
	mutations := []struct {
		name   string
		mutate func(*knowledge_model.UnpublishPublicationOptions)
	}{
		{"engine profile", func(opts *knowledge_model.UnpublishPublicationOptions) { opts.EngineProfileVersion = "other-profile" }},
		{"publication generation", func(opts *knowledge_model.UnpublishPublicationOptions) { opts.ExpectedPublicationGeneration++ }},
		{"revocation generation", func(opts *knowledge_model.UnpublishPublicationOptions) { opts.ExpectedRevocationGeneration++ }},
		{"reason", func(opts *knowledge_model.UnpublishPublicationOptions) { opts.Reason = "different-reason" }},
	}
	for _, tt := range mutations {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, unittest.PrepareTestDatabase())
			space, _, publication := insertCurrentPublicationFixture(t)
			opts := knowledge_model.UnpublishPublicationOptions{
				PublicationID:                 publication.ID,
				ActorID:                       1,
				ExpectedPublicationGeneration: publication.Generation,
				ExpectedRevocationGeneration:  space.RevocationGeneration,
				EngineProfileVersion:          "ragflow-v0.26.4-profile-1",
				TraceID:                       "trace-unpublish-replay",
				Reason:                        "governance-withdrawal",
			}
			require.NoError(t, knowledge_model.UnpublishPublication(t.Context(), opts))
			tt.mutate(&opts)
			assert.ErrorIs(t, knowledge_model.UnpublishPublication(t.Context(), opts), knowledge_model.ErrPublicationConflict)
		})
	}
}

func TestGovernanceOperationsRejectSensitiveOrUnboundedText(t *testing.T) {
	unsafeValues := []string{
		`C:\Users\Alice\private\source.docx`,
		"api_key=ragflow-production-secret",
		"unsafe\ncontrol",
		strings.Repeat("x", 4097),
	}
	for index, unsafe := range unsafeValues {
		t.Run("approval", func(t *testing.T) {
			require.NoError(t, unittest.PrepareTestDatabase())
			_, _, _, approval := insertPendingApproval(t, 2)
			result, err := knowledge_model.ApproveRevision(t.Context(), knowledge_model.ApproveRevisionOptions{
				ApprovalID: approval.ID, DecidedBy: 1, Comment: unsafe,
				EngineProfileVersion: "ragflow-v0.26.4-profile-1", ACLPolicyVersion: 4,
				TraceID: "trace-approval-safe",
			})
			assert.ErrorIs(t, err, knowledge_model.ErrApprovalConflict, "unsafe value case %d", index)
			assert.Nil(t, result)
		})

		t.Run("approval trace", func(t *testing.T) {
			require.NoError(t, unittest.PrepareTestDatabase())
			_, _, _, approval := insertPendingApproval(t, 2)
			result, err := knowledge_model.ApproveRevision(t.Context(), knowledge_model.ApproveRevisionOptions{
				ApprovalID: approval.ID, DecidedBy: 1, Comment: "approved",
				EngineProfileVersion: "ragflow-v0.26.4-profile-1", ACLPolicyVersion: 4,
				TraceID: unsafe,
			})
			assert.ErrorIs(t, err, knowledge_model.ErrApprovalConflict, "unsafe value case %d", index)
			assert.Nil(t, result)
		})

		t.Run("activation trace", func(t *testing.T) {
			require.NoError(t, unittest.PrepareTestDatabase())
			space, _, _, candidate := insertActivationFixture(t)
			err := knowledge_model.ActivatePublication(t.Context(), knowledge_model.ActivatePublicationOptions{
				PublicationID: candidate.ID, ActorID: 1,
				ExpectedPublicationGeneration: candidate.Generation,
				ExpectedRevocationGeneration:  space.RevocationGeneration,
				TraceID:                       unsafe,
			})
			assert.ErrorIs(t, err, knowledge_model.ErrPublicationConflict, "unsafe value case %d", index)
		})

		t.Run("unpublish reason", func(t *testing.T) {
			require.NoError(t, unittest.PrepareTestDatabase())
			space, _, publication := insertCurrentPublicationFixture(t)
			err := knowledge_model.UnpublishPublication(t.Context(), knowledge_model.UnpublishPublicationOptions{
				PublicationID: publication.ID, ActorID: 1,
				ExpectedPublicationGeneration: publication.Generation,
				ExpectedRevocationGeneration:  space.RevocationGeneration,
				EngineProfileVersion:          "ragflow-v0.26.4-profile-1",
				TraceID:                       "trace-unpublish-safe",
				Reason:                        unsafe,
			})
			assert.ErrorIs(t, err, knowledge_model.ErrPublicationConflict, "unsafe value case %d", index)
		})

		t.Run("unpublish trace", func(t *testing.T) {
			require.NoError(t, unittest.PrepareTestDatabase())
			space, _, publication := insertCurrentPublicationFixture(t)
			err := knowledge_model.UnpublishPublication(t.Context(), knowledge_model.UnpublishPublicationOptions{
				PublicationID: publication.ID, ActorID: 1,
				ExpectedPublicationGeneration: publication.Generation,
				ExpectedRevocationGeneration:  space.RevocationGeneration,
				EngineProfileVersion:          "ragflow-v0.26.4-profile-1",
				TraceID:                       unsafe,
				Reason:                        "governance-withdrawal",
			})
			assert.ErrorIs(t, err, knowledge_model.ErrPublicationConflict, "unsafe value case %d", index)
		})
	}
}

func TestConcurrentApprovalIsIdempotent(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	_, _, _, approval := insertPendingApproval(t, 2)
	opts := knowledge_model.ApproveRevisionOptions{ApprovalID: approval.ID, DecidedBy: 1, Comment: "approved", EngineProfileVersion: "ragflow-v0.26.4-profile-1", ACLPolicyVersion: 4, TraceID: "trace-concurrent-approval"}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := knowledge_model.ApproveRevision(t.Context(), opts)
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	count, err := db.GetEngine(t.Context()).Where("approval_id = ?", approval.ID).Count(new(knowledge_model.Publication))
	require.NoError(t, err)
	assert.EqualValues(t, 1, count)
}

func TestConcurrentActivationHasOneCASWinnerAndConsistentPointer(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	space, document, oldCurrent, candidate := insertActivationFixture(t)
	opts := knowledge_model.ActivatePublicationOptions{PublicationID: candidate.ID, ActorID: 1, ExpectedPublicationGeneration: candidate.Generation, ExpectedRevocationGeneration: space.RevocationGeneration, TraceID: "trace-concurrent-activate"}

	errorsSeen := runConcurrentErrors(t, 2, func() error { return knowledge_model.ActivatePublication(t.Context(), opts) })
	successes := 0
	for _, err := range errorsSeen {
		if err == nil {
			successes++
			continue
		}
		assert.True(t, errors.Is(err, knowledge_model.ErrPublicationConflict), "loser must be a deterministic CAS conflict, got %v", err)
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, candidate.ID, loadDocument(t, document.ID).CurrentPublicationID)
	assert.True(t, loadPublication(t, candidate.ID).IsCurrent)
	assert.False(t, loadPublication(t, oldCurrent.ID).IsCurrent)
}

func TestConcurrentUnpublishIsIdempotent(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	space, document, publication := insertCurrentPublicationFixture(t)
	opts := knowledge_model.UnpublishPublicationOptions{PublicationID: publication.ID, ActorID: 1, ExpectedPublicationGeneration: publication.Generation, ExpectedRevocationGeneration: space.RevocationGeneration, EngineProfileVersion: "ragflow-v0.26.4-profile-1", TraceID: "trace-concurrent-unpublish", Reason: "governance-withdrawal"}

	for _, err := range runConcurrentErrors(t, 2, func() error { return knowledge_model.UnpublishPublication(t.Context(), opts) }) {
		require.NoError(t, err)
	}
	assert.Zero(t, loadDocument(t, document.ID).CurrentPublicationID)
	count, err := db.GetEngine(t.Context()).Where("publication_id = ?", publication.ID).Count(new(knowledge_model.RevocationTombstone))
	require.NoError(t, err)
	assert.EqualValues(t, 1, count)
}

func runConcurrentErrors(t *testing.T, count int, operation func() error) []error {
	t.Helper()
	start := make(chan struct{})
	results := make(chan error, count)
	var wait sync.WaitGroup
	for range count {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			results <- operation()
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	values := make([]error, 0, count)
	for err := range results {
		values = append(values, err)
	}
	return values
}

func insertTwoDocumentPublications(t *testing.T, bothCurrent bool) (*knowledge_model.Space, *knowledge_model.Document, *knowledge_model.Publication, *knowledge_model.Document, *knowledge_model.Publication) {
	t.Helper()
	space := &knowledge_model.Space{OwnerID: 2, RepoID: 13001, Name: "Multi document knowledge", Slug: "multi-document-knowledge", Status: knowledge_model.SpaceStatusActive, PublicationGeneration: 2, RevocationGeneration: 3, CreatedBy: 2}
	require.NoError(t, db.Insert(t.Context(), space))

	createPublication := func(title, repoPath, sha string, generation int64, current bool) (*knowledge_model.Document, *knowledge_model.Publication) {
		document := &knowledge_model.Document{SpaceID: space.ID, Title: title, RepoPath: repoPath, MIMEType: "text/markdown", GovernanceStatus: knowledge_model.GovernanceStatusApproved, CreatedBy: 2}
		require.NoError(t, db.Insert(t.Context(), document))
		revision := &knowledge_model.Revision{DocumentID: document.ID, RevisionNo: 1, FileName: title + ".md", RepoPath: repoPath, ContentSHA256: sha, Size: 10, GitCommitSHA: "0123456789abcdef0123456789abcdef01234567", MaskPolicyVersion: "mask-policy-2026-01", MaskManifestJSON: `{"schema_version":1,"masked":true,"findings_count":0,"categories":[]}`, SourceAuthorization: "grant-id:grant_2", CreatedBy: 2}
		require.NoError(t, db.Insert(t.Context(), revision))
		approval := &knowledge_model.Approval{RevisionID: revision.ID, Status: knowledge_model.ApprovalStatusApproved, RequestedBy: 2, DecidedBy: 1}
		require.NoError(t, db.Insert(t.Context(), approval))
		validity, index := knowledge_model.ValidityStatusScheduled, knowledge_model.IndexStatusEvaluation
		if current {
			validity, index = knowledge_model.ValidityStatusCurrent, knowledge_model.IndexStatusSearchable
		}
		publication := &knowledge_model.Publication{SpaceID: space.ID, DocumentID: document.ID, RevisionID: revision.ID, ApprovalID: approval.ID, Generation: generation, GovernanceStatus: knowledge_model.GovernanceStatusApproved, ValidityStatus: validity, IndexStatus: index, ACLPolicyVersion: 4, RevocationGeneration: space.RevocationGeneration, IsCurrent: current, EffectiveUnix: 1}
		require.NoError(t, db.Insert(t.Context(), publication))
		document.CurrentRevisionID = revision.ID
		if current {
			document.CurrentPublicationID = publication.ID
		}
		_, err := db.GetEngine(t.Context()).ID(document.ID).Cols("current_revision_id", "current_publication_id").Update(document)
		require.NoError(t, err)
		return document, publication
	}

	documentA, publicationA := createPublication("document-a", "knowledge/document-a.md", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 1, bothCurrent)
	documentB, publicationB := createPublication("document-b", "knowledge/document-b.md", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", 2, true)
	return space, documentA, publicationA, documentB, publicationB
}
