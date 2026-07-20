// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package knowledge

import (
	"encoding/json"
	"net/http"
	"strings"

	"code.gitea.io/gitea/modules/setting"
	"code.gitea.io/gitea/services/context"
	"code.gitea.io/gitea/services/knowledge/retrieval"
)

type difyRetrievalRequest struct {
	KnowledgeID      string `json:"knowledge_id"`
	Query            string `json:"query"`
	RetrievalSetting struct {
		TopK           int     `json:"top_k"`
		ScoreThreshold float64 `json:"score_threshold"`
	} `json:"retrieval_setting"`
	MetadataCondition json.RawMessage `json:"metadata_condition"`
}

type difyRetrievalRecord struct {
	Content  string         `json:"content"`
	Score    float64        `json:"score"`
	Title    string         `json:"title"`
	Metadata map[string]any `json:"metadata"`
}

// ExternalRetrieval implements Dify's External Knowledge API contract. It is
// deliberately unauthenticated at the FileBay session level: authorization is
// a fixed-scope application Bearer token stored only as a digest in FileBay.
func ExternalRetrieval(ctx *context.Context) {
	if !settingEnabled(ctx) {
		externalError(ctx, http.StatusNotFound, 2001, "知识库不存在")
		return
	}
	token, ok := bearerToken(ctx.Req.Header.Get("Authorization"))
	if !ok {
		externalError(ctx, http.StatusUnauthorized, 1001, "授权格式错误")
		return
	}
	ctx.Req.Body = http.MaxBytesReader(ctx.Resp, ctx.Req.Body, 64<<10)
	defer ctx.Req.Body.Close()
	request := new(difyRetrievalRequest)
	decoder := json.NewDecoder(ctx.Req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(request); err != nil {
		externalError(ctx, http.StatusBadRequest, 1001, "检索请求格式错误")
		return
	}
	binding, err := retrieval.LookupDifyBinding(ctx, token, request.KnowledgeID)
	if err != nil {
		externalError(ctx, http.StatusUnauthorized, 1002, "授权失败")
		return
	}
	result, err := retrieval.RetrieveForDify(ctx, binding, request.Query, request.RetrievalSetting.TopK)
	if err != nil {
		externalError(ctx, http.StatusBadRequest, 2001, "知识库检索不可用")
		return
	}
	records := make([]difyRetrievalRecord, 0, len(result.Citations))
	for _, citation := range result.Citations {
		records = append(records, difyRetrievalRecord{
			Content: citation.Content,
			Score:   citation.Score,
			Title:   citation.Title,
			Metadata: map[string]any{
				"citation_id":    citation.PublicationID,
				"document_id":    citation.DocumentID,
				"revision_id":    citation.RevisionID,
				"source":         "FileBay 企业知识库",
				"security_scope": binding.MaxSecurityLevel,
			},
		})
	}
	ctx.JSON(http.StatusOK, map[string]any{"records": records})
}

func bearerToken(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
		return "", false
	}
	return parts[1], true
}

func externalError(ctx *context.Context, status, code int, message string) {
	ctx.JSON(status, map[string]any{"error_code": code, "message": message})
}

func settingEnabled(ctx *context.Context) bool {
	return ctx != nil && setting.Knowledge.Enabled
}
