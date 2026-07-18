// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Package connector implements controlled structured-data ingestion. Its MySQL
// connector can read only an approved, de-identified view; it never accepts
// arbitrary SQL, raw tables, local paths or credential values from web forms.
package connector

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"code.gitea.io/gitea/models/db"
	knowledge_model "code.gitea.io/gitea/models/knowledge"
	user_model "code.gitea.io/gitea/models/user"
	"code.gitea.io/gitea/modules/timeutil"
	"code.gitea.io/gitea/services/knowledge/catalog"
	"code.gitea.io/gitea/services/knowledge/ingest"
)

const maxSyncRecords = 50

var sqlIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// MySQLReadScope is the fixed allowable origin of structured knowledge.
type MySQLReadScope struct {
	View string `json:"view"`
}

// MySQLFieldMapping maps approved view columns into a knowledge record.
type MySQLFieldMapping struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Content string `json:"content"`
	Cursor  string `json:"cursor"`
}

// RunResult contains only aggregate-safe connector execution information.
type RunResult struct {
	RunID           int64
	RecordsRead     int64
	DocumentsQueued int64
	LastCursor      string
}

// SyncMySQL reads one approved de-identified view and creates governed,
// pending-review Markdown documents. preview mode does not create documents.
func SyncMySQL(ctx context.Context, sourceID, actorID int64, preview bool) (*RunResult, error) {
	actor, err := user_model.GetUserByID(ctx, actorID)
	if err != nil || !actor.IsAdmin {
		return nil, errors.New("MySQL 同步需要站点管理员权限")
	}
	source, exists, err := db.GetByID[knowledge_model.DataSource](ctx, sourceID)
	if err != nil || !exists || source.SourceType != knowledge_model.DataSourceTypeDatabase || source.Status != knowledge_model.DataSourceStatusEnabled || source.ConnectorType != "mysql" {
		return nil, errors.New("数据源不是已启用的 MySQL 脱敏视图")
	}
	scope, mapping, envName, err := parseMySQLConfiguration(source)
	if err != nil {
		return nil, err
	}
	dsn := os.Getenv(envName)
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("MySQL 密钥引用当前不可用")
	}
	mode := "sync"
	if preview {
		mode = "preview"
	}
	run := &knowledge_model.SyncRun{DataSourceID: source.ID, Status: "running", Mode: mode, StartedBy: actor.ID, StartedUnix: timeutil.TimeStampNow()}
	if err := db.Insert(ctx, run); err != nil {
		return nil, fmt.Errorf("创建同步记录失败: %w", err)
	}
	result, syncErr := runMySQLSync(ctx, source, actor.ID, dsn, scope, mapping, preview)
	if syncErr != nil {
		_ = finishRun(ctx, run, "failed", RunResult{}, "mysql_sync_failed")
		return nil, syncErr
	}
	if err := finishRun(ctx, run, "succeeded", *result, ""); err != nil {
		return nil, err
	}
	if !preview && result.LastCursor != "" {
		source.LastSuccessCursor = result.LastCursor
		source.LastSyncUnix = timeutil.TimeStampNow()
		if _, err := db.GetEngine(ctx).ID(source.ID).Cols("last_success_cursor", "last_sync_unix").Update(source); err != nil {
			return nil, fmt.Errorf("更新同步游标失败: %w", err)
		}
	}
	result.RunID = run.ID
	return result, nil
}

