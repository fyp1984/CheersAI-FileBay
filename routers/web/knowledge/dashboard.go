// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package knowledge

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"code.gitea.io/gitea/models/db"
	knowledge_model "code.gitea.io/gitea/models/knowledge"
	perm_model "code.gitea.io/gitea/models/perm"
	access_model "code.gitea.io/gitea/models/perm/access"
	repo_model "code.gitea.io/gitea/models/repo"
	"code.gitea.io/gitea/models/unit"
	user_model "code.gitea.io/gitea/models/user"
	"code.gitea.io/gitea/modules/git"
	"code.gitea.io/gitea/modules/gitrepo"
	"code.gitea.io/gitea/modules/log"
	"code.gitea.io/gitea/modules/setting"
	"code.gitea.io/gitea/modules/templates"
	"code.gitea.io/gitea/modules/timeutil"
	"code.gitea.io/gitea/services/context"
	"code.gitea.io/gitea/services/knowledge/catalog"
	"code.gitea.io/gitea/services/knowledge/connector"
	"code.gitea.io/gitea/services/knowledge/indexer"
	"code.gitea.io/gitea/services/knowledge/ingest"
	"code.gitea.io/gitea/services/knowledge/retrieval"
)

const tplKnowledgeDashboard templates.TplName = "knowledge/dashboard"

// Enabled returns 404 unless the knowledge feature flag is enabled.
func Enabled(ctx *context.Context) {
	if !setting.Knowledge.Enabled {
		ctx.NotFound(nil)
		return
	}
}

// Dashboard renders the production knowledge-governance dashboard.
func Dashboard(ctx *context.Context) {
	ctx.Data["Title"] = "企业知识库"
	ctx.Data["PageIsKnowledge"] = true
	ctx.Data["KnowledgeConfigError"] = setting.ValidateKnowledgeSettings()
	ctx.Data["KnowledgeSettings"] = setting.Knowledge

	var spaces []knowledge_model.Space
	if err := db.GetEngine(ctx).Desc("updated_unix").Limit(20).Find(&spaces); err != nil {
		ctx.ServerError("ListKnowledgeSpaces", err)
		return
	}
	ctx.Data["KnowledgeSpaces"] = spaces
	dataSources := loadRecentDataSources(ctx)
	ctx.Data["KnowledgeDataSources"] = dataSources
	ctx.Data["KnowledgeEnabledDataSources"] = loadEnabledDataSources(ctx)
	ctx.Data["KnowledgeCanApproveDataSources"] = ctx.Doer.IsAdmin
	dataSourceTasks := loadDataSourceGovernanceTasks(ctx)
	ctx.Data["KnowledgeDataSourceTasks"] = dataSourceTasks
	ctx.Data["KnowledgeDataSourceTaskCount"] = len(dataSourceTasks)

	counts, err := loadCounts(ctx)
	if err != nil {
		ctx.ServerError("LoadKnowledgeCounts", err)
		return
	}

	jobs, err := loadRecentJobs(ctx)
	if err != nil {
		ctx.ServerError("LoadKnowledgeJobs", err)
		return
	}
	ctx.Data["KnowledgeJobs"] = jobs
	ctx.Data["KnowledgeDocuments"] = loadRecentDocuments(ctx)
	pendingApprovals := loadPendingApprovals(ctx)
	ctx.Data["KnowledgeApprovals"] = pendingApprovals
	readyForReview := 0
	for _, approval := range pendingApprovals {
		if approval.CanReview {
			readyForReview++
		}
	}
	ctx.Data["KnowledgeReviewTaskCount"] = readyForReview
	ctx.Data["KnowledgeGovernanceTaskCount"] = readyForReview + len(dataSourceTasks)
	publications := loadRecentPublications(ctx)
	if counts.Publications < int64(len(publications)) {
		counts.Publications = int64(len(publications))
	}
	ctx.Data["KnowledgeCounts"] = counts
	ctx.Data["KnowledgePublications"] = publications
	ctx.Data["KnowledgeDifyBindings"] = loadDifyBindings(ctx)
	ctx.Data["KnowledgeSyncRuns"] = loadSyncRuns(ctx)
	ctx.Data["KnowledgeRepositoryArtifacts"] = loadRepositoryArtifacts(ctx)

	ctx.HTML(http.StatusOK, tplKnowledgeDashboard)
}

type repositoryArtifact struct {
	RepoID    int64
	RepoName  string
	Path      string
	CommitSHA string
	Size      int64
}

func loadRepositoryArtifacts(ctx *context.Context) []repositoryArtifact {
	repos, _, err := repo_model.SearchRepository(ctx, repo_model.SearchRepoOptions{
		ListOptions: db.ListOptions{Page: 1, PageSize: 100}, Actor: ctx.Doer, Private: true,
	})
	if err != nil {
		log.Warn("Knowledge repository artifact listing failed: %v", err)
		return nil
	}
	allowed := ingest.DefaultUploadPolicy().AllowedTypes
	artifacts := make([]repositoryArtifact, 0, 32)
	for _, repo := range repos {
		// SearchRepository already applies the current user's repository
		// visibility rules. Do not apply a second unit-level check here: an
		// administrator has repository visibility but may not have an explicit
		// row in the access table, which previously made every eligible file
		// disappear from the picker.
		if strings.HasPrefix(repo.Name, "kb-") {
			continue
		}
		gitRepo, closer, err := gitrepo.RepositoryFromContextOrOpen(ctx, repo)
		if err != nil {
			log.Warn("Knowledge repository artifact open failed for %s: %v", repo.FullName(), err)
			continue
		}
		// GetCommit accepts an object ID, not a branch name. Repository files
		// were therefore silently omitted whenever the default branch was
		// supplied (for example, "main"). Resolve the branch first.
		commit, err := gitRepo.GetBranchCommit(repo.DefaultBranch)
		if err != nil {
			log.Warn("Knowledge repository artifact commit lookup failed for %s branch %q: %v", repo.FullName(), repo.DefaultBranch, err)
		} else {
			collectRepositoryArtifacts(&commit.Tree, "", commit.ID.String(), repo, allowed, &artifacts)
		}
		_ = closer.Close()
	}
	return artifacts
}

func collectRepositoryArtifacts(tree *git.Tree, prefix, commitSHA string, repo *repo_model.Repository, allowed map[string]string, artifacts *[]repositoryArtifact) {
	if len(*artifacts) >= 200 {
		return
	}
	entries, err := tree.ListEntries()
	if err != nil {
		return
	}
	for _, entry := range entries {
		entryPath := path.Join(prefix, entry.Name())
		if entry.IsDir() {
			collectRepositoryArtifacts(entry.Tree(), entryPath, commitSHA, repo, allowed, artifacts)
			continue
		}
		if !entry.IsRegular() || entry.Size() > setting.Knowledge.MaxFileSize || allowed[strings.ToLower(filepath.Ext(entryPath))] == "" {
			continue
		}
		*artifacts = append(*artifacts, repositoryArtifact{RepoID: repo.ID, RepoName: repo.FullName(), Path: entryPath, CommitSHA: commitSHA, Size: entry.Size()})
	}
}

