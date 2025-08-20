// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package renderhelper

import (
	"context"

	repo_model "code.gitea.io/gitea/models/repo"
)

type RepoComment struct {
	ctx  context.Context
	opts RepoCommentOptions

	commitChecker *commitChecker
	repoLink      string
}

func (r *RepoComment) CleanUp() {
	_ = r.commitChecker.Close()
}

func (r *RepoComment) IsCommitIDExisting(commitID string) bool {
	return r.commitChecker.IsCommitIDExisting(commitID)
}

func (r *RepoComment) ResolveLink(link, preferLinkType string) string {
	return link
}

type RepoCommentOptions struct {
	DeprecatedRepoName  string // it is only a patch for the non-standard "markup" api
	DeprecatedOwnerName string // it is only a patch for the non-standard "markup" api
	CurrentRefPath      string // eg: "branch/main" or "commit/11223344"

	// TODO remove me:
	FootnoteContextID string // the extra context ID for footnotes, used to avoid conflicts with other footnotes in the same page
}

func NewRenderContextRepoComment(ctx context.Context, repo *repo_model.Repository, opts ...RepoCommentOptions) *context.Context {
	return &ctx
}
