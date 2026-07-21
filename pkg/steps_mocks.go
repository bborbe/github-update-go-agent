// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

// ClaudeRunner fake for step tests — the runner interface lives in the
// agent lib; the fake is generated into this repo's mocks package.
//
//counterfeiter:generate -o ../mocks/claude_runner.go --fake-name ClaudeRunnerMock github.com/bborbe/agent/claude.ClaudeRunner