// SyncMySQL starts a preview or an actual controlled synchronization from one
// approved de-identified MySQL view.
func SyncMySQL(ctx *context.Context) {
	sourceID, err := strconv.ParseInt(ctx.PathParam("id"), 10, 64)
	if err != nil {
		ctx.NotFound(nil)
		return
	}
	preview := ctx.FormString("mode") == "preview"
	result, err := connector.SyncMySQL(ctx, sourceID, ctx.Doer.ID, preview)
	if err != nil {
		ctx.Flash.Error("MySQL 接入失败：" + err.Error())
	} else if preview {
		ctx.Flash.Success(fmt.Sprintf("脱敏视图预览完成：读取 %d 条记录，未创建知识文档", result.RecordsRead))
	} else {
		ctx.Flash.Success(fmt.Sprintf("MySQL 同步完成：读取 %d 条，已创建 %d 条待审核知识", result.RecordsRead, result.DocumentsQueued))
	}
	ctx.Redirect(setting.AppSubURL + "/knowledge")
}

// ExpireDueDataSources applies the lifecycle expiry check immediately and
// schedules derived-index deletion through the normal publication revocation.
func ExpireDueDataSources(ctx *context.Context) {
	count, err := catalog.ExpireDueDataSources(ctx, ctx.Doer.ID)
	if err != nil {
		ctx.Flash.Error("到期检查失败：" + err.Error())
	} else {
		ctx.Flash.Success(fmt.Sprintf("到期检查完成：已停用 %d 个数据源", count))
	}
	ctx.Redirect(setting.AppSubURL + "/knowledge")
}

// ConfirmDataSourceReview records the administrator's periodic freshness
// confirmation and moves the next review deadline forward.
func ConfirmDataSourceReview(ctx *context.Context) {
	sourceID, err := strconv.ParseInt(ctx.PathParam("id"), 10, 64)
	if err != nil {
		ctx.NotFound(nil)
		return
	}
	if err := catalog.ConfirmDataSourceReview(ctx, sourceID, ctx.Doer.ID); err != nil {
		ctx.Flash.Error("复审确认失败：" + err.Error())
	} else {
		ctx.Flash.Success("已记录本次复审，系统会按该来源的复审周期再次提醒")
	}
	ctx.Redirect(setting.AppSubURL + "/knowledge#knowledge-tasks")
}

// CompleteReview completes one mandatory checklist item before final approval.
func CompleteReview(ctx *context.Context) {
	revisionID, err := strconv.ParseInt(ctx.PathParam("revisionID"), 10, 64)
	if err != nil {
		ctx.Flash.Error("审核记录编号不正确")
		ctx.Redirect(setting.AppSubURL + "/knowledge")
		return
	}
	if err := catalog.CompleteReview(ctx, revisionID, ctx.Doer.ID, ctx.PathParam("reviewType"), ctx.FormString("comment")); err != nil {
		ctx.Flash.Error("审核项未能完成：" + err.Error())
	} else {
		ctx.Flash.Success("审核项已通过，请继续完成下一项审核；六项均通过后即可创建索引任务")
	}
	ctx.Redirect(setting.AppSubURL + "/knowledge#knowledge-review")
}

// CreateDifyBinding issues a one-time external knowledge credential.
func CreateDifyBinding(ctx *context.Context) {
	spaceID, err := strconv.ParseInt(ctx.FormString("space_id"), 10, 64)
	if err != nil {
		ctx.Flash.Error("知识空间编号不正确")
		ctx.Redirect(setting.AppSubURL + "/knowledge")
		return
	}
	binding, token, err := retrieval.CreateDifyBinding(ctx, retrieval.CreateDifyBindingOptions{
		ActorID: ctx.Doer.ID, Name: ctx.FormString("name"), DifyKnowledgeID: ctx.FormString("dify_knowledge_id"), SpaceID: spaceID,
		MaxSecurityLevel: knowledge_model.SecurityLevel(ctx.FormString("max_security_level")),
	})
	if err != nil {
		ctx.Flash.Error("Dify 应用授权创建失败：" + err.Error())
		ctx.Redirect(setting.AppSubURL + "/knowledge")
		return
	}
	ctx.Data["KnowledgeNewDifyToken"] = token
	ctx.Data["KnowledgeNewDifyBinding"] = binding
	Dashboard(ctx)
}

// Search performs a signed-in FileBay-native governed search.
func Search(ctx *context.Context) {
	spaceID, _ := strconv.ParseInt(ctx.FormString("space_id"), 10, 64)
	result, err := retrieval.RetrieveForUser(ctx, ctx.Doer, spaceID, ctx.FormString("query"), 5)
	if err != nil {
		ctx.Flash.Error("知识检索失败：" + err.Error())
		ctx.Redirect(setting.AppSubURL + "/knowledge")
		return
	}
	ctx.Data["KnowledgeSearchQuery"] = ctx.FormString("query")
	ctx.Data["KnowledgeSearchResult"] = result
	Dashboard(ctx)
}

// CreateFeedback stores structured feedback for trial analytics.
func CreateFeedback(ctx *context.Context) {
	spaceID, _ := strconv.ParseInt(ctx.FormString("space_id"), 10, 64)
	publicationID, _ := strconv.ParseInt(ctx.FormString("publication_id"), 10, 64)
	rating, _ := strconv.Atoi(ctx.FormString("rating"))
	if err := retrieval.RecordFeedback(ctx, ctx.Doer.ID, spaceID, publicationID, ctx.FormString("category"), rating, ctx.FormString("comment")); err != nil {
		ctx.Flash.Error("反馈提交失败：" + err.Error())
	} else {
		ctx.Flash.Success("感谢反馈，已记录用于知识库改进")
	}
	ctx.Redirect(setting.AppSubURL + "/knowledge")
}

// ActivatePublication marks a RAGFlow-evaluated candidate as currently usable.
func ActivatePublication(ctx *context.Context) {
	publicationID, err := strconv.ParseInt(ctx.PathParam("id"), 10, 64)
	if err != nil {
		ctx.NotFound(nil)
		return
	}
	if err := catalog.ActivatePublication(ctx, publicationID, ctx.Doer.ID); err != nil {
		ctx.Flash.Error("发布启用失败：" + err.Error())
	} else {
		ctx.Flash.Success("知识已生效。下一步：可以立即检索验证答案和引用来源")
	}
	ctx.Redirect(setting.AppSubURL + "/knowledge#knowledge-search")
}

