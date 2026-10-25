// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-update-go-agent/pkg"
)

var _ = Describe("ghSubprocessEnv", func() {
	proxyKeys := []string{
		"HTTPS_PROXY",
		"https_proxy",
		"HTTP_PROXY",
		"http_proxy",
		"NO_PROXY",
		"no_proxy",
	}

	// setEnv sets key for the duration of the spec and restores the
	// previous value (or absence) afterwards.
	setEnv := func(key, value string) {
		previous, had := os.LookupEnv(key)
		DeferCleanup(func() {
			if had {
				Expect(os.Setenv(key, previous)).To(Succeed())
				return
			}
			Expect(os.Unsetenv(key)).To(Succeed())
		})
		Expect(os.Setenv(key, value)).To(Succeed())
	}

	It("passes each proxy variable and GH_TOKEN through", func() {
		for _, key := range proxyKeys {
			setEnv(key, "value-of-"+key)
		}

		env := pkg.GhSubprocessEnvForTest("gh-token")

		for _, key := range proxyKeys {
			Expect(env).To(ContainElement(key + "=value-of-" + key))
		}
		Expect(env).To(ContainElement("GH_TOKEN=gh-token"))
	})

	It("omits GH_TOKEN when no token is given", func() {
		env := pkg.GhSubprocessEnvForTest("")

		for _, entry := range env {
			Expect(entry).NotTo(HavePrefix("GH_TOKEN="))
		}
	})

	It("does not leak unrelated pod-level variables", func() {
		setEnv("SOME_TOKEN", "secret-value")
		setEnv("GITHUB_TOKEN", "secret-value-2")
		for _, key := range proxyKeys {
			setEnv(key, "http://proxy:3128")
		}

		env := pkg.GhSubprocessEnvForTest("gh-token")

		Expect(env).To(HaveLen(2 + len(proxyKeys) + 1))
		for _, entry := range env {
			Expect(entry).NotTo(HavePrefix("SOME_TOKEN="))
			Expect(entry).NotTo(HavePrefix("GITHUB_TOKEN="))
			Expect(entry).NotTo(ContainSubstring("secret-value"))
		}
	})
})
