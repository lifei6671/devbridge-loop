//go:build !unix && !windows

package app

import "fmt"

func newPlatformLocalRPCListener(transport string, endpoint string) (localRPCListener, error) {
	_ = transport
	_ = endpoint
	return nil, fmt.Errorf("localrpc listener is not implemented for this platform")
}
