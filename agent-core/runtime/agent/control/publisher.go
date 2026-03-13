package control

import "context"

// Publisher emits control-plane events (publish/unpublish/service health).
type Publisher struct{}

func (p *Publisher) Publish(ctx context.Context) error {
	_ = ctx
	return nil
}
