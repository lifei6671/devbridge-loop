package session

import "context"

// Authenticator performs session authentication.
type Authenticator interface {
	Authenticate(ctx context.Context) error
}

// NoopAuthenticator is a placeholder implementation.
type NoopAuthenticator struct{}

func (NoopAuthenticator) Authenticate(ctx context.Context) error {
	_ = ctx
	return nil
}