// UnpublishPublication revokes a publication before derived-index deletion.
func UnpublishPublication(ctx *context.Context) {
	publicationID, err := strconv.ParseInt(ctx.PathParam("id"), 10, 64)
	if err != nil {
		ctx.NotFound(nil)
		return
	}
	if err := catalog.UnpublishPublication(ctx, publicationID, ctx.Doer.ID, ctx.FormString("reason")); err != nil {
		ctx.Flash.Error("知识下架失败：" + err.Error())
	} else {
		ctx.Flash.Success("知识已下架，FileBay 已立即停止返回该引用")
	}
	ctx.Redirect(setting.AppSubURL + "/knowledge")
}

// CreateDataSource registers a governed knowledge source for administrator review.
func CreateDataSource(ctx *context.Context) {
	effectiveDate := strings.TrimSpace(ctx.FormString("effective_date"))
	if effectiveDate == "" {
		effectiveDate = time.Now().Format("2006-01-02")
	}
	effectiveUnix, err := parseKnowledgeDate(effectiveDate, true)
	if err != nil {
		ctx.Flash.Error("生效日期格式不正确，请使用 YYYY-MM-DD")
		ctx.Redirect(setting.AppSubURL + "/knowledge#knowledge-data")
		return
	}
	expiresUnix, err := parseKnowledgeDate(ctx.FormString("expires_date"), false)
	if err != nil {
		ctx.Flash.Error("失效日期格式不正确，请使用 YYYY-MM-DD")
		ctx.Redirect(setting.AppSubURL + "/knowledge#knowledge-data")
		return
	}
	formValue := func(name, fallback string) string {
		if value := strings.TrimSpace(ctx.FormString(name)); value != "" {
			return value
		}
		return fallback
	}
	username := ctx.Doer.Name
	sourceType := formValue("source_type", string(knowledge_model.DataSourceTypePolicy))
	sourceCode := strings.TrimSpace(ctx.FormString("source_code"))
	if sourceCode == "" {
		// Business users should not need to invent an internal source code. The
		// generated value remains stable after creation and is shown in the ledger.
		sourceCode = fmt.Sprintf("manual-%d", time.Now().UnixNano())
	}
	source, err := catalog.CreateDataSource(ctx, catalog.CreateDataSourceOptions{
		SpaceID:                ctx.FormInt64("space_id"),
		ActorID:                ctx.Doer.ID,
		SourceCode:             sourceCode,
		Name:                   ctx.FormString("name"),
		SourceType:             knowledge_model.DataSourceType(sourceType),
		BusinessDomainCode:     formValue("business_domain_code", "general"),
		ContentTypeCode:        formValue("content_type_code", sourceType),
		SourceDepartment:       formValue("source_department", "未指定部门"),
		ContentOwnerUsername:   formValue("content_owner_username", username),
		MaintenanceUsername:    formValue("maintenance_username", username),
		EffectiveUnix:          effectiveUnix,
		ExpiresUnix:            expiresUnix,
		ReviewFrequency:        knowledge_model.ReviewFrequency(formValue("review_frequency", "monthly")),
		SecurityLevel:          knowledge_model.SecurityLevel(formValue("security_level", "internal")),
		ApprovalBasisRef:       formValue("approval_basis_ref", "approval-ref:manual-entry"),
		ApplicableScopeJSON:    formValue("applicable_scope_json", `{"organization":"全公司"}`),
		SourceAuthorizationRef: formValue("source_authorization_ref", "grant-id:manual-upload"),
		SourceFormat:           formValue("source_format", "text/markdown"),
		SearchabilityStatus:    knowledge_model.SearchabilityStatus(formValue("searchability_status", "searchable")),
		ConnectorType:          ctx.FormString("connector_type"),
		SecretRef:              ctx.FormString("secret_ref"),
		ReadScope:              ctx.FormString("read_scope"),
		QueryTemplateRef:       ctx.FormString("query_template_ref"),
		SchemaVersion:          ctx.FormString("schema_version"),
		FieldMappingJSON:       ctx.FormString("field_mapping_json"),
		IncrementalCursorField: ctx.FormString("incremental_cursor_field"),
		DataRetentionRule:      ctx.FormString("data_retention_rule"),
		MaskPolicyVersion:      formValue("mask_policy_version", "mask-policy-manual-v1"),
	})
	if err != nil {
		ctx.Flash.Error("数据源登记失败：" + err.Error())
		ctx.Redirect(setting.AppSubURL + "/knowledge#knowledge-data")
		return
	}
	ctx.Flash.Success("资料来源已登记：" + source.Name + "。下一步：请由另一位知识管理员在下方审核并启用该来源")
	ctx.Redirect(setting.AppSubURL + "/knowledge#knowledge-data")
}

// EnableDataSource allows a second administrator to approve a pending source.
func EnableDataSource(ctx *context.Context) {
	sourceID, err := strconv.ParseInt(ctx.PathParam("id"), 10, 64)
	if err != nil {
		ctx.NotFound(nil)
		return
	}
	if err := catalog.EnableDataSource(ctx, sourceID, ctx.Doer.ID); err != nil {
		ctx.Flash.Error("数据源启用失败：" + err.Error())
		ctx.Redirect(setting.AppSubURL + "/knowledge")
		return
	}
	ctx.Flash.Success("数据源已审核并启用。下一步：从 FileBay 仓库选择已脱敏文件并提交")
	ctx.Redirect(setting.AppSubURL + "/knowledge#knowledge-upload")
}

// CreateSpace creates a private FileBay repository bound to a knowledge space.
func CreateSpace(ctx *context.Context) {
	space, err := catalog.CreateSpace(ctx, catalog.CreateSpaceOptions{
		OwnerID:     ctx.Doer.ID,
		ActorID:     ctx.Doer.ID,
		Name:        ctx.FormString("name"),
		Description: ctx.FormString("description"),
	})
	if err != nil {
		if errors.Is(err, catalog.ErrSpaceAlreadyExists) {
			ctx.Flash.Warning("该资料夹已经创建，请到“资料与空间台账”继续管理。")
			ctx.Redirect(setting.AppSubURL + "/knowledge#knowledge-library")
			return
		}
		ctx.Flash.Error(err.Error())
		ctx.Redirect(setting.AppSubURL + "/knowledge#knowledge-space")
		return
	}
	ctx.Flash.Success("资料夹已创建：" + space.Name + "。下一步：登记这批资料的来源")
	ctx.Redirect(setting.AppSubURL + "/knowledge#knowledge-data")
}

// DeleteEmptySpace deletes an unused test or draft space only when it has no
// governed content, audit trail, or integration binding.
func DeleteEmptySpace(ctx *context.Context) {
	if err := catalog.DeleteEmptySpace(ctx, ctx.PathParamInt64("id"), ctx.Doer.ID); err != nil {
		ctx.Flash.Error("无法删除资料夹：" + err.Error())
		ctx.Redirect(setting.AppSubURL + "/knowledge")
		return
	}
	ctx.Flash.Success("空资料夹已删除")
	ctx.Redirect(setting.AppSubURL + "/knowledge")
}

