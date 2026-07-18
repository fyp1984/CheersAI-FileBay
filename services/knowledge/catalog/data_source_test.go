// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package catalog

import (
	"testing"
	"time"

	"code.gitea.io/gitea/models/db"
	knowledge_model "code.gitea.io/gitea/models/knowledge"
	"code.gitea.io/gitea/models/unittest"
	"code.gitea.io/gitea/modules/timeutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "code.gitea.io/gitea/models"
)

func TestMain(m *testing.M) {
	unittest.MainTest(m)
}

func TestValidateDataSourceOptionsRequiresEnterpriseGovernanceFields(t *testing.T) {
	base := validManualDataSourceOptions()
	assert.NoError(t, validateDataSourceOptions(base))

	tests := []struct {
		name   string
		mutate func(*CreateDataSourceOptions)
	}{
		{"missing source code", func(value *CreateDataSourceOptions) { value.SourceCode = "" }},
		{"missing content owner", func(value *CreateDataSourceOptions) { value.ContentOwnerUsername = "" }},
		{"missing maintenance owner", func(value *CreateDataSourceOptions) { value.MaintenanceUsername = "" }},
		{"missing effective date", func(value *CreateDataSourceOptions) { value.EffectiveUnix = 0 }},
		{"missing approval basis", func(value *CreateDataSourceOptions) { value.ApprovalBasisRef = "" }},
		{"local path in approval basis", func(value *CreateDataSourceOptions) { value.ApprovalBasisRef = `C:\private\approval.pdf` }},
		{"missing authorization", func(value *CreateDataSourceOptions) { value.SourceAuthorizationRef = "" }},
		{"invalid applicable scope", func(value *CreateDataSourceOptions) { value.ApplicableScopeJSON = "all staff" }},
		{"expired before effective", func(value *CreateDataSourceOptions) { value.ExpiresUnix = value.EffectiveUnix }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value := base
			tt.mutate(&value)
			assert.Error(t, validateDataSourceOptions(value))
		})
	}
}

func TestValidateDataSourceOptionsRequiresControlledDatabaseConnector(t *testing.T) {
	base := validManualDataSourceOptions()
	base.SourceType = knowledge_model.DataSourceTypeDatabase
	base.ConnectorType = "mysql"
	base.SecretRef = "secret-ref:env/KB_MYSQL_DSN"
	base.ReadScope = `{"view":"v_kb_masked_faq"}`
	base.QueryTemplateRef = "approval-ref:query-template-001"
	base.SchemaVersion = "v1"
	base.FieldMappingJSON = `{"id":"record_id","title":"question","content":"answer","updated_at":"updated_at"}`
	base.IncrementalCursorField = "updated_at"
	base.DataRetentionRule = "retain-30-days"
	assert.NoError(t, validateDataSourceOptions(base))

	base.ConnectorType = "postgresql"
	assert.Error(t, validateDataSourceOptions(base), "the internal trial supports only MySQL")
	base.ConnectorType = "mysql"

	base.SecretRef = "postgres://reader:password@database/knowledge"
	assert.Error(t, validateDataSourceOptions(base), "connection strings and passwords must never be persisted")
}

func TestDocumentVersionAndRevisionRelationAreStrict(t *testing.T) {
	assert.True(t, validDocumentVersion("V1.0"))
	assert.True(t, validDocumentVersion("V10.2.3"))
	assert.False(t, validDocumentVersion("latest"))
	assert.False(t, validDocumentVersion("v1.0"))
	assert.True(t, validRevisionRelation("new"))
	assert.True(t, validRevisionRelation("replacement"))
	assert.False(t, validRevisionRelation("overwrite"))
}

func TestApprovalRequiresAllSixReviewChecks(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	for index, reviewType := range RequiredReviewTypes {
		status := "passed"
		if index == len(RequiredReviewTypes)-1 {
			status = "pending"
		}
		require.NoError(t, db.Insert(t.Context(), &knowledge_model.ReviewCheck{RevisionID: 99881, ReviewType: reviewType, Status: status}))
	}
	assert.Error(t, requireAllReviewsPassed(t.Context(), 99881))
	_, err := db.GetEngine(t.Context()).Where("revision_id = ? AND review_type = ?", 99881, "release").Cols("status").Update(&knowledge_model.ReviewCheck{Status: "passed"})
	require.NoError(t, err)
	assert.NoError(t, requireAllReviewsPassed(t.Context(), 99881))
}

func validManualDataSourceOptions() CreateDataSourceOptions {
	effective := timeutil.TimeStamp(time.Date(2026, time.July, 17, 0, 0, 0, 0, time.UTC).Unix())
	return CreateDataSourceOptions{
		SpaceID:                1,
		ActorID:                1,
		SourceCode:             "operation-policy-001",
		Name:                   "Customer complaint policy",
		SourceType:             knowledge_model.DataSourceTypePolicy,
		BusinessDomainCode:     "operation",
		ContentTypeCode:        "policy",
		SourceDepartment:       "Customer Operations",
		ContentOwnerUsername:   "knowledge_owner",
		MaintenanceUsername:    "knowledge_maintainer",
		EffectiveUnix:          effective,
		ReviewFrequency:        knowledge_model.ReviewFrequencyMonthly,
		SecurityLevel:          knowledge_model.SecurityLevelInternal,
		ApprovalBasisRef:       "approval-ref:policy-001",
		ApplicableScopeJSON:    `{"organization":"company"}`,
		SourceAuthorizationRef: "grant-id:policy-001",
		SourceFormat:           "text/markdown",
		SearchabilityStatus:    knowledge_model.SearchabilityStatusSearchable,
		MaskPolicyVersion:      "mask-policy-manual-v1",
	}
}
