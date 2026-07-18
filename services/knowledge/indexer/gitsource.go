// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package indexer

import (
	"context"
	"errors"
	"io"
	"strings"

	repo_model "code.gitea.io/gitea/models/repo"
	"code.gitea.io/gitea/modules/gitrepo"
)

// GitSourceProvider reads masked artifacts from authoritative FileBay Git
// repositories at exact immutable commits.
type GitSourceProvider struct{}

// Open returns the blob stream for repoPath at commitSHA.
func (GitSourceProvider) Open(ctx context.Context, repoID int64, commitSHA, repoPath string) (io.ReadCloser, error) {
	if repoID <= 0 || strings.TrimSpace(commitSHA) == "" || strings.TrimSpace(repoPath) == "" {
		return nil, errors.New("invalid knowledge git source")
	}
	repo, err := repo_model.GetRepositoryByID(ctx, repoID)
	if err != nil {
		return nil, err
	}
	gitRepo, closer, err := gitrepo.RepositoryFromContextOrOpen(ctx, repo)
	if err != nil {
		return nil, err
	}
	commit, err := gitRepo.GetCommit(commitSHA)
	if err != nil {
		closer.Close()
		return nil, err
	}
	blob, err := commit.GetBlobByPath(repoPath)
	if err != nil {
		closer.Close()
		return nil, err
	}
	reader, err := blob.DataAsync()
	if err != nil {
		closer.Close()
		return nil, err
	}
	return &closingReader{ReadCloser: reader, close: closer.Close}, nil
}

type closingReader struct {
	io.ReadCloser
	close func() error
}

func (r *closingReader) Close() error {
	readerErr := r.ReadCloser.Close()
	closerErr := r.close()
	if readerErr != nil {
		return readerErr
	}
	return closerErr
}
