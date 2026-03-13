package session

import "context"

// State defines the session lifecycle states.
type State string

const (
	StateConnecting      State = "CONNECTING"
	StateAuthenticating  State = "AUTHENTICATING"
	StateActive          State = "ACTIVE"
	StateDraining        State = "DRAINING"
	StateStale           State = "STALE"
	StateClosed          State = "CLOSED"
)

// Manager owns session lifecycle and epoch handling.
type Manager struct {
	state State
}

func NewManager() *Manager {
	return &Manager{state: StateConnecting}
}

func (m *Manager) Start(ctx context.Context) error {
	_ = ctx
	m.state = StateActive
	return nil
}

func (m *Manager) Stop(ctx context.Context) error {
	_ = ctx
	m.state = StateClosed
	return nil
}

func (m *Manager) State() State {
	return m.state
}
