// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"code.gitea.io/gitea/modules/timeutil"

	"xorm.io/xorm"
)

// AddKnowledgeTrialGovernanceTables adds internal-trial retrieval, review,
// connector-run and Dify application binding records. No secrets or content
// bodies are persisted in these tables.
func AddKnowledgeTrialGovernanceTables(x *xorm.Engine) error {
	type KnowledgeReviewCheck struct {
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
	type KnowledgeSyncRun struct {
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
	type KnowledgeDifyBinding struct {
		ID               int64 `xorm:"pk autoincr"`
		Name             string
		DifyKnowledgeID  string `xorm:"unique"`
		SpaceID          int64  `xorm:"index"`
		MaxSecurityLevel string
		TokenSHA256      string `xorm:"varchar(64) unique"`
		TokenHint        string
		Enabled          bool `xorm:"index"`
		CreatedBy        int64
		CreatedUnix      timeutil.TimeStamp `xorm:"created"`
		UpdatedUnix      timeutil.TimeStamp `xorm:"updated"`
	}
	type KnowledgeRetrievalEvent struct {
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
	type KnowledgeFeedback struct {
		ID            int64 `xorm:"pk autoincr"`
		ActorID       int64
		SpaceID       int64  `xorm:"index"`
		PublicationID int64  `xorm:"index"`
		Category      string `xorm:"index"`
		Rating        int
		Comment       string             `xorm:"TEXT"`
		CreatedUnix   timeutil.TimeStamp `xorm:"created"`
	}
	return x.Sync2(new(KnowledgeReviewCheck), new(KnowledgeSyncRun), new(KnowledgeDifyBinding), new(KnowledgeRetrievalEvent), new(KnowledgeFeedback))
}
