//go:build unix

package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

type unixLocalRPCListener struct {
	listener *net.UnixListener
	endpoint string
}

func newPlatformLocalRPCListener(transport string, endpoint string) (localRPCListener, error) {
	if transport != "uds" {
		return nil, fmt.Errorf("ipc transport %s is not supported on unix", transport)
	}
	if endpoint == "" {
		return nil, errors.New("ipc endpoint is empty")
	}
	parentDir := filepath.Dir(endpoint)
	if parentDir == "." || parentDir == "" {
		return nil, errors.New("uds endpoint must have a parent directory")
	}
	if err := os.MkdirAll(parentDir, 0o700); err != nil {
		return nil, fmt.Errorf("create uds parent directory failed: %w", err)
	}
	if fileInfo, err := os.Lstat(endpoint); err == nil {
		if (fileInfo.Mode() & os.ModeSocket) != 0 {
			if removeErr := os.Remove(endpoint); removeErr != nil {
				return nil, fmt.Errorf("remove stale uds endpoint failed: %w", removeErr)
			}
		} else {
			return nil, fmt.Errorf("uds endpoint exists and is not a socket: %s", endpoint)
		}
	}
	listenAddr := &net.UnixAddr{Name: endpoint, Net: "unix"}
	listener, err := net.ListenUnix("unix", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("listen uds failed: %w", err)
	}
	if chmodErr := os.Chmod(endpoint, 0o600); chmodErr != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("set uds endpoint mode failed: %w", chmodErr)
	}
	return &unixLocalRPCListener{listener: listener, endpoint: endpoint}, nil
}

func (listener *unixLocalRPCListener) Accept(ctx context.Context) (localRPCConn, error) {
	if listener == nil || listener.listener == nil {
		return nil, errors.New("uds listener is nil")
	}
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		_ = listener.listener.SetDeadline(time.Now().Add(250 * time.Millisecond))
		connection, err := listener.listener.AcceptUnix()
		if err == nil {
			_ = listener.listener.SetDeadline(time.Time{})
			return connection, nil
		}
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			continue
		}
		if errors.Is(err, net.ErrClosed) {
			return nil, err
		}
		return nil, err
	}
}

func (listener *unixLocalRPCListener) Close() error {
	if listener == nil || listener.listener == nil {
		return nil
	}
	closeErr := listener.listener.Close()
	removeErr := os.Remove(listener.endpoint)
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		if closeErr != nil {
			return errors.Join(closeErr, removeErr)
		}
		return removeErr
	}
	return closeErr
}
