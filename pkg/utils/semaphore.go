package utils

import (
	"context"
	"fmt"
	"time"
)

// Semaphore
type Semaphore func() (bool, func())

// CreateSemaphore creates a new semaphore to limit concurrency
func CreateSemaphore(concurrency int, wait time.Duration) Semaphore {
	var lock = make(chan bool, concurrency)

	return func() (bool, func()) {
		timer := time.NewTimer(wait)
		defer timer.Stop()

		for {
			select {
			case <-timer.C:
				return false, nil
			case lock <- true:
				return true, func() {
					<-lock
				}
			}
		}
	}
}

// WaitForSemaphore waits until context cancel
func WaitForSemaphore(ctx context.Context, sem Semaphore) (func(), error) {
	var unlock func()
LOCK:
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context deadline")
		default:
			var lock bool
			lock, unlock = sem()
			if lock {
				break LOCK
			}
		}
	}

	return func() {
		unlock()
	}, nil
}
