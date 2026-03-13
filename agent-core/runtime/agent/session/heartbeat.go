package session

import "time"

// HeartbeatScheduler controls heartbeat cadence.
type HeartbeatScheduler struct {
	Interval time.Duration
}
