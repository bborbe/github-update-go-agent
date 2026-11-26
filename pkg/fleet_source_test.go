// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pkg "github.com/bborbe/github-update-go-agent/pkg"
)

var _ = Describe("FleetScanSource", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("RepoLister", func() {
		It("follows pages and returns the sorted full_name values", func() {
			page1 := make([]map[string]string, 100)
			for i := range page1 {
				page1[i] = map[string]string{"full_name": fmt.Sprintf("bborbe/r%03d", i)}
			}
			page2 := []map[string]string{
				{"full_name": "bborbe/zzz"},
				{"full_name": "bborbe/aaa"},
			}
			var requestedPages []string
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					Expect(r.URL.Path).To(Equal("/installation/repositories"))
					page := r.URL.Query().Get("page")
					requestedPages = append(requestedPages, page)
					repos := page2
					if page == "1" {
						repos = page1
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"repositories": repos})
				}),
			)
			defer server.Close()

			lister := pkg.NewGitHubRepoListerForTest("token", server.URL)
			repos, err := lister.ListRepos(ctx, "bborbe")
			Expect(err).To(BeNil())
			Expect(requestedPages).To(Equal([]string{"1", "2"}))

			expected := make([]string, 0, 102)
			for _, repo := range page1 {
				expected = append(expected, repo["full_name"])
			}
			for _, repo := range page2 {
				expected = append(expected, repo["full_name"])
			}
			sort.Strings(expected)
			Expect(repos).To(Equal(expected))
		})

		It("returns ErrFleetRateLimited on HTTP 403", func() {
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusForbidden)
				}),
			)
			defer server.Close()

			lister := pkg.NewGitHubRepoListerForTest("token", server.URL)
			_, err := lister.ListRepos(ctx, "bborbe")
			Expect(err).NotTo(BeNil())
			Expect(stderrors.Is(err, pkg.ErrFleetRateLimited)).To(BeTrue())
		})
	})

	Describe("RepoFileFetcher", func() {
		It("returns the decoded bytes of a base64 file", func() {
			content := []byte("module github.com/bborbe/ip\n")
			wrapped := base64.StdEncoding.EncodeToString(content)
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					Expect(r.URL.Path).To(Equal("/repos/bborbe/ip/contents/go.mod"))
					Expect(r.URL.Query().Get("ref")).To(BeEmpty())
					_ = json.NewEncoder(w).Encode(map[string]string{
						"encoding": "base64",
						// GitHub wraps base64 across lines.
						"content": wrapped[:8] + "\n" + wrapped[8:],
					})
				}),
			)
			defer server.Close()

			fetcher := pkg.NewGitHubRepoFileFetcherForTest("token", server.URL)
			data, err := fetcher.FetchFile(ctx, "bborbe/ip", "go.mod")
			Expect(err).To(BeNil())
			Expect(data).To(Equal(content))
		})

		It("returns ErrFleetFileNotFound on HTTP 404", func() {
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNotFound)
				}),
			)
			defer server.Close()

			fetcher := pkg.NewGitHubRepoFileFetcherForTest("token", server.URL)
			_, err := fetcher.FetchFile(ctx, "bborbe/ip", "go.mod")
			Expect(stderrors.Is(err, pkg.ErrFleetFileNotFound)).To(BeTrue())
		})

		It("redacts the body on HTTP 500", func() {
			const secret = "internal-path-that-must-not-leak"
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(secret))
				}),
			)
			defer server.Close()

			fetcher := pkg.NewGitHubRepoFileFetcherForTest("token", server.URL)
			_, err := fetcher.FetchFile(ctx, "bborbe/ip", "go.mod")
			Expect(err).NotTo(BeNil())
			Expect(err.Error()).To(ContainSubstring("status 500"))
			Expect(err.Error()).NotTo(ContainSubstring(secret))
		})

		It("returns ErrFleetRateLimited on HTTP 429", func() {
			server := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusTooManyRequests)
				}),
			)
			defer server.Close()

			fetcher := pkg.NewGitHubRepoFileFetcherForTest("token", server.URL)
			_, err := fetcher.FetchFile(ctx, "bborbe/ip", "go.mod")
			Expect(stderrors.Is(err, pkg.ErrFleetRateLimited)).To(BeTrue())
		})
	})
})
