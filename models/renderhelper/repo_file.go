// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package renderhelper

import (
	"context"
)

type RepoFile struct {
	ctx  *context.Context
	opts RepoFileOptions

	commitChecker *commitChecker
	repoLink      string
}

func (r *RepoFile) CleanUp() {
	_ = r.commitChecker.Close()
}

func (r *RepoFile) IsCommitIDExisting(commitID string) bool {
	return r.commitChecker.IsCommitIDExisting(commitID)
}

func (r *RepoFile) ResolveLink(link, preferLinkType string) (finalLink string) {
	return link
}

type RepoFileOptions struct {
	DeprecatedRepoName  string // it is only a patch for the non-standard "markup" api
	DeprecatedOwnerName string // it is only a patch for the non-standard "markup" api

	CurrentRefPath  string // eg: "branch/main"
	CurrentTreePath string // eg: "path/to/file" in the repo
}
