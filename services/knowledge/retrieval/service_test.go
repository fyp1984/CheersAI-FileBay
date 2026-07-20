// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package retrieval

import (
	"testing"
	"time"

	"code.gitea.io/gitea/models/db"
	knowledge_model "code.gitea.io/gitea/models/knowledge"
	"code.gitea.io/gitea/models/unittest"
	"code.gitea.io/gitea/modules/setting"
	"code.gitea.io/gitea/modules/timeutil"

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
	_, err = db.GetEngine(t.Context()).ID(binding.ID).Cols("enabled").Update(&knowledge_model.DifyBinding{Enabled: false})
	require.NoError(t, err)
	_, err = LookupDifyBinding(t.Context(), token, "dify-kb-customer")
	assert.Error(t, err, "a disabled Dify grant must fail before retrieval")
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

func TestLoadRetrievablePublicationsFailsClosedAfterWithdrawalOrExpiry(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	previousSettings := setting.Knowledge
	setting.Knowledge = setting.KnowledgeSetting{
		EngineProfileVersion: "retrieval-acceptance-profile",
		RAGFlowDatasetID:     "retrieval-acceptance-dataset",
	}
	t.Cleanup(func() { setting.Knowledge = previousSettings })

	now := timeutil.TimeStamp(time.Now().Unix())
	space := &knowledge_model.Space{
		OwnerID:              1,
		RepoID:               20992,
		Name:                 "检索验收空间",
		Slug:                 "retrieval-acceptance-space",
		Status:               knowledge_model.SpaceStatusActive,
		RevocationGeneration: 3,
		CreatedBy:            1,
	}
	require.NoError(t, db.Insert(t.Context(), space))
	source := &knowledge_model.DataSource{
		SpaceID:       space.ID,
		Name:          "已审批脱敏资料",
		SourceCode:    "retrieval-acceptance-source",
		Status:        knowledge_model.DataSourceStatusEnabled,
		SecurityLevel: knowledge_model.SecurityLevelInternal,
		EffectiveUnix: now - 60,
		CreatedBy:     1,
	}
	require.NoError(t, db.Insert(t.Context(), source))
	document := &knowledge_model.Document{
		SpaceID:      space.ID,
		DataSourceID: source.ID,
		Title:        "可撤回的脱敏资料",
		RepoPath:     "knowledge/acceptance.md",
		MIMEType:     "text/markdown",
		CreatedBy:    1,
	}
	require.NoError(t, db.Insert(t.Context(), document))
	revision := &knowledge_model.Revision{
		DocumentID:          document.ID,
		RevisionNo:          1,
		FileName:            "acceptance.md",
		RepoPath:            document.RepoPath,
		ContentSHA256:       "5bb1b064554ad5b0c665e1a437b62d85a1ce28e5e54b593f4f874b3b84d6e60b",
		Size:                16,
		GitCommitSHA:        "0123456789abcdef0123456789abcdef01234567",
		MaskPolicyVersion:   "mask-policy-2026-07",
		MaskManifestJSON:    `{"schema_version":1,"masked":true,"findings_count":0,"categories":[]}`,
		SourceAuthorization: "repo-write-grant:1",
		SecurityLevel:       knowledge_model.SecurityLevelInternal,
		CreatedBy:           1,
	}
	require.NoError(t, db.Insert(t.Context(), revision))
	publication := &knowledge_model.Publication{
		SpaceID:              space.ID,
		DocumentID:           document.ID,
		RevisionID:           revision.ID,
		Generation:           1,
		GovernanceStatus:     knowledge_model.GovernanceStatusApproved,
		ValidityStatus:       knowledge_model.ValidityStatusCurrent,
		IndexStatus:          knowledge_model.IndexStatusSearchable,
		IsCurrent:            true,
		EffectiveUnix:        now - 60,
		ExpiresUnix:          now + 60,
		RevocationGeneration: space.RevocationGeneration,
	}
	require.NoError(t, db.Insert(t.Context(), publication))
	document.CurrentPublicationID = publication.ID
	_, err := db.GetEngine(t.Context()).ID(document.ID).Cols("current_publication_id").Update(document)
	require.NoError(t, err)
	require.NoError(t, db.Insert(t.Context(), &knowledge_model.IndexBinding{
		SpaceID:              space.ID,
		DocumentID:           document.ID,
		RevisionID:           revision.ID,
		PublicationID:        publication.ID,
		Engine:               "ragflow",
		EngineProfileVersion: setting.Knowledge.EngineProfileVersion,
		DatasetID:            setting.Knowledge.RAGFlowDatasetID,
		EngineDocumentID:     "retrieval-acceptance-document",
		ContentSHA256:        revision.ContentSHA256,
		Status:               knowledge_model.IndexStatusSearchable,
	}))

	items, err := loadRetrievablePublications(t.Context(), space, knowledge_model.SecurityLevelInternal)
	require.NoError(t, err)
	require.Len(t, items, 1)

	_, err = db.GetEngine(t.Context()).ID(publication.ID).Cols("validity_status").Update(&knowledge_model.Publication{ValidityStatus: knowledge_model.ValidityStatusUnpublished})
	require.NoError(t, err)
	items, err = loadRetrievablePublications(t.Context(), space, knowledge_model.SecurityLevelInternal)
	require.NoError(t, err)
	assert.Empty(t, items, "withdrawn knowledge must disappear before derived-index deletion")

	_, err = db.GetEngine(t.Context()).ID(publication.ID).Cols("validity_status").Update(&knowledge_model.Publication{ValidityStatus: knowledge_model.ValidityStatusCurrent})
	require.NoError(t, err)
	_, err = db.GetEngine(t.Context()).ID(source.ID).Cols("expires_unix").Update(&knowledge_model.DataSource{ExpiresUnix: now})
	require.NoError(t, err)
	items, err = loadRetrievablePublications(t.Context(), space, knowledge_model.SecurityLevelInternal)
	require.NoError(t, err)
	assert.Empty(t, items, "expired data sources must fail closed even before cleanup revokes their index")
}
