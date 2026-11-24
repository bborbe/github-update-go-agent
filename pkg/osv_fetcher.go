// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	stderrors "errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/bborbe/errors"
	"github.com/golang/glog"
)

// osvRequestTimeout caps a single OSV request. Set high enough to survive
// transient latency, low enough that one slow advisory cannot consume the
// whole classification budget.
const osvRequestTimeout = 10 * time.Second

// maxOSVBodyBytes caps how many bytes the fetcher reads from an OSV response.
// The Go index is a few MiB in practice; 64 MiB bounds a malicious or
// misconfigured upstream without rejecting a legitimate index.
const maxOSVBodyBytes = 64 << 20 // 64 MiB

//counterfeiter:generate -o ../mocks/osv_fetcher.go --fake-name OSVFetcher . OSVFetcher

// OSVFetcher fetches one advisory record by ID and the Go vulnerability index.
// Implementations MUST be safe for concurrent use.
type OSVFetcher interface {
	// FetchVuln returns the advisory record, or ErrAdvisoryNotFound on HTTP 404.
	FetchVuln(ctx context.Context, id string) (*OSVVulnerability, error)
	// FetchGoIndex returns the parsed Go vulnerability index.
	FetchGoIndex(ctx context.Context) (*GoVulnIndex, error)
}

// ErrAdvisoryNotFound is returned by FetchVuln on HTTP 404.
var ErrAdvisoryNotFound = stderrors.New("osv: advisory not found")

// NewOSVHTTPFetcher constructs the production fetcher against the public OSV
// API and the Go vulnerability index. The internal http.Client has a hard
// 10-second timeout per request.
func NewOSVHTTPFetcher() OSVFetcher {
	return newOSVHTTPFetcherWithBase(
		"https://api.osv.dev/v1/vulns",
		"https://vuln.go.dev/index/vulns.json",
	)
}

// newOSVHTTPFetcherWithBase is an internal constructor used by tests to point
// the fetcher at a test server. Not exported in the production API.
func newOSVHTTPFetcherWithBase(vulnBaseURL, indexURL string) OSVFetcher {
	return &osvHTTPFetcher{
		client:      &http.Client{Timeout: osvRequestTimeout},
		vulnBaseURL: vulnBaseURL,
		indexURL:    indexURL,
	}
}

type osvHTTPFetcher struct {
	client      *http.Client
	vulnBaseURL string
	indexURL    string
}

// FetchVuln GETs one advisory. A 404 is the ErrAdvisoryNotFound sentinel; any
// other non-2xx is a wrapped error carrying the status code and a body
// fingerprint, never the raw body.
func (f *osvHTTPFetcher) FetchVuln(
	ctx context.Context,
	id string,
) (*OSVVulnerability, error) {
	endpoint := f.vulnBaseURL + "/" + url.PathEscape(id)
	status, body, err := f.doGet(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		glog.V(2).Infof("osv: GET %s status=404 (not found)", osvURLPath(endpoint))
		return nil, ErrAdvisoryNotFound
	}
	if err := f.checkStatus(ctx, endpoint, status, body); err != nil {
		return nil, err
	}
	var vuln OSVVulnerability
	if err := json.Unmarshal(body, &vuln); err != nil {
		return nil, errors.Wrapf(ctx, err, "osv: decode advisory %s", id)
	}
	return &vuln, nil
}

// FetchGoIndex GETs the Go vulnerability index and decodes it. The body is
// capped by http.MaxBytesReader so a hostile index cannot exhaust memory.
func (f *osvHTTPFetcher) FetchGoIndex(ctx context.Context) (*GoVulnIndex, error) {
	status, body, err := f.doGet(ctx, f.indexURL)
	if err != nil {
		return nil, err
	}
	if err := f.checkStatus(ctx, f.indexURL, status, body); err != nil {
		return nil, err
	}
	var index GoVulnIndex
	if err := json.Unmarshal(body, &index); err != nil {
		return nil, errors.Wrapf(ctx, err, "osv: decode go index")
	}
	return &index, nil
}

// doGet performs the request and reads the capped body. The status code is
// returned rather than interpreted so each caller can decide how to treat a
// 404.
func (f *osvHTTPFetcher) doGet(
	ctx context.Context,
	endpoint string,
) (int, []byte, error) {
	// #nosec G107 -- endpoint is a hardcoded base URL plus an escaped
	// advisory id or the constant index URL.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, nil, errors.Wrapf(ctx, err, "osv: build request %s", osvURLPath(endpoint))
	}
	resp, err := f.client.Do(req)
	if err != nil {
		glog.V(2).Infof("osv: GET %s transport error: %v", osvURLPath(endpoint), err)
		return 0, nil, errors.Wrapf(ctx, err, "osv: GET %s", osvURLPath(endpoint))
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(http.MaxBytesReader(nil, resp.Body, maxOSVBodyBytes))
	if err != nil {
		return resp.StatusCode, nil, errors.Wrapf(
			ctx,
			err,
			"osv: read body %s (cap=%d bytes)",
			osvURLPath(endpoint),
			maxOSVBodyBytes,
		)
	}
	return resp.StatusCode, body, nil
}

// checkStatus turns a non-2xx response into a wrapped error. The body is
// redacted: a 5xx body from OSV or an intermediary can contain internal paths,
// header echoes, or partial traces. Surface the status (operator-actionable)
// and a short SHA-256 prefix so two reports of the same body correlate without
// exposing the bytes.
func (f *osvHTTPFetcher) checkStatus(
	ctx context.Context,
	endpoint string,
	status int,
	body []byte,
) error {
	if status < 200 || status >= 300 {
		sum := sha256.Sum256(body)
		fingerprint := hex.EncodeToString(sum[:])[:8]
		return errors.Errorf(
			ctx,
			"osv: GET %s status %d body_sha256_prefix=%s body_bytes=%d",
			osvURLPath(endpoint),
			status,
			fingerprint,
			len(body),
		)
	}
	glog.V(2).Infof(
		"osv: GET %s status=%d bytes=%d",
		osvURLPath(endpoint),
		status,
		len(body),
	)
	return nil
}

// osvURLPath returns the path of endpoint for logging, falling back to the
// whole endpoint when it cannot be parsed.
func osvURLPath(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil || u.Path == "" {
		return endpoint
	}
	return u.Path
}
