package tunnel

import "time"

// TTLReaper closes idle tunnels past TTL.
type TTLReaper struct {
	TTL time.Duration
}