// UploadRevision accepts only explicitly declared, client-masked artifacts.
func UploadRevision(ctx *context.Context) {
	if !ctx.ParseMultipartForm() {
		ctx.Flash.Error("上传资料格式不正确")
		ctx.Redirect(setting.AppSubURL + "/knowledge#knowledge-upload")
		return
	}
	file, header, err := ctx.Req.FormFile("artifact")
	if err != nil {
		ctx.Flash.Error("请选择已经脱敏的资料文件")
		ctx.Redirect(setting.AppSubURL + "/knowledge#knowledge-upload")
		return
	}
	defer file.Close()
	declaredMIME := ingest.DefaultUploadPolicy().AllowedTypes[strings.ToLower(filepath.Ext(header.Filename))]
	manifest := ingest.UploadManifest{
		Masked: ctx.FormBool("masked"),
		// MIME is derived from the submitted file name and independently checked
		// against the file signature by the catalog service. It is not a user
		// input because asking users to select a MIME type is error-prone.
		MIMEType:            declaredMIME,
		MaskPolicyVersion:   ctx.FormString("mask_policy_version"),
		MaskManifestJSON:    []byte(ctx.FormString("mask_manifest_json")),
		SourceAuthorization: ctx.FormString("source_authorization"),
	}
	versionNo := strings.TrimSpace(ctx.FormString("version_no"))
	if versionNo == "" {
		versionNo = "V1.0"
	}
	relation := strings.TrimSpace(ctx.FormString("revision_relation"))
	if relation == "" {
		relation = "new"
	}
	result, err := catalog.UploadRevision(ctx, catalog.UploadRevisionOptions{
		DocumentID:   ctx.FormInt64("document_id"),
		SpaceID:      ctx.FormInt64("space_id"),
		DataSourceID: ctx.FormInt64("data_source_id"),
		ActorID:      ctx.Doer.ID,
		Title:        ctx.FormString("title"),
		VersionNo:    versionNo,
		Relation:     relation,
		FileName:     header.Filename,
		Content:      file,
		Manifest:     manifest,
	})
	if err != nil {
		ctx.Flash.Error(err.Error())
		ctx.Redirect(setting.AppSubURL + "/knowledge#knowledge-upload")
		return
	}
	ctx.Flash.Success("资料已提交：" + result.Document.Title + "。下一步：由另一位知识管理员完成六项审核")
	ctx.Redirect(setting.AppSubURL + "/knowledge#knowledge-review")
}

// ImportRepositoryArtifact copies an explicitly selected, already-masked
// FileBay repository revision into the governed knowledge workflow.
func ImportRepositoryArtifact(ctx *context.Context) {
	repoID := ctx.FormInt64("repository_id")
	repoPath := path.Clean(strings.TrimSpace(ctx.FormString("repository_path")))
	commitSHA := strings.TrimSpace(ctx.FormString("repository_commit"))
	if repoID <= 0 || repoPath == "." || strings.HasPrefix(repoPath, "../") || commitSHA == "" || !ctx.FormBool("repository_masked") {
		ctx.Flash.Error("请选择仓库中的已脱敏文件，并确认脱敏声明")
		ctx.Redirect(setting.AppSubURL + "/knowledge#knowledge-upload")
		return
	}
	repo, err := repo_model.GetRepositoryByID(ctx, repoID)
	if err != nil {
		ctx.Flash.Error("仓库文件不可用")
		ctx.Redirect(setting.AppSubURL + "/knowledge#knowledge-upload")
		return
	}
	ok, err := access_model.HasAccessUnit(ctx, ctx.Doer, repo, unit.TypeCode, perm_model.AccessModeRead)
	if err != nil || !ok || strings.HasPrefix(repo.Name, "kb-") {
		ctx.Flash.Error("你没有读取该仓库文件的权限")
		ctx.Redirect(setting.AppSubURL + "/knowledge#knowledge-upload")
		return
	}
	reader, err := (indexer.GitSourceProvider{}).Open(ctx, repoID, commitSHA, repoPath)
	if err != nil {
		ctx.Flash.Error("无法读取所选仓库版本")
		ctx.Redirect(setting.AppSubURL + "/knowledge#knowledge-upload")
		return
	}
	defer reader.Close()
	content, err := io.ReadAll(io.LimitReader(reader, setting.Knowledge.MaxFileSize+1))
	if err != nil || int64(len(content)) > setting.Knowledge.MaxFileSize {
		ctx.Flash.Error("所选仓库文件读取失败或超过大小限制")
		ctx.Redirect(setting.AppSubURL + "/knowledge#knowledge-upload")
		return
	}
	result, err := catalog.UploadRevision(ctx, catalog.UploadRevisionOptions{
		DocumentID: ctx.FormInt64("document_id"), SpaceID: ctx.FormInt64("space_id"), DataSourceID: ctx.FormInt64("data_source_id"), ActorID: ctx.Doer.ID,
		Title: ctx.FormString("title"), VersionNo: defaultKnowledgeVersion(ctx.FormString("version_no")), Relation: defaultKnowledgeRelation(ctx.FormString("revision_relation")),
		FileName: path.Base(repoPath), Content: bytes.NewReader(content), Manifest: ingest.UploadManifest{Masked: true},
	})
	if err != nil {
		ctx.Flash.Error("仓库文件提交失败：" + err.Error())
		ctx.Redirect(setting.AppSubURL + "/knowledge#knowledge-upload")
		return
	}
	ctx.Flash.Success("已从 FileBay 仓库提交资料：" + result.Document.Title + "。下一步：由另一位知识管理员完成六项审核")
	ctx.Redirect(setting.AppSubURL + "/knowledge#knowledge-review")
}

func defaultKnowledgeVersion(value string) string {
	if strings.TrimSpace(value) == "" {
		return "V1.0"
	}
	return strings.TrimSpace(value)
}
func defaultKnowledgeRelation(value string) string {
	if strings.TrimSpace(value) == "" {
		return "new"
	}
	return strings.TrimSpace(value)
}

// Approve approves a pending revision. The model enforces no self-approval.
func Approve(ctx *context.Context) {
	approvalID, err := strconv.ParseInt(ctx.PathParam("id"), 10, 64)
	if err != nil {
		ctx.NotFound(nil)
		return
	}
	result, err := catalog.Approve(ctx, approvalID, ctx.Doer.ID, ctx.FormString("comment"))
	if err != nil {
		ctx.Flash.Error(err.Error())
		ctx.Redirect(setting.AppSubURL + "/knowledge")
		return
	}
	ctx.Flash.Success("六项审核已通过，RAGFlow 索引任务已创建：#" + strconv.FormatInt(result.IndexJob.ID, 10) + "。下一步：处理索引并评测发布")
	ctx.Redirect(setting.AppSubURL + "/knowledge#knowledge-publish")
}

