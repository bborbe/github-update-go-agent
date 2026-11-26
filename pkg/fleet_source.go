// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/bborbe/errors"
	"github.com/golang/glog"
)

// fleetRequestTimeout caps every GitHub API call the fleet scan makes.
const fleetRequestTimeout = 15 * time.Second

// maxFleetBodyBytes caps how many bytes a fleet request reads from GitHub.
// go.mod and repository listings are small; 1 MiB bounds a hostile or
// misconfigured upstream without exhausting agent memory.
const maxFleetBodyBytes = 1 << 20 // 1 MiB

// fleetPerPage is the GitHub pagination page size for repository listing.
const fleetPerPage = 100

// fleetAPIBase is the production GitHub REST API base URL.
const fleetAPIBase = "https://api.github.com"

// ErrFleetFileNotFound is returned by FetchFile on HTTP 404.
var ErrFleetFileNotFound = stderrors.New("fleet: file not found at default branch")

// ErrFleetRateLimited is returned by either seam on HTTP 403 or 429, so the
// scan can stop the run instead of treating a rate limit as a per-repo skip.
var ErrFleetRateLimited = stderrors.New("fleet: github rate limited")

//counterfeiter:generate -o ../mocks/repo_lister.go --fake-name RepoLister . RepoLister

// RepoLister enumerates the repositories the GitHub App installation can read.
// Implementations MUST be safe for concurrent use.
type RepoLister interface {
	// ListRepos returns the owner's repositories as "owner/name", sorted, one
	// entry per repo. An empty result is not an error.
	ListRepos(ctx context.Context, owner string) ([]string, error)
}

//counterfeiter:generate -o ../mocks/repo_file_fetcher.go --fake-name RepoFileFetcher . RepoFileFetcher

// RepoFileFetcher reads one small file from a repository's default branch.
// Implementations MUST be safe for concurrent use.
type RepoFileFetcher interface {
	// FetchFile returns the file's bytes at the repo's default branch, or
	// ErrFleetFileNotFound when the path does not exist there.
	FetchFile(ctx context.Context, repo, path string) ([]byte, error)
}

// NewGitHubRepoLister constructs a RepoLister backed by net/http against
// api.github.com. token is the bearer token; empty sends no Authorization
// header.
func NewGitHubRepoLister(token string) RepoLister {
	return newGitHubRepoListerWithBase(token, fleetAPIBase)
}

// NewGitHubRepoFileFetcher constructs a RepoFileFetcher backed by net/http
// against api.github.com. token is the bearer token; empty sends no
// Authorization header.
func NewGitHubRepoFileFetcher(token string) RepoFileFetcher {
	return newGitHubRepoFileFetcherWithBase(token, fleetAPIBase)
}

// newGitHubRepoListerWithBase is the test seam for pointing the lister at an
// httptest server.
func newGitHubRepoListerWithBase(token, apiBase string) RepoLister {
	return newFleetHTTPClient(token, apiBase)
}

// newGitHubRepoFileFetcherWithBase is the test seam for pointing the fetcher
// at an httptest server.
func newGitHubRepoFileFetcherWithBase(token, apiBase string) RepoFileFetcher {
	return newFleetHTTPClient(token, apiBase)
}

// fleetHTTPClient implements both GitHub read seams over a single shared
// *http.Client.
type fleetHTTPClient struct {
	client  *http.Client
	token   string
	apiBase string
}

func newFleetHTTPClient(token, apiBase string) *fleetHTTPClient {
	return &fleetHTTPClient{
		client:  &http.Client{Timeout: fleetRequestTimeout},
		token:   token,
		apiBase: apiBase,
	}
}

// reposResponse is the slim JSON shape read from /installation/repositories.
type reposResponse struct {
	Repositories []struct {
		FullName string `json:"full_name"`
	} `json:"repositories"`
}

// fleetContentResponse is the slim JSON shape read from the contents API.
type fleetContentResponse struct {
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
}

