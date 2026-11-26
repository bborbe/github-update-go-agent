// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"

	agentlib "github.com/bborbe/agent"
	task "github.com/bborbe/agent/command/task"
	"github.com/bborbe/errors"
)

//counterfeiter:generate -o ../mocks/scan_command_sender.go --fake-name ScanCommandSender . ScanCommandSender

// ScanCommandSender is the publish seam. task.CreateCommandSender satisfies it
// structurally, so the production wiring passes the real sender.
type ScanCommandSender interface {
	SendCommand(ctx context.Context, cmd task.CreateCommand) error
}

//counterfeiter:generate -o ../mocks/scan_task_store.go --fake-name ScanTaskStore . ScanTaskStore

// ScanTaskStore is the controller boundary: it answers whether an identifier is
// already present and files a task when it is not.
type ScanTaskStore interface {
	// Has reports whether a task with this identifier already exists.
	Has(ctx context.Context, identifier agentlib.TaskIdentifier) (bool, error)
	// Publish files one task.
	Publish(ctx context.Context, cmd task.CreateCommand) error
}

// NewKafkaScanTaskStore returns the production store over the controller's
// create-task sender.
//
// Has currently reports false: the controller's create handler is the only
// place that knows whether a title path is occupied, and it signals a collision
// on the result topic, which this Job does not consume. The controller's
// title-path dedup still makes a re-publish a no-op, so the fleet never gains a
// duplicate task; only the already_recorded count under-reports until a task
// lookup path exists.
func NewKafkaScanTaskStore(sender ScanCommandSender) ScanTaskStore {
	return &kafkaScanTaskStore{sender: sender}
}

type kafkaScanTaskStore struct {
	sender ScanCommandSender
}

// Has implements ScanTaskStore. See NewKafkaScanTaskStore for why it reports
// false in production.
func (s *kafkaScanTaskStore) Has(
	_ context.Context,
	_ agentlib.TaskIdentifier,
) (bool, error) {
	return false, nil
}

// Publish implements ScanTaskStore.
func (s *kafkaScanTaskStore) Publish(ctx context.Context, cmd task.CreateCommand) error {
	if err := s.sender.SendCommand(ctx, cmd); err != nil {
		return errors.Wrap(ctx, err, "publish fleet scan task")
	}
	return nil
}
