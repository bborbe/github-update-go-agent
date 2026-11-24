// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/bborbe/errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-update-go-agent/pkg"
)

func osvNewFetcher(server *httptest.Server) pkg.OSVFetcher {
	return pkg.NewOSVHTTPFetcherForTest(
		server.URL+"/v1/vulns",
		server.URL+"/index/vulns.json",
	)
}

var _ = Describe("OSV HTTP fetcher", func() {
	ctx := context.Background()

	It("parses a 200 advisory record", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Expect(r.URL.Path).To(Equal("/v1/vulns/GO-2026-1234"))
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(pkg.OSVVulnerability{
				ID:        "GO-2026-1234",
				Withdrawn: "2026-01-01T00:00:00Z",
				Affected: []pkg.OSVAffected{{
					Package: pkg.OSVPackage{Ecosystem: "Go", Name: "example.com/dep"},
					Ranges: []pkg.OSVRange{{
						Type:   "SEMVER",
						Events: []pkg.OSVEvent{{Introduced: "0"}, {Fixed: "2.0.0"}},
					}},
				}},
			})
		}))
		defer server.Close()

		record, err := osvNewFetcher(server).FetchVuln(ctx, "GO-2026-1234")
		Expect(err).NotTo(HaveOccurred())
		Expect(record.ID).To(Equal("GO-2026-1234"))
		Expect(record.Withdrawn).To(Equal("2026-01-01T00:00:00Z"))
		Expect(record.Affected).To(HaveLen(1))
		Expect(record.Affected[0].Package.Name).To(Equal("example.com/dep"))
	})

	It("returns ErrAdvisoryNotFound on 404", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		record, err := osvNewFetcher(server).FetchVuln(ctx, "CVE-2026-33817")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, pkg.ErrAdvisoryNotFound)).To(BeTrue())
		Expect(record).To(BeNil())
	})

	It("redacts the body of a 500 response", func() {
		secret := "/internal/path/credentials.json missing — uid=42"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(secret))
		}))
		defer server.Close()

		_, err := osvNewFetcher(server).FetchVuln(ctx, "GO-2026-1234")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("status 500"))
		Expect(err.Error()).To(ContainSubstring("body_sha256_prefix="))
		Expect(err.Error()).To(ContainSubstring("body_bytes="))
		Expect(err.Error()).NotTo(ContainSubstring("/internal/path"))
		Expect(err.Error()).NotTo(ContainSubstring("credentials.json"))
	})

	It("parses the 200 Go vulnerability index", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			Expect(r.URL.Path).To(Equal("/index/vulns.json"))
			w.Header().Set("Content-Type", "application/json")
			// The endpoint serves a JSON ARRAY of {id, modified, aliases}, not a
			// module-keyed object. Encoding the Go type instead of a literal
			// would pass even when the type is wrong — the exact defect this
			// assertion exists to catch.
			_, _ = w.Write([]byte(
				`[{"id":"GO-2026-4923","modified":"2026-04-08T15:30:16Z",` +
					`"aliases":["CVE-2026-33817","GHSA-6jwv-w5xf-7j27"]}]`,
			))
		}))
		defer server.Close()

		index, err := osvNewFetcher(server).FetchGoIndex(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(index).NotTo(BeNil())
		Expect(*index).To(HaveLen(1))
		Expect((*index)[0].ID).To(Equal("GO-2026-4923"))
		Expect((*index)[0].Aliases).To(ContainElement("CVE-2026-33817"))
	})

	It("fails when a request exceeds the per-request timeout", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		defer server.Close()

		_, err := osvNewFetcher(server).FetchVuln(ctx, "GO-2026-1234")
		Expect(err).To(HaveOccurred())
	})
})