// ListRepos implements RepoLister. It walks pages until a page returns fewer
// than fleetPerPage repositories, then sorts the combined result.
func (c *fleetHTTPClient) ListRepos(ctx context.Context, owner string) ([]string, error) {
	glog.V(3).Infof("fleet: list repos owner=%s", owner)
	var all []string
	for page := 1; ; page++ {
		path := fmt.Sprintf(
			"/installation/repositories?per_page=%d&page=%d",
			fleetPerPage,
			page,
		)
		body, status, err := c.doGet(ctx, path)
		if err != nil {
			return nil, err
		}
		if err := fleetCheckStatus(ctx, "list repositories", status, body); err != nil {
			return nil, err
		}
		var rr reposResponse
		if err := json.Unmarshal(body, &rr); err != nil {
			return nil, errors.Wrapf(ctx, err, "fleet: decode repositories page %d", page)
		}
		for _, repo := range rr.Repositories {
			all = append(all, repo.FullName)
		}
		if len(rr.Repositories) < fleetPerPage {
			break
		}
	}
	sort.Strings(all)
	return all, nil
}

// FetchFile implements RepoFileFetcher. It reads a file at the repository's
// default branch (no ref query parameter) and base64-decodes the response.
func (c *fleetHTTPClient) FetchFile(ctx context.Context, repo, path string) ([]byte, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return nil, errors.Errorf(ctx, "fleet: malformed repo %q", repo)
	}
	endpoint := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) +
		"/contents/" + escapeContentPath(path)
	body, status, err := c.doGet(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, ErrFleetFileNotFound
	}
	if err := fleetCheckStatus(ctx, "fetch file "+repo+"/"+path, status, body); err != nil {
		return nil, err
	}
	return decodeFleetContent(ctx, body)
}

// doGet issues one authenticated GET and returns the body plus status code.
// The token is never logged; only the request path is.
func (c *fleetHTTPClient) doGet(
	ctx context.Context,
	path string,
) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase+path, nil)
	if err != nil {
		return nil, 0, errors.Wrapf(ctx, err, "fleet: build request %s", path)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, 0, errors.Wrapf(ctx, err, "fleet: http %s", path)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(http.MaxBytesReader(nil, resp.Body, maxFleetBodyBytes))
	if err != nil {
		return nil, resp.StatusCode, errors.Wrapf(ctx, err, "fleet: read body %s", path)
	}
	glog.V(2).Infof(
		"fleet: GET %s status=%d bytes=%d",
		req.URL.Path,
		resp.StatusCode,
		len(body),
	)
	return body, resp.StatusCode, nil
}

// fleetCheckStatus maps a non-2xx status to an error. 403/429 wrap
// ErrFleetRateLimited; every other status carries a redacted fingerprint.
func fleetCheckStatus(ctx context.Context, op string, status int, body []byte) error {
	if status >= 200 && status < 300 {
		return nil
	}
	if status == http.StatusForbidden || status == http.StatusTooManyRequests {
		return errors.Wrapf(ctx, ErrFleetRateLimited, "%s: status %d", op, status)
	}
	sum := sha256.Sum256(body)
	fingerprint := hex.EncodeToString(sum[:])[:8]
	return errors.Errorf(
		ctx,
		"%s: status %d body_sha256_prefix=%s body_bytes=%d",
		op,
		status,
		fingerprint,
		len(body),
	)
}

// decodeFleetContent base64-decodes the contents-API response. GitHub wraps
// the base64 string across lines, so newlines are stripped before decoding.
func decodeFleetContent(ctx context.Context, body []byte) ([]byte, error) {
	var cr fleetContentResponse
	if err := json.Unmarshal(body, &cr); err != nil {
		return nil, errors.Wrapf(ctx, err, "fleet: decode contents json")
	}
	if cr.Encoding != "base64" {
		return nil, errors.Errorf(
			ctx,
			"fleet: unsupported encoding %q (want base64)",
			cr.Encoding,
		)
	}
	cleaned := strings.NewReplacer("\n", "", "\r", "").Replace(cr.Content)
	decoded, err := base64.StdEncoding.DecodeString(cleaned)
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "fleet: base64 decode")
	}
	return decoded, nil
}

// escapeContentPath escapes each segment of a contents-API path, preserving
// the separating slashes.
func escapeContentPath(path string) string {
	parts := strings.Split(path, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}
