// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package knowledge

import (
	"code.gitea.io/gitea/models/db"
	"code.gitea.io/gitea/modules/timeutil"
)

// SpaceStatus is the governance state of a knowledge space.
type SpaceStatus string

const (
	SpaceStatusActive   SpaceStatus = "active"
	SpaceStatusArchived SpaceStatus = "archived"
)

// DataSourceType identifies the governed origin of knowledge artifacts.
type DataSourceType string

const (
	DataSourceTypePolicy         DataSourceType = "policy"
	DataSourceTypeProcedure      DataSourceType = "procedure"
	DataSourceTypeProduct        DataSourceType = "product"
	DataSourceTypeTraining       DataSourceType = "training"
	DataSourceTypeFAQ            DataSourceType = "faq"
	DataSourceTypeCase           DataSourceType = "case"
	DataSourceTypeTicket         DataSourceType = "ticket"
	DataSourceTypePublicMaterial DataSourceType = "public_material"
	DataSourceTypeDatabase       DataSourceType = "database"
	DataSourceTypeAPI            DataSourceType = "api"
	DataSourceTypeFileRepository DataSourceType = "file_repository"
)

// DataSourceStatus is the independent governance state of a data source.
type DataSourceStatus string

const (
	DataSourceStatusDraft         DataSourceStatus = "draft"
	DataSourceStatusPendingReview DataSourceStatus = "pending_review"
	DataSourceStatusApproved      DataSourceStatus = "approved"
	DataSourceStatusEnabled       DataSourceStatus = "enabled"
	DataSourceStatusSuspended     DataSourceStatus = "suspended"
	DataSourceStatusExpired       DataSourceStatus = "expired"
	DataSourceStatusArchived      DataSourceStatus = "archived"
)

// SecurityLevel controls the maximum audience of a knowledge source.
type SecurityLevel string

const (
	SecurityLevelPublic     SecurityLevel = "public"
	SecurityLevelInternal   SecurityLevel = "internal"
	SecurityLevelRestricted SecurityLevel = "restricted"
	SecurityLevelProhibited SecurityLevel = "prohibited"
)

// ReviewFrequency describes the required source freshness review cadence.
type ReviewFrequency string

const (
	ReviewFrequencyWeekly         ReviewFrequency = "weekly"
	ReviewFrequencyMonthly        ReviewFrequency = "monthly"
	ReviewFrequencyQuarterly      ReviewFrequency = "quarterly"
	ReviewFrequencyEventTriggered ReviewFrequency = "event_triggered"
)

// SearchabilityStatus records whether an artifact can be used by retrieval.
type SearchabilityStatus string

const (
	SearchabilityStatusSearchable    SearchabilityStatus = "searchable"
	SearchabilityStatusOCRRequired   SearchabilityStatus = "ocr_required"
	SearchabilityStatusNotSearchable SearchabilityStatus = "not_searchable"
)

// GovernanceStatus describes the approval state of governed knowledge.
type GovernanceStatus string

const (
	GovernanceStatusDraft     GovernanceStatus = "draft"
	GovernanceStatusPending   GovernanceStatus = "pending"
	GovernanceStatusRejected  GovernanceStatus = "rejected"
	GovernanceStatusApproved  GovernanceStatus = "approved"
	GovernanceStatusWithdrawn GovernanceStatus = "withdrawn"
)

// ValidityStatus describes whether a publication is currently valid.
type ValidityStatus string

const (
	ValidityStatusScheduled   ValidityStatus = "scheduled"
	ValidityStatusCurrent     ValidityStatus = "current"
	ValidityStatusExpired     ValidityStatus = "expired"
	ValidityStatusUnpublished ValidityStatus = "unpublished"
	ValidityStatusArchived    ValidityStatus = "archived"
)

// IndexStatus describes the state of a derived search index.
type IndexStatus string

const (
	IndexStatusUnindexed  IndexStatus = "unindexed"
	IndexStatusQueued     IndexStatus = "queued"
	IndexStatusIndexing   IndexStatus = "indexing"
	IndexStatusEvaluation IndexStatus = "evaluation"
	IndexStatusSearchable IndexStatus = "searchable"
	IndexStatusFailed     IndexStatus = "failed"
	IndexStatusSuperseded IndexStatus = "superseded"
	IndexStatusDeleting   IndexStatus = "deleting"
	IndexStatusDeleted    IndexStatus = "deleted"
)

// ApprovalStatus describes an immutable approval decision.
type ApprovalStatus string

