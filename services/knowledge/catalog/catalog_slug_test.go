// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package catalog

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMakeSlugCreatesStableUniqueFallbackForChineseNames(t *testing.T) {
	testSlug := makeSlug("测试")

	assert.Regexp(t, regexp.MustCompile(`^knowledge-[a-f0-9]{8}$`), testSlug)
	assert.Equal(t, testSlug, makeSlug("测试"))
	assert.NotEqual(t, testSlug, makeSlug("客户知识库"))
}

func TestMakeSlugKeepsExistingASCIINamesStable(t *testing.T) {
	assert.Equal(t, "customer-knowledge", makeSlug("Customer Knowledge"))
}
