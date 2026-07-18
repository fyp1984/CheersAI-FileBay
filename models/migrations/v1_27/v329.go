// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"code.gitea.io/gitea/modules/timeutil"

	"xorm.io/xorm"
)

// AddKnowledgeDataSourceGovernance adds the governed data-source register and
// immutable document metadata fields required for enterprise knowledge intake.
func AddKnowledgeDataSourceGovernance(x *xorm.Engine) error {
	type KnowledgeDataSource struct {
		ID                     int64  `xorm:"pk autoincr"`
		SpaceID                int64  `xorm:"index unique(space_source_code)"`
		SourceCode             string `xorm:"varchar(128) unique(space_source_code)"`
		Name                   string
		SourceType             string `xorm:"index"`
		BusinessDomainCode     string `xorm:"index"`
		ContentTypeCode        string `xorm:"index"`
		SourceDepartment       string
		ContentOwnerID         int64              `xorm:"index"`
		MaintenanceOwnerID     int64              `xorm:"index"`
		EffectiveUnix          timeutil.TimeStamp `xorm:"index"`
		ExpiresUnix            timeutil.TimeStamp `xorm:"index"`
		ReviewFrequency        string
		SecurityLevel          string `xorm:"index"`
		ApprovalBasisRef       string
		ApplicableScopeJSON    string `xorm:"TEXT"`
		SourceAuthorizationRef string
		SourceFormat           string
		SearchabilityStatus    string
		Status                 string `xorm:"index"`
		ConnectorType          string
		SecretRef              string
		ReadScope              string `xorm:"TEXT"`
		QueryTemplateRef       string
		SchemaVersion          string
		FieldMappingJSON       string `xorm:"TEXT"`
		IncrementalCursorField string
		DataRetentionRule      string
		MaskPolicyVersion      string
		LastSyncUnix           timeutil.TimeStamp
		LastSuccessCursor      string
		CreatedBy              int64
		CreatedUnix            timeutil.TimeStamp `xorm:"created"`
		UpdatedUnix            timeutil.TimeStamp `xorm:"updated"`
	}
	type KnowledgeDocument struct {
		ID           int64 `xorm:"pk autoincr"`
		DataSourceID int64 `xorm:"index"`
	}
	type KnowledgeRevision struct {
		ID                  int64 `xorm:"pk autoincr"`
		BusinessDomainCode  string
		ContentTypeCode     string
		SecurityLevel       string
		ApplicableScopeJSON string `xorm:"TEXT"`
		VersionNo           string
		RevisionRelation    string
		SourceDepartment    string
		ContentOwnerID      int64
		MaintenanceOwnerID  int64
		ApprovalBasisRef    string
		EffectiveUnix       timeutil.TimeStamp
		ExpiresUnix         timeutil.TimeStamp
		SearchabilityStatus string
	}

	return x.Sync2(
		new(KnowledgeDataSource),
		new(KnowledgeDocument),
		new(KnowledgeRevision),
	)
}
