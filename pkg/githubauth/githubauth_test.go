// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package githubauth_test

import (
	"context"
	stderrors "errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-update-go-agent/pkg/githubauth"
)

var _ = Describe("ResolveAuthMode", func() {
	DescribeTable("picks the credential type",
		func(appID, installationID int64, pemFile, pemKey string, expected githubauth.AuthMode) {
			Expect(
				githubauth.ResolveAuthMode(appID, installationID, pemFile, pemKey),
			).To(Equal(expected))
		},
		Entry("full app creds via file", int64(1), int64(2), "/pem", "", githubauth.AuthModeGitHubApp),
		Entry("full app creds via content", int64(1), int64(2), "", "PEM", githubauth.AuthModeGitHubApp),
		Entry("missing pem", int64(1), int64(2), "", "", githubauth.AuthModeNone),
		Entry("missing installation", int64(1), int64(0), "/pem", "", githubauth.AuthModeNone),
		Entry("missing app id", int64(0), int64(2), "/pem", "", githubauth.AuthModeNone),
		Entry("nothing", int64(0), int64(0), "", "", githubauth.AuthModeNone),
	)
})

var _ = Describe("Resolve", func() {
	It("returns ErrAppCredentialsRequired when nothing is configured", func() {
		_, err := githubauth.Resolve(context.Background(), githubauth.Config{})
		Expect(err).To(HaveOccurred())
		Expect(stderrors.Is(err, githubauth.ErrAppCredentialsRequired)).To(BeTrue())
	})
})

var _ = Describe("NewGhAuthSetupGit", func() {
	It("is a no-op for an empty token", func() {
		Expect(githubauth.NewGhAuthSetupGit("").Setup(context.Background())).To(Succeed())
	})
})

var _ = Describe("NewNoopAuthSetup", func() {
	It("always succeeds", func() {
		Expect(githubauth.NewNoopAuthSetup().Setup(context.Background())).To(Succeed())
	})
})
