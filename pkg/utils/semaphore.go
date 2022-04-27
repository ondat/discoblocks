package utils

import "time"

func CreateSemaphore(concurrency int, timeout time.Duration) func() (bool, func()) {
	var lock = make(chan bool, concurrency)

	return func() (bool, func()) {
		timer := time.NewTimer(timeout)
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