// Reject records a terminal review decision without deleting its governed
// artifact or affecting an earlier currently published revision.
func Reject(ctx *context.Context) {
	approvalID, err := strconv.ParseInt(ctx.PathParam("id"), 10, 64)
	if err != nil {
		ctx.NotFound(nil)
		return
	}
	if err := catalog.Reject(ctx, approvalID, ctx.Doer.ID, ctx.FormString("comment")); err != nil {
		ctx.Flash.Error("审核驳回失败：" + err.Error())
	} else {
		ctx.Flash.Success("知识修订已驳回，原有已生效版本不受影响")
	}
	ctx.Redirect(setting.AppSubURL + "/knowledge")
}

// ProcessJob runs one queued RAGFlow job with the configured engine binding.
func ProcessJob(ctx *context.Context) {
	jobID, err := strconv.ParseInt(ctx.PathParam("id"), 10, 64)
	if err != nil {
		ctx.NotFound(nil)
		return
	}
	if err := catalog.ProcessIndexJob(ctx, jobID, ctx.Doer.ID); err != nil {
		ctx.Flash.Error(err.Error())
		ctx.Redirect(setting.AppSubURL + "/knowledge")
		return
	}
	ctx.Flash.Success("RAGFlow 索引任务已处理：#" + strconv.FormatInt(jobID, 10) + "。下一步：评测通过后使知识生效")
	ctx.Redirect(setting.AppSubURL + "/knowledge#knowledge-publish")
}

// RetryPublicationIndex requeues a failed publication after its RAGFlow
// configuration has been corrected.
func RetryPublicationIndex(ctx *context.Context) {
	publicationID, err := strconv.ParseInt(ctx.PathParam("id"), 10, 64)
	if err != nil {
		ctx.NotFound(nil)
		return
	}
	if err := catalog.RetryPublicationIndex(ctx, publicationID, ctx.Doer.ID); err != nil {
		ctx.Flash.Error("重新索引失败：" + err.Error())
	} else {
		ctx.Flash.Success("已重新提交索引，FileBay 将自动写入 RAGFlow")
	}
	ctx.Redirect(setting.AppSubURL + "/knowledge#knowledge-publish")
}

type dashboardCounts struct {
	Spaces         int64
	DataSources    int64
	Documents      int64
	Publications   int64
	QueuedJobs     int64
	FailedJobs     int64
	Retrievals     int64
	NoAnswer       int64
	Feedback       int64
	ExpiredSources int64
}

type dashboardJob struct {
	ID                   int64
	PublicationID        int64
	JobType              knowledge_model.IndexJobType
	JobTypeLabel         string
	Status               knowledge_model.IndexJobStatus
	StatusLabel          string
	Attempt              int
	MaxAttempts          int
	EngineProfileVersion string
	LastError            string
	CanProcess           bool
}

type dashboardDocument struct {
	ID                int64
	SpaceID           int64
	DataSourceID      int64
	Title             string
	RepoPath          string
	MIMEType          string
	GovernanceStatus  knowledge_model.GovernanceStatus
	GovernanceLabel   string
	CurrentRevisionID int64
}

type dashboardDataSource struct {
	ID                   int64
	SpaceID              int64
	SourceCode           string
	Name                 string
	SourceType           knowledge_model.DataSourceType
	SourceTypeLabel      string
	BusinessDomainCode   string
	ContentTypeCode      string
	SecurityLevel        knowledge_model.SecurityLevel
	SecurityLevelLabel   string
	Status               knowledge_model.DataSourceStatus
	StatusLabel          string
	ContentOwnerName     string
	MaintenanceOwnerName string
	EffectiveUnix        timeutil.TimeStamp
	ExpiresUnix          timeutil.TimeStamp
	SearchabilityStatus  knowledge_model.SearchabilityStatus
	SearchabilityLabel   string
	CanApprove           bool
	ConnectorType        string
	CanSync              bool
	ReviewStatusLabel    string
	ReviewDueUnix        timeutil.TimeStamp
}

type dashboardDataSourceTask struct {
	ID                int64
	Name              string
	ReviewFrequency   string
	ReviewDueUnix     timeutil.TimeStamp
	ReviewStatusLabel string
	IsOverdue         bool
	CanConfirmReview  bool
}

type dashboardApproval struct {
	ID           int64
	RevisionID   int64
	RequestedBy  int64
	Status       knowledge_model.ApprovalStatus
	DocumentID   int64
	Title        string
	FileName     string
	ReviewChecks []dashboardReviewCheck
	CanReview    bool
	CanApprove   bool
}

type dashboardReviewCheck struct {
	ReviewType  string
	ReviewLabel string
	Status      string
	StatusLabel string
	CanReview   bool
}

type dashboardPublication struct {
	ID                   int64
	SpaceID              int64
	DocumentID           int64
	Generation           int64
	ValidityStatus       knowledge_model.ValidityStatus
	ValidityLabel        string
	IndexStatus          knowledge_model.IndexStatus
	IndexLabel           string
	IsCurrent            bool
	RevocationGeneration int64
	CanActivate          bool
	CanUnpublish         bool
	CanRetry             bool
}

func loadCounts(ctx *context.Context) (*dashboardCounts, error) {
	spaces, err := db.GetEngine(ctx).Count(new(knowledge_model.Space))
	if err != nil {
		return nil, err
	}
	documents, err := db.GetEngine(ctx).Count(new(knowledge_model.Document))
	if err != nil {
		return nil, err
	}
	dataSources, err := db.GetEngine(ctx).Count(new(knowledge_model.DataSource))
	if err != nil {
		return nil, err
	}
	publications, err := db.GetEngine(ctx).Count(new(knowledge_model.Publication))
	if err != nil {
		return nil, err
	}
	queuedJobs, err := db.GetEngine(ctx).
		Where("status IN (?, ?, ?)", string(knowledge_model.IndexJobStatusQueued), string(knowledge_model.IndexJobStatusRetry), string(knowledge_model.IndexJobStatusRunning)).
		Count(new(knowledge_model.IndexJob))
	if err != nil {
		return nil, err
	}
	failedJobs, err := db.GetEngine(ctx).Where("status = ?", string(knowledge_model.IndexJobStatusFailed)).Count(new(knowledge_model.IndexJob))
	if err != nil {
		return nil, err
	}
	retrievals, err := db.GetEngine(ctx).Count(new(knowledge_model.RetrievalEvent))
	if err != nil {
		return nil, err
	}
	noAnswer, err := db.GetEngine(ctx).Where("outcome = ?", "empty").Count(new(knowledge_model.RetrievalEvent))
	if err != nil {
		return nil, err
	}
	feedback, err := db.GetEngine(ctx).Count(new(knowledge_model.Feedback))
	if err != nil {
		return nil, err
	}
	expiredSources, err := db.GetEngine(ctx).Where("expires_unix > 0 AND expires_unix <= ?", timeutil.TimeStampNow()).Count(new(knowledge_model.DataSource))
	if err != nil {
		return nil, err
	}
	return &dashboardCounts{
		Spaces:         spaces,
		DataSources:    dataSources,
		Documents:      documents,
		Publications:   publications,
		QueuedJobs:     queuedJobs,
		FailedJobs:     failedJobs,
		Retrievals:     retrievals,
		NoAnswer:       noAnswer,
		Feedback:       feedback,
		ExpiredSources: expiredSources,
	}, nil
}

