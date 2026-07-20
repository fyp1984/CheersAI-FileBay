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

	_ "code.gitea.io/gitea/models"
)

func TestMain(m *testing.M) {
	unittest.MainTest(m)
}

func TestDifyBindingPersistsOnlyDigestAndRejectsWrongKnowledgeID(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	space := &knowledge_model.Space{OwnerID: 1, RepoID: 20991, Name: "Dify trial space", Slug: "dify-trial-space", Status: knowledge_model.SpaceStatusActive, RevocationGeneration: 1, CreatedBy: 1}
	require.NoError(t, db.Insert(t.Context(), space))
	binding, token, err := CreateDifyBinding(t.Context(), CreateDifyBindingOptions{ActorID: 1, Name: "客服助手", DifyKnowledgeID: "dify-kb-customer", SpaceID: space.ID, MaxSecurityLevel: knowledge_model.SecurityLevelInternal})
	require.NoError(t, err)
	assert.NotContains(t, binding.TokenSHA256, token)
	assert.NotEmpty(t, binding.TokenHint)

	loaded, err := LookupDifyBinding(t.Context(), token, "dify-kb-customer")
	require.NoError(t, err)
	assert.Equal(t, binding.ID, loaded.ID)
	_, err = LookupDifyBinding(t.Context(), token, "dify-kb-other")
	assert.Error(t, err)
	_, err = LookupDifyBinding(t.Context(), "dify_kb_wrong_token_value_0123456789", "dify-kb-customer")
	assert.Error(t, err)
}

func TestSecurityScopeFailsClosedForProhibitedKnowledge(t *testing.T) {
	assert.True(t, securityAllowed(knowledge_model.SecurityLevelInternal, knowledge_model.SecurityLevelRestricted))
	assert.False(t, securityAllowed(knowledge_model.SecurityLevelRestricted, knowledge_model.SecurityLevelInternal))
	assert.False(t, securityAllowed(knowledge_model.SecurityLevelProhibited, knowledge_model.SecurityLevelRestricted))
}

func TestRetrievalCandidateLimitLeavesRoomForGovernanceFiltering(t *testing.T) {
	assert.Equal(t, minRetrievalCandidates, retrievalCandidateLimit(1))
	assert.Equal(t, 20, retrievalCandidateLimit(5))
	assert.Equal(t, maxRetrievalCandidates, retrievalCandidateLimit(100))
}
