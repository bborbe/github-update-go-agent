// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	stderrors "errors"

	agentlib "github.com/bborbe/agent"
	task "github.com/bborbe/agent/command/task"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-update-go-agent/mocks"
	pkg "github.com/bborbe/github-update-go-agent/pkg"
)

var _ = Describe("FleetScanTaskStore", func() {
	var (
		ctx    context.Context
		sender *mocks.ScanCommandSender
		store  pkg.ScanTaskStore
	)

	BeforeEach(func() {
		ctx = context.Background()
		sender = &mocks.ScanCommandSender{}
		store = pkg.NewKafkaScanTaskStore(sender)
	})

	It("forwards the exact command to SendCommand", func() {
		cmd := task.CreateCommand{
			TaskIdentifier: agentlib.TaskIdentifier("abc"),
			Title:          "Update Go bborbe-ip 0123456",
		}
		Expect(store.Publish(ctx, cmd)).To(BeNil())
		Expect(sender.SendCommandCallCount()).To(Equal(1))
		_, got := sender.SendCommandArgsForCall(0)
		Expect(got).To(Equal(cmd))
	})

	It("wraps a sender error and preserves errors.Is", func() {
		sentinel := stderrors.New("send failed")
		sender.SendCommandReturns(sentinel)
		err := store.Publish(ctx, task.CreateCommand{Title: "x"})
		Expect(err).NotTo(BeNil())
		Expect(stderrors.Is(err, sentinel)).To(BeTrue())
	})

	It("reports Has as false", func() {
		has, err := store.Has(ctx, agentlib.TaskIdentifier("abc"))
		Expect(err).To(BeNil())
		Expect(has).To(BeFalse())
	})
})