func loadRecentJobs(ctx *context.Context) ([]dashboardJob, error) {
	var jobs []knowledge_model.IndexJob
	if err := db.GetEngine(ctx).Desc("updated_unix").Limit(20).Find(&jobs); err != nil {
		return nil, err
	}
	rows := make([]dashboardJob, 0, len(jobs))
	for _, job := range jobs {
		rows = append(rows, dashboardJob{
			ID:                   job.ID,
			PublicationID:        job.PublicationID,
			JobType:              job.JobType,
			JobTypeLabel:         indexJobTypeLabel(job.JobType),
			Status:               job.Status,
			StatusLabel:          indexJobStatusLabel(job.Status),
			Attempt:              job.Attempt,
			MaxAttempts:          job.MaxAttempts,
			EngineProfileVersion: job.EngineProfileVersion,
			LastError:            job.LastError,
			CanProcess:           job.Status == knowledge_model.IndexJobStatusQueued || job.Status == knowledge_model.IndexJobStatusRetry,
		})
	}
	return rows, nil
}

func loadRecentDocuments(ctx *context.Context) []dashboardDocument {
	var documents []knowledge_model.Document
	if err := db.GetEngine(ctx).Desc("updated_unix").Limit(20).Find(&documents); err != nil {
		return nil
	}
	rows := make([]dashboardDocument, 0, len(documents))
	for _, document := range documents {
		rows = append(rows, dashboardDocument{
			ID:                document.ID,
			SpaceID:           document.SpaceID,
			DataSourceID:      document.DataSourceID,
			Title:             document.Title,
			RepoPath:          document.RepoPath,
			MIMEType:          document.MIMEType,
			GovernanceStatus:  document.GovernanceStatus,
			GovernanceLabel:   governanceStatusLabel(document.GovernanceStatus),
			CurrentRevisionID: document.CurrentRevisionID,
		})
	}
	return rows
}

func loadRecentDataSources(ctx *context.Context) []dashboardDataSource {
	var sources []knowledge_model.DataSource
	if err := db.GetEngine(ctx).Desc("updated_unix").Limit(20).Find(&sources); err != nil {
		return nil
	}
	latestReviews := loadLatestDataSourceReviewEvents(ctx, sources)
	now := timeutil.TimeStampNow()
	rows := make([]dashboardDataSource, 0, len(sources))
	for _, source := range sources {
		contentOwnerName := fmt.Sprintf("#%d", source.ContentOwnerID)
		if owner, err := user_model.GetUserByID(ctx, source.ContentOwnerID); err == nil && owner != nil {
			contentOwnerName = owner.Name
		}
		maintenanceOwnerName := fmt.Sprintf("#%d", source.MaintenanceOwnerID)
		if owner, err := user_model.GetUserByID(ctx, source.MaintenanceOwnerID); err == nil && owner != nil {
			maintenanceOwnerName = owner.Name
		}
		freshness := evaluateDataSourceFreshness(source, latestReviews[source.ID], now)
		rows = append(rows, dashboardDataSource{
			ID:                   source.ID,
			SpaceID:              source.SpaceID,
			SourceCode:           source.SourceCode,
			Name:                 source.Name,
			SourceType:           source.SourceType,
			SourceTypeLabel:      dataSourceTypeLabel(source.SourceType),
			BusinessDomainCode:   source.BusinessDomainCode,
			ContentTypeCode:      source.ContentTypeCode,
			SecurityLevel:        source.SecurityLevel,
			SecurityLevelLabel:   securityLevelLabel(source.SecurityLevel),
			Status:               source.Status,
			StatusLabel:          dataSourceStatusLabel(source.Status),
			ContentOwnerName:     contentOwnerName,
			MaintenanceOwnerName: maintenanceOwnerName,
			EffectiveUnix:        source.EffectiveUnix,
			ExpiresUnix:          source.ExpiresUnix,
			SearchabilityStatus:  source.SearchabilityStatus,
			SearchabilityLabel:   searchabilityStatusLabel(source.SearchabilityStatus),
			CanApprove:           ctx.Doer.IsAdmin && source.Status == knowledge_model.DataSourceStatusPendingReview && source.CreatedBy != ctx.Doer.ID,
			ConnectorType:        source.ConnectorType,
			CanSync:              ctx.Doer.IsAdmin && source.Status == knowledge_model.DataSourceStatusEnabled && source.SourceType == knowledge_model.DataSourceTypeDatabase && source.ConnectorType == "mysql",
			ReviewStatusLabel:    freshness.Label,
			ReviewDueUnix:        freshness.DueUnix,
		})
	}
	return rows
}

type dataSourceFreshness struct {
	DueUnix   timeutil.TimeStamp
	Label     string
	IsTask    bool
	IsOverdue bool
}

const dataSourceReviewReminderWindow = 7 * 24 * time.Hour

func evaluateDataSourceFreshness(source knowledge_model.DataSource, lastConfirmed, now timeutil.TimeStamp) dataSourceFreshness {
	if source.Status != knowledge_model.DataSourceStatusEnabled {
		return dataSourceFreshness{Label: "启用后开始复审"}
	}
	if source.ReviewFrequency == knowledge_model.ReviewFrequencyEventTriggered {
		return dataSourceFreshness{Label: "事件触发时复审"}
	}

	base := source.EffectiveUnix
	if source.UpdatedUnix > base {
		base = source.UpdatedUnix
	}
	if lastConfirmed > base {
		base = lastConfirmed
	}
	if base <= 0 {
		return dataSourceFreshness{Label: "缺少复审基准"}
	}

	due := nextReviewDue(base, source.ReviewFrequency)
	if due <= 0 {
		return dataSourceFreshness{Label: "复审周期未配置"}
	}
	if due <= now {
		days := int((int64(now) - int64(due)) / int64(24*time.Hour/time.Second))
		if days == 0 {
			return dataSourceFreshness{DueUnix: due, Label: "今天需要复审", IsTask: true, IsOverdue: true}
		}
		return dataSourceFreshness{DueUnix: due, Label: fmt.Sprintf("已逾期 %d 天", days), IsTask: true, IsOverdue: true}
	}
	if time.Duration(int64(due-now))*time.Second <= dataSourceReviewReminderWindow {
		days := int((int64(due) - int64(now) + int64(24*time.Hour/time.Second) - 1) / int64(24*time.Hour/time.Second))
		return dataSourceFreshness{DueUnix: due, Label: fmt.Sprintf("%d 天后需要复审", days), IsTask: true}
	}
	return dataSourceFreshness{DueUnix: due, Label: "复审正常"}
}

