// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package retrieval

import (
	"testing"

	"code.gitea.io/gitea/models/db"
	knowledge_model "code.gitea.io/gitea/models/knowledge"
	"code.gitea.io/gitea/models/unittest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveQualityTaskWritesOneAuditRecord(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	event := &knowledge_model.RetrievalEvent{ActorID: 2, SpaceID: 1, QuerySHA256: "quality-task-query", Outcome: "empty", ReasonCode: "no_authorized_citation"}
	require.NoError(t, db.Insert(t.Context(), event))

	require.NoError(t, ResolveQualityTask(t.Context(), 1, qualityTaskNoAnswer, event.ID))
	require.NoError(t, ResolveQualityTask(t.Context(), 1, qualityTaskNoAnswer, event.ID), "repeat handling must be idempotent")
	count, err := db.GetEngine(t.Context()).Where("action = ? AND entity_type = ? AND entity_id = ?", qualityTaskResolved, "retrieval_event", event.ID).Count(new(knowledge_model.AuditEvent))
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestResolveQualityTaskRejectsHighRatingFeedback(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	feedback := &knowledge_model.Feedback{ActorID: 2, SpaceID: 1, PublicationID: 1, Category: "知识正确性", Rating: 5}
	require.NoError(t, db.Insert(t.Context(), feedback))
	assert.Error(t, ResolveQualityTask(t.Context(), 1, qualityTaskLowFeedback, feedback.ID))
}
