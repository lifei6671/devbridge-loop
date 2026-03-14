//go:build windows

package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync/atomic"

	"golang.org/x/sys/windows"
)

type windowsLocalRPCListener struct {
	endpoint string
	closed   uint32
}

func newPlatformLocalRPCListener(transport string, endpoint string) (localRPCListener, error) {
	if transport != "named_pipe" {
		return nil, fmt.Errorf("ipc transport %s is not supported on windows", transport)
	}
	normalizedEndpoint := strings.TrimSpace(endpoint)
	if normalizedEndpoint == "" {
		return nil, errors.New("named pipe endpoint is empty")
	}
	if !strings.HasPrefix(normalizedEndpoint, `\\.\pipe\`) {
		return nil, fmt.Errorf("invalid named pipe endpoint: %s", normalizedEndpoint)
	}
	return &windowsLocalRPCListener{endpoint: normalizedEndpoint}, nil
}

func (listener *windowsLocalRPCListener) createPipeHandle() (windows.Handle, error) {
	pipeName, err := windows.UTF16PtrFromString(listener.endpoint)
	if err != nil {
		return windows.InvalidHandle, fmt.Errorf("convert pipe endpoint failed: %w", err)
	}
	openMode := uint32(windows.PIPE_ACCESS_DUPLEX)
	pipeMode := uint32(
		windows.PIPE_TYPE_BYTE |
			windows.PIPE_READMODE_BYTE |
			windows.PIPE_WAIT |
			windows.PIPE_REJECT_REMOTE_CLIENTS,
	)
	bufferSize := uint32(localRPCHeaderLen + localRPCMaxBodyLen)
	handle, err := windows.CreateNamedPipe(
		pipeName,
		openMode,
		pipeMode,
		1,
		bufferSize,
		bufferSize,
		0,
		nil,
	)
	if err != nil {
		return windows.InvalidHandle, fmt.Errorf("create named pipe failed: %w", err)
	}
	return handle, nil
}

func (listener *windowsLocalRPCListener) Accept(ctx context.Context) (localRPCConn, error) {
	if listener == nil {
		return nil, errors.New("named pipe listener is nil")
	}
	if atomic.LoadUint32(&listener.closed) == 1 {
		return nil, net.ErrClosed
	}
	handle, err := listener.createPipeHandle()
	if err != nil {
		return nil, err
	}
	connectErrChan := make(chan error, 1)
	go func() {
		connectErr := windows.ConnectNamedPipe(handle, nil)
		if connectErr != nil && !errors.Is(connectErr, windows.ERROR_PIPE_CONNECTED) {
			connectErrChan <- connectErr
			return
		}
		connectErrChan <- nil
	}()

	select {
	case <-ctx.Done():
		_ = windows.CloseHandle(handle)
		return nil, ctx.Err()
	case connectErr := <-connectErrChan:
		if connectErr != nil {
			_ = windows.CloseHandle(handle)
			return nil, fmt.Errorf("connect named pipe client failed: %w", connectErr)
		}
		pipeFile := os.NewFile(uintptr(handle), listener.endpoint)
		if pipeFile == nil {
			_ = windows.CloseHandle(handle)
			return nil, errors.New("wrap named pipe handle failed")
		}
		return pipeFile, nil
	}
}

func (listener *windowsLocalRPCListener) Close() error {
	if listener == nil {
		return nil
	}
	atomic.StoreUint32(&listener.closed, 1)
	return nil
}