func nextReviewDue(base timeutil.TimeStamp, frequency knowledge_model.ReviewFrequency) timeutil.TimeStamp {
	baseTime := time.Unix(int64(base), 0)
	switch frequency {
	case knowledge_model.ReviewFrequencyWeekly:
		return timeutil.TimeStamp(baseTime.AddDate(0, 0, 7).Unix())
	case knowledge_model.ReviewFrequencyMonthly:
		return timeutil.TimeStamp(baseTime.AddDate(0, 1, 0).Unix())
	case knowledge_model.ReviewFrequencyQuarterly:
		return timeutil.TimeStamp(baseTime.AddDate(0, 3, 0).Unix())
	default:
		return 0
	}
}

func loadLatestDataSourceReviewEvents(ctx *context.Context, sources []knowledge_model.DataSource) map[int64]timeutil.TimeStamp {
	ids := make([]int64, 0, len(sources))
	for _, source := range sources {
		ids = append(ids, source.ID)
	}
	if len(ids) == 0 {
		return nil
	}
	var events []knowledge_model.AuditEvent
	if err := db.GetEngine(ctx).In("entity_id", ids).Where("entity_type = ? AND action = ?", "data_source", "data_source_review_confirmed").Desc("created_unix").Find(&events); err != nil {
		return nil
	}
	latest := make(map[int64]timeutil.TimeStamp, len(events))
	for _, event := range events {
		if _, exists := latest[event.EntityID]; !exists {
			latest[event.EntityID] = event.CreatedUnix
		}
	}
	return latest
}

func loadDataSourceGovernanceTasks(ctx *context.Context) []dashboardDataSourceTask {
	var sources []knowledge_model.DataSource
	if err := db.GetEngine(ctx).Where("status = ?", knowledge_model.DataSourceStatusEnabled).Asc("expires_unix").Find(&sources); err != nil {
		return nil
	}
	latestReviews := loadLatestDataSourceReviewEvents(ctx, sources)
	now := timeutil.TimeStampNow()
	tasks := make([]dashboardDataSourceTask, 0, len(sources))
	for _, source := range sources {
		freshness := evaluateDataSourceFreshness(source, latestReviews[source.ID], now)
		if !freshness.IsTask {
			continue
		}
		tasks = append(tasks, dashboardDataSourceTask{
			ID:                source.ID,
			Name:              source.Name,
			ReviewFrequency:   reviewFrequencyLabel(source.ReviewFrequency),
			ReviewDueUnix:     freshness.DueUnix,
			ReviewStatusLabel: freshness.Label,
			IsOverdue:         freshness.IsOverdue,
			CanConfirmReview:  ctx.Doer.IsAdmin,
		})
	}
	return tasks
}

func loadSyncRuns(ctx *context.Context) []knowledge_model.SyncRun {
	var runs []knowledge_model.SyncRun
	if err := db.GetEngine(ctx).Desc("updated_unix").Limit(20).Find(&runs); err != nil {
		return nil
	}
	return runs
}

func dataSourceTypeLabel(value knowledge_model.DataSourceType) string {
	labels := map[knowledge_model.DataSourceType]string{
		knowledge_model.DataSourceTypePolicy:         "制度库",
		knowledge_model.DataSourceTypeProcedure:      "流程与作业手册",
		knowledge_model.DataSourceTypeProduct:        "产品资料",
		knowledge_model.DataSourceTypeTraining:       "培训资料",
		knowledge_model.DataSourceTypeFAQ:            "常见问答",
		knowledge_model.DataSourceTypeCase:           "案例资料",
		knowledge_model.DataSourceTypeTicket:         "工单记录",
		knowledge_model.DataSourceTypePublicMaterial: "外部公开材料",
		knowledge_model.DataSourceTypeDatabase:       "数据库/业务系统",
		knowledge_model.DataSourceTypeAPI:            "外部 API",
		knowledge_model.DataSourceTypeFileRepository: "文件资料库",
	}
	return labels[value]
}

func securityLevelLabel(value knowledge_model.SecurityLevel) string {
	labels := map[knowledge_model.SecurityLevel]string{
		knowledge_model.SecurityLevelPublic:     "一级公开",
		knowledge_model.SecurityLevelInternal:   "二级内部",
		knowledge_model.SecurityLevelRestricted: "三级受限",
		knowledge_model.SecurityLevelProhibited: "四级禁止入库",
	}
	return labels[value]
}

func dataSourceStatusLabel(value knowledge_model.DataSourceStatus) string {
	labels := map[knowledge_model.DataSourceStatus]string{
		knowledge_model.DataSourceStatusDraft:         "草稿",
		knowledge_model.DataSourceStatusPendingReview: "待审核",
		knowledge_model.DataSourceStatusApproved:      "已批准",
		knowledge_model.DataSourceStatusEnabled:       "已启用",
		knowledge_model.DataSourceStatusSuspended:     "已停用",
		knowledge_model.DataSourceStatusExpired:       "已过期",
		knowledge_model.DataSourceStatusArchived:      "已归档",
	}
	return labels[value]
}

func reviewFrequencyLabel(value knowledge_model.ReviewFrequency) string {
	return map[knowledge_model.ReviewFrequency]string{
		knowledge_model.ReviewFrequencyWeekly:         "每周",
		knowledge_model.ReviewFrequencyMonthly:        "每月",
		knowledge_model.ReviewFrequencyQuarterly:      "每季度",
		knowledge_model.ReviewFrequencyEventTriggered: "事件触发",
	}[value]
}

func searchabilityStatusLabel(value knowledge_model.SearchabilityStatus) string {
	labels := map[knowledge_model.SearchabilityStatus]string{
		knowledge_model.SearchabilityStatusSearchable:    "可全文检索",
		knowledge_model.SearchabilityStatusOCRRequired:   "需完成 OCR",
		knowledge_model.SearchabilityStatusNotSearchable: "不可检索",
	}
	return labels[value]
}

func loadEnabledDataSources(ctx *context.Context) []knowledge_model.DataSource {
	var sources []knowledge_model.DataSource
	if err := db.GetEngine(ctx).
		Where("status = ? AND security_level <> ?", knowledge_model.DataSourceStatusEnabled, knowledge_model.SecurityLevelProhibited).
		Asc("name").Find(&sources); err != nil {
		return nil
	}
	return sources
}

func parseKnowledgeDate(value string, required bool) (timeutil.TimeStamp, error) {
	if value == "" {
		if required {
			return 0, fmt.Errorf("date is required")
		}
		return 0, nil
	}
	parsed, err := time.ParseInLocation("2006-01-02", value, time.Local)
	if err != nil {
		return 0, err
	}
	return timeutil.TimeStamp(parsed.Unix()), nil
}