const (
	ApprovalStatusPending  ApprovalStatus = "pending"
	ApprovalStatusApproved ApprovalStatus = "approved"
	ApprovalStatusRejected ApprovalStatus = "rejected"
)

// IndexJobType describes work performed against a derived index.
type IndexJobType string

const (
	IndexJobTypeUpsert  IndexJobType = "upsert"
	IndexJobTypePoll    IndexJobType = "poll"
	IndexJobTypeDelete  IndexJobType = "delete"
	IndexJobTypeRebuild IndexJobType = "rebuild"
)

// IndexJobStatus describes worker execution state.
type IndexJobStatus string

const (
	IndexJobStatusQueued    IndexJobStatus = "queued"
	IndexJobStatusRunning   IndexJobStatus = "running"
	IndexJobStatusRetry     IndexJobStatus = "retry"
	IndexJobStatusSucceeded IndexJobStatus = "succeeded"
	IndexJobStatusFailed    IndexJobStatus = "failed"
	IndexJobStatusCancelled IndexJobStatus = "cancelled"
)

// OutboxStatus describes dispatch state for a transactional outbox event.
type OutboxStatus string

const (
	OutboxStatusPending    OutboxStatus = "pending"
	OutboxStatusDispatched OutboxStatus = "dispatched"
	OutboxStatusFailed     OutboxStatus = "failed"
)

// Space is the authoritative governance boundary and repository binding.
type Space struct {
	ID                    int64 `xorm:"pk autoincr"`
	OwnerID               int64 `xorm:"index unique(owner_slug)"`
	RepoID                int64 `xorm:"unique"`
	Name                  string
	Slug                  string `xorm:"unique(owner_slug)"`
	Description           string
	Status                SpaceStatus `xorm:"index"`
	PublicationGeneration int64
	RevocationGeneration  int64
	CreatedBy             int64
	CreatedUnix           timeutil.TimeStamp `xorm:"created"`
	UpdatedUnix           timeutil.TimeStamp `xorm:"updated"`
}

func (*Space) TableName() string { return "knowledge_space" }

