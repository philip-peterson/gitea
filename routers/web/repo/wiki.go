// Copyright 2015 The Gogs Authors. All rights reserved.
// Copyright 2018 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"bytes"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"code.gitea.io/gitea/models/renderhelper"
	repo_model "code.gitea.io/gitea/models/repo"
	"code.gitea.io/gitea/models/unit"
	"code.gitea.io/gitea/modules/base"
	"code.gitea.io/gitea/modules/charset"
	"code.gitea.io/gitea/modules/git"
	"code.gitea.io/gitea/modules/gitrepo"
	"code.gitea.io/gitea/modules/log"

	"code.gitea.io/gitea/modules/setting"
	"code.gitea.io/gitea/modules/templates"
	"code.gitea.io/gitea/modules/timeutil"
	"code.gitea.io/gitea/modules/util"
	"code.gitea.io/gitea/modules/web"
	"code.gitea.io/gitea/routers/common"
	"code.gitea.io/gitea/services/context"
	"code.gitea.io/gitea/services/forms"
	git_service "code.gitea.io/gitea/services/git"
	notify_service "code.gitea.io/gitea/services/notify"
	wiki_service "code.gitea.io/gitea/services/wiki"
)

// PageMeta wiki page meta information
type PageMeta struct {
	Name         string
	SubURL       string
	GitEntryName string
	UpdatedUnix  timeutil.TimeStamp
}
