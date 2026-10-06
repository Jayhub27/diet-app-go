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

// LocalChatStreamFunc streams tokens from the local model. Set it at startup
// alongside LocalChatFunc.
var LocalChatStreamFunc func(ctx context.Context, req ChatRequest, onDelta func(StreamChunk)) error

// SetLocalChatFunc wires the local source to a real implementation.
func SetLocalChatFunc(f func(ctx context.Context, req ChatRequest) (*ChatResponse, error)) {
	LocalChatFunc = f
}

// SetLocalChatStreamFunc wires the local streaming source to a real impl.
func SetLocalChatStreamFunc(f func(ctx context.Context, req ChatRequest, onDelta func(StreamChunk)) error) {
	LocalChatStreamFunc = f
}

func initLocal(_ string) error { return errLocalUnavailable }

func localChat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if LocalChatFunc == nil {
		return nil, errLocalUnavailable
	}
	return LocalChatFunc(ctx, req)
}

func localChatStream(ctx context.Context, req ChatRequest, onDelta func(StreamChunk)) error {
	if LocalChatStreamFunc == nil {
		if LocalChatFunc == nil {
			onDelta(StreamChunk{Error: errLocalUnavailable.Error()})
			return errLocalUnavailable
		}
		// Fall back to non-streaming if no stream hook registered.
		res, err := LocalChatFunc(ctx, req)
		if err != nil {
			onDelta(StreamChunk{Error: err.Error()})
			return err
		}
		if len(res.Choices) > 0 {
			onDelta(StreamChunk{Delta: res.Choices[0].Message.Content})
		}
		onDelta(StreamChunk{Done: true})
		return nil
	}
	return LocalChatStreamFunc(ctx, req, onDelta)
}

var errLocalUnavailable = errors.New("local chat func not registered; call provider.SetLocalChatFunc at startup")
