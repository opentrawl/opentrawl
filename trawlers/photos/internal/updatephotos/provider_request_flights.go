package updatephotos

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"

	"google.golang.org/protobuf/proto"
)

type providerRequestFlights struct {
	mutex  sync.Mutex
	active map[string]chan struct{}
}

func runProviderRequestFlight[T any](ctx context.Context, flights *providerRequestFlights, providerOperation string, providerRequest proto.Message, operation func() (T, error)) (T, error) {
	var zero T
	requestBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(providerRequest)
	if err != nil {
		return zero, err
	}
	requestDigest := sha256.Sum256(requestBytes)
	key := fmt.Sprintf("%s:%x", providerOperation, requestDigest)

	flights.mutex.Lock()
	if flights.active == nil {
		flights.active = make(map[string]chan struct{})
	}
	if completed, found := flights.active[key]; found {
		flights.mutex.Unlock()
		select {
		case <-completed:
			return operation()
		case <-ctx.Done():
			return zero, ctx.Err()
		}
	}
	completed := make(chan struct{})
	flights.active[key] = completed
	flights.mutex.Unlock()

	defer func() {
		flights.mutex.Lock()
		delete(flights.active, key)
		close(completed)
		flights.mutex.Unlock()
	}()
	return operation()
}