func runMySQLSync(ctx context.Context, source *knowledge_model.DataSource, actorID int64, dsn string, scope MySQLReadScope, mapping MySQLFieldMapping, preview bool) (*RunResult, error) {
	database, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, errors.New("无法打开 MySQL 只读连接")
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	database.SetConnMaxLifetime(30 * time.Second)
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := database.PingContext(pingCtx); err != nil {
		return nil, errors.New("MySQL 只读连接不可用")
	}
	query := fmt.Sprintf("SELECT `%s`, `%s`, `%s`, `%s` FROM `%s` WHERE `%s` > ? ORDER BY `%s` ASC LIMIT %d", mapping.ID, mapping.Title, mapping.Content, mapping.Cursor, scope.View, mapping.Cursor, mapping.Cursor, maxSyncRecords)
	rows, err := database.QueryContext(ctx, query, source.LastSuccessCursor)
	if err != nil {
		return nil, errors.New("读取 MySQL 脱敏视图失败")
	}
	defer rows.Close()
	result := &RunResult{}
	for rows.Next() {
		var id, title, content, cursor sql.NullString
		if err := rows.Scan(&id, &title, &content, &cursor); err != nil {
			return nil, errors.New("读取 MySQL 脱敏记录失败")
		}
		if !id.Valid || !validIdentifier(id.String) || !content.Valid || len(content.String) == 0 || len(content.String) > 1<<20 {
			return nil, errors.New("MySQL 脱敏视图返回了不合规记录")
		}
		result.RecordsRead++
		result.LastCursor = cursor.String
		if preview {
			continue
		}
		documentTitle := strings.TrimSpace(title.String)
		if documentTitle == "" {
			documentTitle = "数据库知识记录 " + id.String
		}
		fileName := "mysql-" + id.String + ".md"
		manifest := ingest.UploadManifest{FileName: fileName, Masked: true, MaskPolicyVersion: source.MaskPolicyVersion, MaskManifestJSON: json.RawMessage(`{"schema_version":1,"masked":true,"findings_count":0,"categories":["upstream_deidentified_view"]}`)}
		if _, err := catalog.UploadRevision(ctx, catalog.UploadRevisionOptions{SpaceID: source.SpaceID, DataSourceID: source.ID, ActorID: actorID, Title: documentTitle, VersionNo: "V1.0", Relation: "new", FileName: fileName, Content: strings.NewReader(content.String), Manifest: manifest}); err != nil {
			return nil, errors.New("创建待审核数据库知识失败")
		}
		result.DocumentsQueued++
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("遍历 MySQL 脱敏视图失败")
	}
	return result, nil
}

// ParseMySQLConfiguration validates an approved connector record without
// exposing its secret reference value or executing a connection.
func ParseMySQLConfiguration(source *knowledge_model.DataSource) (MySQLReadScope, MySQLFieldMapping, string, error) {
	return parseMySQLConfiguration(source)
}

func parseMySQLConfiguration(source *knowledge_model.DataSource) (MySQLReadScope, MySQLFieldMapping, string, error) {
	if source == nil || source.ConnectorType != "mysql" || !strings.HasPrefix(source.SecretRef, "secret-ref:env/") {
		return MySQLReadScope{}, MySQLFieldMapping{}, "", errors.New("MySQL 连接器配置不合规")
	}
	envName := strings.TrimPrefix(source.SecretRef, "secret-ref:env/")
	if !sqlIdentifier.MatchString(envName) || strings.Contains(strings.ToLower(envName), "password") {
		return MySQLReadScope{}, MySQLFieldMapping{}, "", errors.New("MySQL 密钥引用不合规")
	}
	var scope MySQLReadScope
	var mapping MySQLFieldMapping
	if json.Unmarshal([]byte(source.ReadScope), &scope) != nil || json.Unmarshal([]byte(source.FieldMappingJSON), &mapping) != nil || !validDeidentifiedView(scope.View) || !validIdentifier(mapping.ID) || !validIdentifier(mapping.Title) || !validIdentifier(mapping.Content) || !validIdentifier(mapping.Cursor) || mapping.Cursor != source.IncrementalCursorField {
		return MySQLReadScope{}, MySQLFieldMapping{}, "", errors.New("MySQL 只读视图或字段映射不合规")
	}
	return scope, mapping, envName, nil
}

func validIdentifier(value string) bool {
	return sqlIdentifier.MatchString(value)
}

func validDeidentifiedView(value string) bool {
	return strings.HasPrefix(value, "v_kb_masked_") && validIdentifier(value)
}

func finishRun(ctx context.Context, run *knowledge_model.SyncRun, status string, result RunResult, errorCode string) error {
	run.Status = status
	run.FinishedUnix = timeutil.TimeStampNow()
	run.RecordsRead = result.RecordsRead
	run.DocumentsQueued = result.DocumentsQueued
	run.LastCursor = result.LastCursor
	run.ErrorCode = errorCode
	_, err := db.GetEngine(ctx).ID(run.ID).Cols("status", "finished_unix", "records_read", "documents_queued", "last_cursor", "error_code").Update(run)
	return err
}
