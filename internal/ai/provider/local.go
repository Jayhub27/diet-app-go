package provider

import (
	"context"
	"errors"
)

// LocalChatFunc is the hook used by the local provider to dispatch chat
// requests. Set it once at startup to a function that calls the local model
// manager. Kept as a function variable (not an interface field on Client)
// to keep the existing provider.New() signature unchanged.
var LocalChatFunc func(ctx context.Context, req ChatRequest) (*ChatResponse, error)

// SetLocalChatFunc wires the local source to a real implementation.
func SetLocalChatFunc(f func(ctx context.Context, req ChatRequest) (*ChatResponse, error)) {
	LocalChatFunc = f
}

func initLocal(_ string) error { return errLocalUnavailable }

func localChat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if LocalChatFunc == nil {
		return nil, errLocalUnavailable
	}
	return LocalChatFunc(ctx, req)
}

var errLocalUnavailable = errors.New("local chat func not registered; call provider.SetLocalChatFunc at startup")