// DataSource is the mandatory governed register entry for one knowledge origin.
// Credential values are never stored here; connector authentication only uses a
// server-side secret reference.
type DataSource struct {
	ID                     int64  `xorm:"pk autoincr"`
	SpaceID                int64  `xorm:"index unique(space_source_code)"`
	SourceCode             string `xorm:"varchar(128) unique(space_source_code)"`
	Name                   string
	SourceType             DataSourceType `xorm:"index"`
	BusinessDomainCode     string         `xorm:"index"`
	ContentTypeCode        string         `xorm:"index"`
	SourceDepartment       string
	ContentOwnerID         int64              `xorm:"index"`
	MaintenanceOwnerID     int64              `xorm:"index"`
	EffectiveUnix          timeutil.TimeStamp `xorm:"index"`
	ExpiresUnix            timeutil.TimeStamp `xorm:"index"`
	ReviewFrequency        ReviewFrequency
	SecurityLevel          SecurityLevel `xorm:"index"`
	ApprovalBasisRef       string
	ApplicableScopeJSON    string `xorm:"TEXT"`
	SourceAuthorizationRef string
	SourceFormat           string
	SearchabilityStatus    SearchabilityStatus
	Status                 DataSourceStatus `xorm:"index"`
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

func (*DataSource) TableName() string { return "knowledge_data_source" }

// ReviewCheck is an append-only evidence record for one required review.
// A revision is not eligible for approval until all six review types passed.
type ReviewCheck struct {
	ID          int64  `xorm:"pk autoincr"`
	RevisionID  int64  `xorm:"index unique(revision_review_type)"`
	ReviewType  string `xorm:"varchar(32) unique(revision_review_type)"`
	Status      string `xorm:"index"`
	Comment     string `xorm:"TEXT"`
	CheckedBy   int64
	CheckedUnix timeutil.TimeStamp
	CreatedUnix timeutil.TimeStamp `xorm:"created"`
	UpdatedUnix timeutil.TimeStamp `xorm:"updated"`
}

func (*ReviewCheck) TableName() string { return "knowledge_review_check" }

// SyncRun is the durable, content-free execution history of a controlled
// connector synchronization. Connector records only reference secrets.
type SyncRun struct {
	ID              int64  `xorm:"pk autoincr"`
	DataSourceID    int64  `xorm:"index"`
	Status          string `xorm:"index"`
	Mode            string
	StartedBy       int64
	StartedUnix     timeutil.TimeStamp
	FinishedUnix    timeutil.TimeStamp
	RecordsRead     int64
	DocumentsQueued int64
	LastCursor      string
	ErrorCode       string
	CreatedUnix     timeutil.TimeStamp `xorm:"created"`
	UpdatedUnix     timeutil.TimeStamp `xorm:"updated"`
}

func (*SyncRun) TableName() string { return "knowledge_sync_run" }

// DifyBinding grants one external Dify application a fixed, non-impersonating
// retrieval scope. TokenSHA256 is the only token representation persisted.
type DifyBinding struct {
	ID               int64 `xorm:"pk autoincr"`
	Name             string
	DifyKnowledgeID  string `xorm:"unique"`
	SpaceID          int64  `xorm:"index"`
	MaxSecurityLevel SecurityLevel
	TokenSHA256      string `xorm:"varchar(64) unique"`
	TokenHint        string
	Enabled          bool `xorm:"index"`
	CreatedBy        int64
	CreatedUnix      timeutil.TimeStamp `xorm:"created"`
	UpdatedUnix      timeutil.TimeStamp `xorm:"updated"`
}

func (*DifyBinding) TableName() string { return "knowledge_dify_binding" }

// RetrievalEvent stores aggregate-safe search telemetry. Query content and
// returned chunks are deliberately excluded; QuerySHA256 is non-reversible.
type RetrievalEvent struct {
	ID          int64 `xorm:"pk autoincr"`
	ActorID     int64
	BindingID   int64  `xorm:"index"`
	SpaceID     int64  `xorm:"index"`
	QuerySHA256 string `xorm:"varchar(64)"`
	ResultCount int
	Outcome     string `xorm:"index"`
	ReasonCode  string
	CreatedUnix timeutil.TimeStamp `xorm:"created index"`
}

func (*RetrievalEvent) TableName() string { return "knowledge_retrieval_event" }

// Feedback records structured user feedback without copying answer content.
type Feedback struct {
	ID            int64 `xorm:"pk autoincr"`
	ActorID       int64
	SpaceID       int64  `xorm:"index"`
	PublicationID int64  `xorm:"index"`
	Category      string `xorm:"index"`
	Rating        int
	Comment       string             `xorm:"TEXT"`
	CreatedUnix   timeutil.TimeStamp `xorm:"created"`
}

func (*Feedback) TableName() string { return "knowledge_feedback" }

// Document is a logical knowledge document within a space.
type Document struct {
	ID                   int64 `xorm:"pk autoincr"`
	SpaceID              int64 `xorm:"index unique(space_repo_path)"`
	DataSourceID         int64 `xorm:"index"`
	Title                string
	RepoPath             string `xorm:"unique(space_repo_path)"`
	MIMEType             string
	GovernanceStatus     GovernanceStatus `xorm:"index"`
	CurrentRevisionID    int64
	CurrentPublicationID int64
	CreatedBy            int64
	CreatedUnix          timeutil.TimeStamp `xorm:"created"`
	UpdatedUnix          timeutil.TimeStamp `xorm:"updated"`
}

func (*Document) TableName() string { return "knowledge_document" }

// Revision is an immutable reference to a masked repository artifact.
type Revision struct {
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
	BusinessDomainCode  string
	ContentTypeCode     string
	SecurityLevel       SecurityLevel
	ApplicableScopeJSON string `xorm:"TEXT"`
	VersionNo           string
	RevisionRelation    string
	SourceDepartment    string
	ContentOwnerID      int64
	MaintenanceOwnerID  int64
	ApprovalBasisRef    string
	EffectiveUnix       timeutil.TimeStamp
	ExpiresUnix         timeutil.TimeStamp
	SearchabilityStatus SearchabilityStatus
	CreatedBy           int64
	CreatedUnix         timeutil.TimeStamp `xorm:"created"`
}

func (*Revision) TableName() string { return "knowledge_revision" }

// ACL is an explicit knowledge policy entry. Explicit deny takes precedence in
// authorization services; the model stores the versioned policy declaration.
type ACL struct {
	ID            int64 `xorm:"pk autoincr"`
	SpaceID       int64 `xorm:"index"`
	SubjectType   string
	SubjectID     int64
	Permission    string
	Effect        string
	PolicyVersion int64 `xorm:"index"`
}

func (*ACL) TableName() string { return "knowledge_acl" }

// Approval is one append-only review decision for a revision.
type Approval struct {
	ID            int64          `xorm:"pk autoincr"`
	RevisionID    int64          `xorm:"unique"`
	Status        ApprovalStatus `xorm:"index"`
	RequestedBy   int64
	RequestedUnix timeutil.TimeStamp `xorm:"created"`
	DecidedBy     int64
	DecidedUnix   timeutil.TimeStamp
	Comment       string `xorm:"TEXT"`
}

func (*Approval) TableName() string { return "knowledge_approval" }

// Publication is an immutable approved snapshot plus its derived-index state.
type Publication struct {
	ID                   int64            `xorm:"pk autoincr"`
	SpaceID              int64            `xorm:"index unique(space_generation)"`
	DocumentID           int64            `xorm:"index"`
	RevisionID           int64            `xorm:"index"`
	ApprovalID           int64            `xorm:"unique"`
	Generation           int64            `xorm:"unique(space_generation)"`
	GovernanceStatus     GovernanceStatus `xorm:"index"`
	ValidityStatus       ValidityStatus   `xorm:"index"`
	IndexStatus          IndexStatus      `xorm:"index"`
	ACLPolicyVersion     int64
	RevocationGeneration int64
	IsCurrent            bool
	EffectiveUnix        timeutil.TimeStamp
	ExpiresUnix          timeutil.TimeStamp
	CreatedUnix          timeutil.TimeStamp `xorm:"created"`
	UpdatedUnix          timeutil.TimeStamp `xorm:"updated"`
}

func (*Publication) TableName() string { return "knowledge_publication" }

// RevocationTombstone is the durable deny record created before derived index
// deletion. One publication can be revoked only once.
type RevocationTombstone struct {
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

func (*RevocationTombstone) TableName() string { return "knowledge_revocation_tombstone" }

// IndexBinding maps an authoritative FileBay publication to a derived engine.
type IndexBinding struct {
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
	Status               IndexStatus
	LastSuccessUnix      timeutil.TimeStamp
}

func (*IndexBinding) TableName() string { return "knowledge_index_binding" }

// IndexJob is durable work for a derived index.
type IndexJob struct {
	ID                   int64 `xorm:"pk autoincr"`
	PublicationID        int64 `xorm:"index"`
	JobType              IndexJobType
	IdempotencyKey       string         `xorm:"varchar(512) unique"`
	Status               IndexJobStatus `xorm:"index"`
	RevisionSHA256       string         `xorm:"varchar(64)"`
	EngineProfileVersion string
	Attempt              int
	MaxAttempts          int
	NextAttemptUnix      timeutil.TimeStamp
	LastError            string             `xorm:"TEXT"`
	CreatedUnix          timeutil.TimeStamp `xorm:"created"`
	UpdatedUnix          timeutil.TimeStamp `xorm:"updated"`
}

func (*IndexJob) TableName() string { return "knowledge_index_job" }

// Outbox is a transactional, durable event waiting for dispatch.
type Outbox struct {
	ID              int64 `xorm:"pk autoincr"`
	AggregateType   string
	AggregateID     int64
	EventType       string
	IdempotencyKey  string       `xorm:"varchar(512) unique"`
	PayloadJSON     string       `xorm:"TEXT"`
	Status          OutboxStatus `xorm:"index"`
	Attempt         int
	NextAttemptUnix timeutil.TimeStamp
	CreatedUnix     timeutil.TimeStamp `xorm:"created"`
	UpdatedUnix     timeutil.TimeStamp `xorm:"updated"`
}

func (*Outbox) TableName() string { return "knowledge_outbox" }

// AuditEvent is an append-only, content-free governance audit record.
type AuditEvent struct {
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

func (*AuditEvent) TableName() string { return "knowledge_audit_event" }

func init() {
	db.RegisterModel(new(Space))
	db.RegisterModel(new(DataSource))
	db.RegisterModel(new(Document))
	db.RegisterModel(new(Revision))
	db.RegisterModel(new(ACL))
	db.RegisterModel(new(Approval))
	db.RegisterModel(new(Publication))
	db.RegisterModel(new(RevocationTombstone))
	db.RegisterModel(new(IndexBinding))
	db.RegisterModel(new(IndexJob))
	db.RegisterModel(new(Outbox))
	db.RegisterModel(new(AuditEvent))
	db.RegisterModel(new(ReviewCheck))
	db.RegisterModel(new(SyncRun))
	db.RegisterModel(new(DifyBinding))
	db.RegisterModel(new(RetrievalEvent))
	db.RegisterModel(new(Feedback))
}
