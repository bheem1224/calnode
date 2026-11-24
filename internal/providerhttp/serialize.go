package providerhttp

import (
	"context"
	"sync"
)

var operations = struct {
	sync.Mutex
	entries map[string]*operation
}{entries: map[string]*operation{}}

type operation struct {
	lock chan struct{}
	refs int
}

// Serialize keeps a read-before-write check and its notification-producing write
// together, including when inline delivery overlaps the background reconciler.
func Serialize(ctx context.Context, key string, fn func() error) error {
	operations.Lock()
	entry := operations.entries[key]
	if entry == nil {
		entry = &operation{lock: make(chan struct{}, 1)}
		operations.entries[key] = entry
	}
	entry.refs++
	operations.Unlock()
	defer func() {
		operations.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(operations.entries, key)
		}
		operations.Unlock()
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case entry.lock <- struct{}{}:
	}
	defer func() { <-entry.lock }()
	return fn()
}
