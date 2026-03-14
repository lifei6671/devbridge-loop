package app

import (
	"context"
	"io"
)

type localRPCConn interface {
	io.ReadWriteCloser
}

type localRPCListener interface {
	Accept(ctx context.Context) (localRPCConn, error)
	Close() error
}

func newLocalRPCListener(transport string, endpoint string) (localRPCListener, error) {
	return newPlatformLocalRPCListener(transport, endpoint)
}
