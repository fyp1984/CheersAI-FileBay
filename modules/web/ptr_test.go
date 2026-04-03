// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package web

func ptr[T any](v T) *T {
	return &v
}
