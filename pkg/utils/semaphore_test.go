package utils

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateSemaphoreSerial(t *testing.T) {
	wg := sync.WaitGroup{}
	wg.Add(1)

	sem := CreateSemaphore(1, time.Second)

	go func() {
		defer wg.Done()

		lock, unlock := sem()

		require.NotNil(t, unlock, "unlock not received")
		assert.True(t, lock, "unable to grab lock")

		time.Sleep(2 * time.Second)
		unlock()
	}()

	time.Sleep(time.Millisecond)

	lock, unlock := sem()

	require.Nil(t, unlock, "unlock received")
	assert.False(t, lock, "able to grab lock")

	wg.Wait()
}

func TestCreateSemaphoreParallel(t *testing.T) {
	wg := sync.WaitGroup{}

	sem := CreateSemaphore(2, time.Second)

	for i := 0; i < 2; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			lock, unlock := sem()

			require.NotNil(t, unlock, "unlock not received")
			assert.True(t, lock, "unable to grab lock")

			time.Sleep(2 * time.Second)
			unlock()
		}()
	}

	time.Sleep(time.Millisecond)

	lock, unlock := sem()

	require.Nil(t, unlock, "unlock received")
	assert.False(t, lock, "able to grab lock")

	wg.Wait()
}
