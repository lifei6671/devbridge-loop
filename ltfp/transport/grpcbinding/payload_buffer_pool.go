package grpcbinding

import "sync"

const (
	defaultPooledPayloadBufferCapacity = 4 * 1024
	maxPooledPayloadBufferCapacity     = 256 * 1024
)

var grpcPayloadBufferPool = sync.Pool{
	New: func() any {
		return make([]byte, 0, defaultPooledPayloadBufferCapacity)
	},
}

func acquireGRPCPayloadBuffer(size int) ([]byte, bool) {
	if size <= 0 {
		return nil, false
	}
	if size > maxPooledPayloadBufferCapacity {
		return make([]byte, size), false
	}
	if pooledBuffer, ok := grpcPayloadBufferPool.Get().([]byte); ok {
		if cap(pooledBuffer) >= size {
			return pooledBuffer[:size], true
		}
		grpcPayloadBufferPool.Put(pooledBuffer[:0])
	}
	return make([]byte, size), false
}

func releaseGRPCPayloadBuffer(buffer []byte) {
	if cap(buffer) == 0 || cap(buffer) > maxPooledPayloadBufferCapacity {
		return
	}
	grpcPayloadBufferPool.Put(buffer[:0])
}
