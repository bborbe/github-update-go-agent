// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory

import (
	task "github.com/bborbe/agent/command/task"
	"github.com/bborbe/cqrs/base"
	"github.com/bborbe/cqrs/cdb"
	libkafka "github.com/bborbe/kafka"
	"github.com/bborbe/log"

	updatepkg "github.com/bborbe/github-update-go-agent/pkg"
)

// CreateKafkaScanTaskStore wires the fleet scan's publish path over the
// controller's Kafka command bus, exactly as CreateBuildFixChainEmitter does.
// The producer is passed in pre-built; main.go is its single close owner.
func CreateKafkaScanTaskStore(
	syncProducer libkafka.SyncProducer,
	topicPrefix base.TopicPrefix,
) updatepkg.ScanTaskStore {
	sender := cdb.NewCommandObjectSender(syncProducer, topicPrefix, log.DefaultSamplerFactory)
	createSender := task.NewCreateCommandSender(sender, "")
	return updatepkg.NewKafkaScanTaskStore(createSender)
}