func loadPendingApprovals(ctx *context.Context) []dashboardApproval {
	var approvals []knowledge_model.Approval
	if err := db.GetEngine(ctx).Where("status = ?", knowledge_model.ApprovalStatusPending).Desc("requested_unix").Limit(20).Find(&approvals); err != nil {
		return nil
	}
	rows := make([]dashboardApproval, 0, len(approvals))
	for _, approval := range approvals {
		revision, exists, err := db.GetByID[knowledge_model.Revision](ctx, approval.RevisionID)
		if err != nil || !exists {
			continue
		}
		document, exists, err := db.GetByID[knowledge_model.Document](ctx, revision.DocumentID)
		if err != nil || !exists {
			continue
		}
		checks := loadReviewChecks(ctx, approval.RevisionID, ctx.Doer.IsAdmin && approval.RequestedBy != ctx.Doer.ID)
		allPassed := len(checks) == len(catalog.RequiredReviewTypes)
		for _, check := range checks {
			allPassed = allPassed && check.Status == "passed"
		}
		rows = append(rows, dashboardApproval{
			ID:           approval.ID,
			RevisionID:   approval.RevisionID,
			RequestedBy:  approval.RequestedBy,
			Status:       approval.Status,
			DocumentID:   document.ID,
			Title:        document.Title,
			FileName:     revision.FileName,
			ReviewChecks: checks,
			CanReview:    ctx.Doer.IsAdmin && approval.RequestedBy != ctx.Doer.ID,
			CanApprove:   ctx.Doer.IsAdmin && approval.RequestedBy != ctx.Doer.ID && allPassed,
		})
	}
	return rows
}

func loadReviewChecks(ctx *context.Context, revisionID int64, canReview bool) []dashboardReviewCheck {
	var checks []knowledge_model.ReviewCheck
	if err := db.GetEngine(ctx).Where("revision_id = ?", revisionID).Asc("id").Find(&checks); err != nil {
		return nil
	}
	rows := make([]dashboardReviewCheck, 0, len(checks))
	for _, check := range checks {
		rows = append(rows, dashboardReviewCheck{ReviewType: check.ReviewType, ReviewLabel: reviewTypeLabel(check.ReviewType), Status: check.Status, StatusLabel: reviewStatusLabel(check.Status), CanReview: canReview && check.Status == "pending"})
	}
	return rows
}

func reviewTypeLabel(value string) string {
	return map[string]string{"source": "数据源", "content": "内容质量", "permission": "权限范围", "sensitive": "敏感信息", "format": "格式可检索性", "release": "发布准入"}[value]
}

func reviewStatusLabel(value string) string {
	return map[string]string{"pending": "待审核", "passed": "已通过", "failed": "未通过"}[value]
}

func loadDifyBindings(ctx *context.Context) []knowledge_model.DifyBinding {
	var bindings []knowledge_model.DifyBinding
	if err := db.GetEngine(ctx).Desc("updated_unix").Limit(20).Find(&bindings); err != nil {
		return nil
	}
	return bindings
}

func loadRecentPublications(ctx *context.Context) []dashboardPublication {
	var publications []knowledge_model.Publication
	if err := db.GetEngine(ctx).Desc("updated_unix").Limit(20).Find(&publications); err != nil {
		return nil
	}
	rows := make([]dashboardPublication, 0, len(publications))
	for _, publication := range publications {
		rows = append(rows, dashboardPublication{
			ID:                   publication.ID,
			SpaceID:              publication.SpaceID,
			DocumentID:           publication.DocumentID,
			Generation:           publication.Generation,
			ValidityStatus:       publication.ValidityStatus,
			ValidityLabel:        validityStatusLabel(publication.ValidityStatus),
			IndexStatus:          publication.IndexStatus,
			IndexLabel:           indexStatusLabel(publication.IndexStatus),
			IsCurrent:            publication.IsCurrent,
			RevocationGeneration: publication.RevocationGeneration,
			CanActivate:          ctx.Doer.IsAdmin && publication.ValidityStatus == knowledge_model.ValidityStatusScheduled && publication.IndexStatus == knowledge_model.IndexStatusEvaluation,
			CanUnpublish:         ctx.Doer.IsAdmin && publication.IsCurrent && publication.ValidityStatus == knowledge_model.ValidityStatusCurrent,
			CanRetry:             ctx.Doer.IsAdmin && publication.ValidityStatus == knowledge_model.ValidityStatusScheduled && publication.IndexStatus == knowledge_model.IndexStatusFailed,
		})
	}
	return rows
}

func governanceStatusLabel(value knowledge_model.GovernanceStatus) string {
	labels := map[knowledge_model.GovernanceStatus]string{
		knowledge_model.GovernanceStatusDraft:     "草稿",
		knowledge_model.GovernanceStatusPending:   "待审核",
		knowledge_model.GovernanceStatusRejected:  "已驳回",
		knowledge_model.GovernanceStatusApproved:  "已批准",
		knowledge_model.GovernanceStatusWithdrawn: "已撤回",
	}
	return labels[value]
}

func validityStatusLabel(value knowledge_model.ValidityStatus) string {
	labels := map[knowledge_model.ValidityStatus]string{
		knowledge_model.ValidityStatusScheduled:   "待生效",
		knowledge_model.ValidityStatusCurrent:     "当前有效",
		knowledge_model.ValidityStatusExpired:     "已失效",
		knowledge_model.ValidityStatusUnpublished: "已下架",
		knowledge_model.ValidityStatusArchived:    "已归档",
	}
	return labels[value]
}

func indexStatusLabel(value knowledge_model.IndexStatus) string {
	labels := map[knowledge_model.IndexStatus]string{
		knowledge_model.IndexStatusUnindexed:  "未索引",
		knowledge_model.IndexStatusQueued:     "待处理",
		knowledge_model.IndexStatusIndexing:   "索引中",
		knowledge_model.IndexStatusEvaluation: "待评测",
		knowledge_model.IndexStatusSearchable: "可检索",
		knowledge_model.IndexStatusFailed:     "索引失败",
		knowledge_model.IndexStatusSuperseded: "已替代",
		knowledge_model.IndexStatusDeleting:   "删除中",
		knowledge_model.IndexStatusDeleted:    "已删除",
	}
	return labels[value]
}

func indexJobTypeLabel(value knowledge_model.IndexJobType) string {
	labels := map[knowledge_model.IndexJobType]string{
		knowledge_model.IndexJobTypeUpsert:  "写入索引",
		knowledge_model.IndexJobTypePoll:    "查询状态",
		knowledge_model.IndexJobTypeDelete:  "删除索引",
		knowledge_model.IndexJobTypeRebuild: "重建索引",
	}
	return labels[value]
}

func indexJobStatusLabel(value knowledge_model.IndexJobStatus) string {
	labels := map[knowledge_model.IndexJobStatus]string{
		knowledge_model.IndexJobStatusQueued:    "待处理",
		knowledge_model.IndexJobStatusRunning:   "处理中",
		knowledge_model.IndexJobStatusRetry:     "等待重试",
		knowledge_model.IndexJobStatusSucceeded: "已完成",
		knowledge_model.IndexJobStatusFailed:    "失败",
		knowledge_model.IndexJobStatusCancelled: "已取消",
	}
	return labels[value]
}
