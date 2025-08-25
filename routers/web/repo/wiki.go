// Copyright 2015 The Gogs Authors. All rights reserved.
// Copyright 2018 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"code.gitea.io/gitea/modules/timeutil"
	"code.gitea.io/gitea/services/context"
)

// PageMeta wiki page meta information
type PageMeta struct {
	Name         string
	SubURL       string
	GitEntryName string
	UpdatedUnix  timeutil.TimeStamp
}

// TODO wiki: Replace with new wiki system
// Wiki placeholder function
func Wiki(ctx *context.Context) {
	// Placeholder - wiki functionality disabled
	ctx.PlainText(200, "Wiki functionality temporarily disabled")
}

// WikiPost placeholder function
func WikiPost(ctx *context.Context) {
	// Placeholder - wiki functionality disabled
	ctx.PlainText(200, "Wiki functionality temporarily disabled")
}

// WikiRaw placeholder function
func WikiRaw(ctx *context.Context) {
	// Placeholder - wiki functionality disabled
	ctx.PlainText(200, "Wiki functionality temporarily disabled")
}
