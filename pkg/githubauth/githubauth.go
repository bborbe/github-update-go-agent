// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package githubauth resolves the github-update-go agent's effective GitHub
// credential at startup. It mirrors the github-releaser agent's resolution
// order: GitHub App installation token, or startup error. Extracted into
// its own package so the resolution outcomes are unit-testable against an
// httptest IAT endpoint (the pattern lib/githubapp tests already use).
package githubauth

import (
	"context"
	stderrors "errors"

	"github.com/bborbe/errors"
	githubapp "github.com/bborbe/maintainer/githubapp"
	"github.com/golang/glog"
)

// ErrAppCredentialsRequired is returned (wrapped) when no usable GitHub App
// credentials are configured at startup. Exposed as a sentinel so callers
// and tests can match it via errors.Is rather than string comparison.
var ErrAppCredentialsRequired = stderrors.New(
	"github-update-go auth: App credentials required",
)

// AuthMode classifies which credential type is active at pod startup.
type AuthMode int

const (
	// AuthModeNone means no usable App credential is configured; the caller
	// falls back to a raw GH_TOKEN (local cmd/run-task) or refuses to start.
	AuthModeNone AuthMode = iota
	// AuthModeGitHubApp means App credentials are present and an IAT will
	// be minted.
	AuthModeGitHubApp
)

// Config carries the raw credential inputs read from env/flags. Either a
// PEM file path (PEMKeyFile) or PEM env content (PEMKey) may be supplied;
// PEMKeyFile is preferred when both are present. BaseURL overrides the
// GitHub API base (defaults to https://api.github.com); tests point it at
// an httptest server.
type Config struct {
	AppID          int64
	InstallationID int64
	PEMKeyFile     string
	PEMKey         string
	BaseURL        string
}

// ResolveAuthMode picks the credential type to use at startup.
//   - AppID>0 AND InstallationID>0 AND (PEMKeyFile set OR PEMKey set) → AuthModeGitHubApp
//   - else → AuthModeNone
func ResolveAuthMode(appID, installationID int64, pemKeyFile, pemKey string) AuthMode {
	hasAppPEM := pemKeyFile != "" || pemKey != ""
	if appID > 0 && installationID > 0 && hasAppPEM {
		return AuthModeGitHubApp
	}
	return AuthModeNone
}

// Resolve returns the single effective GitHub token for the agent.
//
//   - App mode: mints an installation access token via lib/githubapp.MintIAT
//     (preferring PEMKeyFile over PEMKey when both are set).
//   - None: returns a non-nil error naming the required App env vars.
//
// The returned token is the bearer credential wired to the GitOps clone/push
// URLs, the gh CLI seam, and the Claude subprocess env. It is never logged
// in full (MintIAT logs only token_prefix).
//
// Token lifetime (known constraint): the App-mode IAT is minted ONCE at
// startup and is valid for ~1 hour (GitHub's max IAT lifetime); it is not
// refreshed during the run. A github-update-go task — clone, update, gate,
// push, draft PR — completes well under the IAT lifetime for typical repos.
func Resolve(ctx context.Context, cfg Config) (string, error) {
	switch ResolveAuthMode(cfg.AppID, cfg.InstallationID, cfg.PEMKeyFile, cfg.PEMKey) {
	case AuthModeGitHubApp:
		appCfg := githubapp.Config{
			AppID:          cfg.AppID,
			InstallationID: cfg.InstallationID,
			BaseURL:        cfg.BaseURL,
		}
		if cfg.PEMKeyFile != "" {
			appCfg.PEMPath = cfg.PEMKeyFile
		} else {
			appCfg.PEM = []byte(cfg.PEMKey)
		}
		iat, err := githubapp.MintIAT(ctx, appCfg)
		if err != nil {
			return "", errors.Wrap(ctx, err, "mint github app iat")
		}
		glog.V(1).Infof(
			"github-update-go: minted github-app iat app_id=%d installation_id=%d",
			cfg.AppID, cfg.InstallationID,
		)
		return iat, nil
	default:
		// AuthModeNone (or any future unhandled mode): no usable credential.
		return "", errors.Wrap(
			ctx,
			ErrAppCredentialsRequired,
			"set APP_ID + INSTALLATION_ID + (PEM_KEY_FILE or PEM_KEY)",
		)
	}
}
