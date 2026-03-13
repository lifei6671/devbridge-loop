package tunnel

import "context"

// Manager controls tunnel pool lifecycle.
type Manager struct{}

func (m *Manager) Start(ctx context.Context) error {
	_ = ctx
	return nil
}
