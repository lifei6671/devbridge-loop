package tcpbinding

import (
	"context"
	"fmt"
	"sync"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

type queuedControlWrite struct {
	priority  transport.ControlMessagePriority
	frame     transport.ControlFrame
	operation *controlWriteOperation
}

type controlWriteQueue struct {
	highPriorityWrites   []queuedControlWrite
	normalPriorityWrites []queuedControlWrite
	lowPriorityWrites    []queuedControlWrite
}

func newControlWriteQueue() *controlWriteQueue {
	return &controlWriteQueue{
		highPriorityWrites:   make([]queuedControlWrite, 0),
		normalPriorityWrites: make([]queuedControlWrite, 0),
		lowPriorityWrites:    make([]queuedControlWrite, 0),
	}
}

func (queue *controlWriteQueue) Enqueue(write queuedControlWrite) {
	if queue == nil {
		return
	}
	switch transport.NormalizeControlMessagePriority(write.priority) {
	case transport.ControlMessagePriorityHigh:
		queue.highPriorityWrites = append(queue.highPriorityWrites, write)
	case transport.ControlMessagePriorityLow:
		queue.lowPriorityWrites = append(queue.lowPriorityWrites, write)
	default:
		queue.normalPriorityWrites = append(queue.normalPriorityWrites, write)
	}
}

func (queue *controlWriteQueue) Dequeue() (queuedControlWrite, bool) {
	if queue == nil {
		return queuedControlWrite{}, false
	}
	if write, ok := dequeueControlWriteBucket(&queue.highPriorityWrites); ok {
		return write, true
	}
	if write, ok := dequeueControlWriteBucket(&queue.normalPriorityWrites); ok {
		return write, true
	}
	return dequeueControlWriteBucket(&queue.lowPriorityWrites)
}

func (queue *controlWriteQueue) Drain() []queuedControlWrite {
	if queue == nil {
		return nil
	}
	totalWrites := len(queue.highPriorityWrites) + len(queue.normalPriorityWrites) + len(queue.lowPriorityWrites)
	drainedWrites := make([]queuedControlWrite, 0, totalWrites)
	drainedWrites = append(drainedWrites, queue.highPriorityWrites...)
	drainedWrites = append(drainedWrites, queue.normalPriorityWrites...)
	drainedWrites = append(drainedWrites, queue.lowPriorityWrites...)
	queue.highPriorityWrites = queue.highPriorityWrites[:0]
	queue.normalPriorityWrites = queue.normalPriorityWrites[:0]
	queue.lowPriorityWrites = queue.lowPriorityWrites[:0]
	return drainedWrites
}

func (queue *controlWriteQueue) Len() int {
	if queue == nil {
		return 0
	}
	return len(queue.highPriorityWrites) + len(queue.normalPriorityWrites) + len(queue.lowPriorityWrites)
}

func dequeueControlWriteBucket(bucket *[]queuedControlWrite) (queuedControlWrite, bool) {
	if len(*bucket) == 0 {
		return queuedControlWrite{}, false
	}
	write := (*bucket)[0]
	*bucket = (*bucket)[1:]
	return write, true
}

type controlWriteOperation struct {
	ctx context.Context

	mutex              sync.Mutex
	doneChannel        chan struct{}
	remainingFragments int
	active             bool
	finished           bool
	err                error
}

func newControlWriteOperation(ctx context.Context, fragmentCount int) *controlWriteOperation {
	if ctx == nil {
		ctx = context.Background()
	}
	if fragmentCount <= 0 {
		fragmentCount = 1
	}
	return &controlWriteOperation{
		ctx:                ctx,
		doneChannel:        make(chan struct{}),
		remainingFragments: fragmentCount,
	}
}

func (operation *controlWriteOperation) Done() <-chan struct{} {
	if operation == nil {
		closedChannel := make(chan struct{})
		close(closedChannel)
		return closedChannel
	}
	return operation.doneChannel
}

func (operation *controlWriteOperation) Err() error {
	if operation == nil {
		return controlWriteReturnError(transport.ErrInvalidArgument)
	}
	operation.mutex.Lock()
	defer operation.mutex.Unlock()
	return operation.err
}

func (operation *controlWriteOperation) Watch(channel *TCPControlChannel) {
	if operation == nil || operation.ctx == nil || operation.ctx.Done() == nil {
		return
	}
	go func() {
		select {
		case <-operation.ctx.Done():
			operation.handleContextDone(channel)
		case <-operation.doneChannel:
		}
	}()
}

func (operation *controlWriteOperation) Begin() bool {
	if operation == nil {
		return false
	}
	operation.mutex.Lock()
	defer operation.mutex.Unlock()
	if operation.finished {
		return false
	}
	operation.active = true
	return true
}

func (operation *controlWriteOperation) MarkIdle() {
	if operation == nil {
		return
	}
	operation.mutex.Lock()
	defer operation.mutex.Unlock()
	if operation.finished {
		return
	}
	operation.active = false
}

func (operation *controlWriteOperation) CompleteFragment() {
	if operation == nil {
		return
	}
	operation.mutex.Lock()
	defer operation.mutex.Unlock()
	if operation.finished {
		return
	}
	operation.active = false
	operation.remainingFragments--
	if operation.remainingFragments > 0 {
		return
	}
	operation.finished = true
	close(operation.doneChannel)
}

func (operation *controlWriteOperation) Finish(err error) bool {
	if operation == nil {
		return false
	}
	operation.mutex.Lock()
	defer operation.mutex.Unlock()
	if operation.finished {
		return false
	}
	operation.active = false
	operation.finished = true
	operation.err = err
	close(operation.doneChannel)
	return true
}

func (operation *controlWriteOperation) IsFinished() bool {
	if operation == nil {
		return true
	}
	operation.mutex.Lock()
	defer operation.mutex.Unlock()
	return operation.finished
}

func (operation *controlWriteOperation) handleContextDone(channel *TCPControlChannel) {
	if operation == nil {
		return
	}
	ctxErr := operation.ctx.Err()
	if ctxErr == nil {
		return
	}
	operation.mutex.Lock()
	if operation.finished {
		operation.mutex.Unlock()
		return
	}
	if !operation.active {
		operation.finished = true
		operation.err = controlWriteReturnError(ctxErr)
		close(operation.doneChannel)
		operation.mutex.Unlock()
		if channel != nil {
			channel.notifyWriteLoop()
		}
		return
	}
	operation.mutex.Unlock()
	if channel != nil {
		channel.shutdownWithError(ctxErr)
	}
}

func controlWriteReturnError(err error) error {
	if err == nil {
		err = transport.ErrClosed
	}
	return fmt.Errorf("tcp control channel write: %w", err)
}
