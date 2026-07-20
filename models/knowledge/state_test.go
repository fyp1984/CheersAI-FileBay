// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package knowledge_test

import (
	"testing"
	"time"

	knowledge_model "code.gitea.io/gitea/models/knowledge"
	"code.gitea.io/gitea/modules/timeutil"

	"github.com/stretchr/testify/assert"
)

func TestCanTransitionAcceptsOnlyDeclaredLifecycleEdges(t *testing.T) {
	t.Parallel()

	draft := knowledge_model.LifecycleState{
		Governance: knowledge_model.GovernanceStatusDraft,
		Validity:   knowledge_model.ValidityStatusUnpublished,
		Index:      knowledge_model.IndexStatusUnindexed,
	}
	pending := draft
	pending.Governance = knowledge_model.GovernanceStatusPending
	rejected := draft
	rejected.Governance = knowledge_model.GovernanceStatusRejected
	approved := knowledge_model.LifecycleState{
		Governance: knowledge_model.GovernanceStatusApproved,
		Validity:   knowledge_model.ValidityStatusScheduled,
		Index:      knowledge_model.IndexStatusUnindexed,
	}
	queued := approved
	queued.Index = knowledge_model.IndexStatusQueued
	indexing := queued
	indexing.Index = knowledge_model.IndexStatusIndexing
	evaluation := indexing
	evaluation.Index = knowledge_model.IndexStatusEvaluation
	searchable := evaluation
	searchable.Validity = knowledge_model.ValidityStatusCurrent
	searchable.Index = knowledge_model.IndexStatusSearchable
	unpublished := searchable
	unpublished.Validity = knowledge_model.ValidityStatusUnpublished
	deleting := unpublished
	deleting.Index = knowledge_model.IndexStatusDeleting
	deleted := deleting
	deleted.Index = knowledge_model.IndexStatusDeleted
	failed := indexing
	failed.Index = knowledge_model.IndexStatusFailed

	allowed := []struct {
		name string
		from knowledge_model.LifecycleState
		to   knowledge_model.LifecycleState
	}{
		{"submit draft", draft, pending},
		{"reject pending", pending, rejected},
		{"approve pending", pending, approved},
		{"queue approved publication", approved, queued},
		{"start indexing", queued, indexing},
		{"finish indexing", indexing, evaluation},
		{"activate evaluated publication", evaluation, searchable},
		{"unpublish searchable publication", searchable, unpublished},
		{"start derived deletion", unpublished, deleting},
		{"finish derived deletion", deleting, deleted},
		{"record indexing failure", indexing, failed},
		{"retry failed indexing", failed, queued},
	}

	for _, tt := range allowed {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, knowledge_model.CanTransition(tt.from, tt.to))
		})
	}
}

func TestCanTransitionRejectsSkippedAndMixedDimensionEdges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		from knowledge_model.LifecycleState
		to   knowledge_model.LifecycleState
	}{
		{
			name: "draft cannot become searchable",
			from: knowledge_model.LifecycleState{Governance: knowledge_model.GovernanceStatusDraft, Validity: knowledge_model.ValidityStatusUnpublished, Index: knowledge_model.IndexStatusUnindexed},
			to:   knowledge_model.LifecycleState{Governance: knowledge_model.GovernanceStatusApproved, Validity: knowledge_model.ValidityStatusCurrent, Index: knowledge_model.IndexStatusSearchable},
		},
		{
			name: "pending cannot queue before approval",
			from: knowledge_model.LifecycleState{Governance: knowledge_model.GovernanceStatusPending, Validity: knowledge_model.ValidityStatusUnpublished, Index: knowledge_model.IndexStatusUnindexed},
			to:   knowledge_model.LifecycleState{Governance: knowledge_model.GovernanceStatusPending, Validity: knowledge_model.ValidityStatusUnpublished, Index: knowledge_model.IndexStatusQueued},
		},
		{
			name: "indexing cannot silently widen validity",
			from: knowledge_model.LifecycleState{Governance: knowledge_model.GovernanceStatusApproved, Validity: knowledge_model.ValidityStatusScheduled, Index: knowledge_model.IndexStatusIndexing},
			to:   knowledge_model.LifecycleState{Governance: knowledge_model.GovernanceStatusApproved, Validity: knowledge_model.ValidityStatusCurrent, Index: knowledge_model.IndexStatusFailed},
		},
		{
			name: "failed old generation cannot self activate",
			from: knowledge_model.LifecycleState{Governance: knowledge_model.GovernanceStatusApproved, Validity: knowledge_model.ValidityStatusScheduled, Index: knowledge_model.IndexStatusFailed},
			to:   knowledge_model.LifecycleState{Governance: knowledge_model.GovernanceStatusApproved, Validity: knowledge_model.ValidityStatusCurrent, Index: knowledge_model.IndexStatusSearchable},
		},
		{
			name: "searchable cannot delete without synchronous unpublish",
			from: knowledge_model.LifecycleState{Governance: knowledge_model.GovernanceStatusApproved, Validity: knowledge_model.ValidityStatusCurrent, Index: knowledge_model.IndexStatusSearchable},
			to:   knowledge_model.LifecycleState{Governance: knowledge_model.GovernanceStatusApproved, Validity: knowledge_model.ValidityStatusCurrent, Index: knowledge_model.IndexStatusDeleting},
		},
		{
			name: "deleted cannot be resurrected",
			from: knowledge_model.LifecycleState{Governance: knowledge_model.GovernanceStatusApproved, Validity: knowledge_model.ValidityStatusUnpublished, Index: knowledge_model.IndexStatusDeleted},
			to:   knowledge_model.LifecycleState{Governance: knowledge_model.GovernanceStatusApproved, Validity: knowledge_model.ValidityStatusScheduled, Index: knowledge_model.IndexStatusQueued},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.False(t, knowledge_model.CanTransition(tt.from, tt.to))
		})
	}
}

