// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"code.gitea.io/gitea/modules/timeutil"

	"xorm.io/xorm"
)

// AddKnowledgeBaseTables creates the governed knowledge-base tables.
func AddKnowledgeBaseTables(x *xorm.Engine) error {
	type KnowledgeSpace struct {
		ID                    int64 `xorm:"pk autoincr"`
		OwnerID               int64 `xorm:"index unique(owner_slug)"`
		RepoID                int64 `xorm:"unique"`
		Name                  string
		Slug                  string `xorm:"unique(owner_slug)"`
		Description           string
		Status                string `xorm:"index"`
		PublicationGeneration int64
		RevocationGeneration  int64
		CreatedBy             int64
		CreatedUnix           timeutil.TimeStamp `xorm:"created"`
		UpdatedUnix           timeutil.TimeStamp `xorm:"updated"`
	}
	type KnowledgeDocument struct {
		ID                   int64 `xorm:"pk autoincr"`
		SpaceID              int64 `xorm:"index unique(space_repo_path)"`
		Title                string
		RepoPath             string `xorm:"unique(space_repo_path)"`
		MIMEType             string
		GovernanceStatus     string `xorm:"index"`
		CurrentRevisionID    int64
		CurrentPublicationID int64
		CreatedBy            int64
		CreatedUnix          timeutil.TimeStamp `xorm:"created"`
		UpdatedUnix          timeutil.TimeStamp `xorm:"updated"`
	}
	type KnowledgeRevision struct {
		ID                  int64 `xorm:"pk autoincr"`
		DocumentID          int64 `xorm:"index unique(document_revision) unique(document_content)"`
		RevisionNo          int64 `xorm:"unique(document_revision)"`
		FileName            string
		RepoPath            string
		ContentSHA256       string `xorm:"varchar(64) unique(document_content)"`
		Size                int64
		GitCommitSHA        string
		MaskPolicyVersion   string
		MaskManifestJSON    string `xorm:"TEXT"`
		SourceAuthorization string
		CreatedBy           int64
		CreatedUnix         timeutil.TimeStamp `xorm:"created"`
	}
	type KnowledgeACL struct {
		ID            int64 `xorm:"pk autoincr"`
		SpaceID       int64 `xorm:"index"`
		SubjectType   string
		SubjectID     int64
		Permission    string
		Effect        string
		PolicyVersion int64 `xorm:"index"`
	}
	type KnowledgeApproval struct {
		ID            int64  `xorm:"pk autoincr"`
		RevisionID    int64  `xorm:"unique"`
		Status        string `xorm:"index"`
		RequestedBy   int64
		RequestedUnix timeutil.TimeStamp `xorm:"created"`
		DecidedBy     int64
		DecidedUnix   timeutil.TimeStamp
		Comment       string `xorm:"TEXT"`
	}
	type KnowledgePublication struct {
		ID                   int64  `xorm:"pk autoincr"`
		SpaceID              int64  `xorm:"index unique(space_generation)"`
		DocumentID           int64  `xorm:"index"`
		RevisionID           int64  `xorm:"index"`
		ApprovalID           int64  `xorm:"unique"`
		Generation           int64  `xorm:"unique(space_generation)"`
		GovernanceStatus     string `xorm:"index"`
		ValidityStatus       string `xorm:"index"`
		IndexStatus          string `xorm:"index"`
		ACLPolicyVersion     int64
		RevocationGeneration int64
		IsCurrent            bool
		EffectiveUnix        timeutil.TimeStamp
		ExpiresUnix          timeutil.TimeStamp
		CreatedUnix          timeutil.TimeStamp `xorm:"created"`
		UpdatedUnix          timeutil.TimeStamp `xorm:"updated"`
	}
	type KnowledgeRevocationTombstone struct {
		ID                    int64 `xorm:"pk autoincr"`
		PublicationID         int64 `xorm:"unique"`
		SpaceID               int64 `xorm:"index"`
		PublicationGeneration int64
		RevocationGeneration  int64
		EngineProfileVersion  string
		ActorID               int64
		Reason                string
		TraceID               string
		CreatedUnix           timeutil.TimeStamp `xorm:"created"`
	}
	type KnowledgeIndexBinding struct {
		ID                   int64 `xorm:"pk autoincr"`
		SpaceID              int64 `xorm:"index"`
		DocumentID           int64
		RevisionID           int64
		PublicationID        int64 `xorm:"unique(publication_engine_profile)"`
		Engine               string
		EngineProfileVersion string `xorm:"unique(publication_engine_profile)"`
		DatasetID            string
		EngineDocumentID     string
		ContentSHA256        string `xorm:"varchar(64)"`
		Status               string
		LastSuccessUnix      timeutil.TimeStamp
	}
	type KnowledgeIndexJob struct {
		ID                   int64 `xorm:"pk autoincr"`
		PublicationID        int64 `xorm:"index"`
		JobType              string
		IdempotencyKey       string `xorm:"varchar(512) unique"`
		Status               string `xorm:"index"`
		RevisionSHA256       string `xorm:"varchar(64)"`
		EngineProfileVersion string
		Attempt              int
		MaxAttempts          int
		NextAttemptUnix      timeutil.TimeStamp
		LastError            string             `xorm:"TEXT"`
		CreatedUnix          timeutil.TimeStamp `xorm:"created"`
		UpdatedUnix          timeutil.TimeStamp `xorm:"updated"`
	}
	type KnowledgeOutbox struct {
		ID              int64 `xorm:"pk autoincr"`
		AggregateType   string
		AggregateID     int64
		EventType       string
		IdempotencyKey  string `xorm:"varchar(512) unique"`
		PayloadJSON     string `xorm:"TEXT"`
		Status          string `xorm:"index"`
		Attempt         int
		NextAttemptUnix timeutil.TimeStamp
		CreatedUnix     timeutil.TimeStamp `xorm:"created"`
		UpdatedUnix     timeutil.TimeStamp `xorm:"updated"`
	}
	type KnowledgeAuditEvent struct {
		ID           int64 `xorm:"pk autoincr"`
		ActorID      int64
		Action       string
		EntityType   string
		EntityID     int64
		SpaceID      int64 `xorm:"index"`
		Result       string
		ReasonCode   string
		TraceID      string
		MetadataJSON string             `xorm:"TEXT"`
		CreatedUnix  timeutil.TimeStamp `xorm:"created index"`
	}

	return x.Sync2(
		new(KnowledgeSpace),
		new(KnowledgeDocument),
		new(KnowledgeRevision),
		new(KnowledgeACL),
		new(KnowledgeApproval),
		new(KnowledgePublication),
		new(KnowledgeRevocationTombstone),
		new(KnowledgeIndexBinding),
		new(KnowledgeIndexJob),
		new(KnowledgeOutbox),
		new(KnowledgeAuditEvent),
	)
}