func TestPublicationIsPublishedFailsClosedForEveryRequiredDimension(t *testing.T) {
	t.Parallel()

	now := timeutil.TimeStamp(time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC).Unix())
	base := knowledge_model.Publication{
		ID:                   77,
		GovernanceStatus:     knowledge_model.GovernanceStatusApproved,
		ValidityStatus:       knowledge_model.ValidityStatusCurrent,
		IndexStatus:          knowledge_model.IndexStatusSearchable,
		RevocationGeneration: 9,
		IsCurrent:            true,
		EffectiveUnix:        now - 60,
		ExpiresUnix:          now + 60,
	}

	assert.True(t, base.IsPublished(now, base.ID, 9))

	tests := []struct {
		name                 string
		mutate               func(*knowledge_model.Publication)
		currentPublicationID int64
		currentRevocation    int64
	}{
		{"draft", func(p *knowledge_model.Publication) { p.GovernanceStatus = knowledge_model.GovernanceStatusDraft }, base.ID, 9},
		{"pending", func(p *knowledge_model.Publication) { p.GovernanceStatus = knowledge_model.GovernanceStatusPending }, base.ID, 9},
		{"rejected", func(p *knowledge_model.Publication) { p.GovernanceStatus = knowledge_model.GovernanceStatusRejected }, base.ID, 9},
		{"withdrawn", func(p *knowledge_model.Publication) { p.GovernanceStatus = knowledge_model.GovernanceStatusWithdrawn }, base.ID, 9},
		{"scheduled", func(p *knowledge_model.Publication) { p.ValidityStatus = knowledge_model.ValidityStatusScheduled }, base.ID, 9},
		{"expired status", func(p *knowledge_model.Publication) { p.ValidityStatus = knowledge_model.ValidityStatusExpired }, base.ID, 9},
		{"unpublished", func(p *knowledge_model.Publication) { p.ValidityStatus = knowledge_model.ValidityStatusUnpublished }, base.ID, 9},
		{"archived", func(p *knowledge_model.Publication) { p.ValidityStatus = knowledge_model.ValidityStatusArchived }, base.ID, 9},
		{"not searchable", func(p *knowledge_model.Publication) { p.IndexStatus = knowledge_model.IndexStatusEvaluation }, base.ID, 9},
		{"not current flag", func(p *knowledge_model.Publication) { p.IsCurrent = false }, base.ID, 9},
		{"effective time in future", func(p *knowledge_model.Publication) { p.EffectiveUnix = now + 1 }, base.ID, 9},
		{"expired by time", func(p *knowledge_model.Publication) { p.ExpiresUnix = now }, base.ID, 9},
		{"current pointer mismatch", func(*knowledge_model.Publication) {}, base.ID + 1, 9},
		{"revocation generation mismatch", func(*knowledge_model.Publication) {}, base.ID, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			publication := base
			tt.mutate(&publication)
			assert.False(t, publication.IsPublished(now, tt.currentPublicationID, tt.currentRevocation))
		})
	}

	withoutExpiry := base
	withoutExpiry.ExpiresUnix = 0
	assert.True(t, withoutExpiry.IsPublished(now, base.ID, 9), "zero expiry represents no scheduled expiration")
}
