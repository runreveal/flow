package batch

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/runreveal/flow"
)

func TestAckChu(t *testing.T) {
	var called bool
	callMe := ackFn(func() { called = true }, 2)
	for i := 0; i < 2; i++ {
		callMe()
	}
	assert.True(t, called, "ack should be called")

	nilMe := ackFn(nil, 2)
	for i := 0; i < 2; i++ {
		// shouldn't panic
		nilMe()
	}
}

// func flushTest[T any](c context.Context, msgs []flow.Message[T]) {
// 	for _, msg := range msgs {
// 		fmt.Println(msg.Value)
// 	}
// 	counter++
// }

func TestBatcher(t *testing.T) {

	var ff = func(c context.Context, msgs []flow.Message[string]) error {
		for _, msg := range msgs {
			fmt.Println(msg.Value)
		}
		return nil
	}

	bat := NewDestination[string](FlushFunc[string](ff), FlushLength(1))

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)

	errc := make(chan error)

	go func(c context.Context, ec chan error) {
		ec <- bat.Run(c)
	}(ctx, errc)

	writeMsgs := []flow.Message[string]{
		flow.Message[string]{Value: "hi"},
		flow.Message[string]{Value: "hello"},
		flow.Message[string]{Value: "bonjour"},
	}

	done := make(chan struct{})
	err := bat.Send(ctx, func() { close(done) }, writeMsgs...)
	assert.NoError(t, err)

	select {
	case err := <-errc:
		assert.NoError(t, err)
	case <-done:
	}
	cancel()

}

func TestBatchFlushTimeout(t *testing.T) {
	hMu := sync.Mutex{}
	handled := false

	var ff = func(c context.Context, msgs []flow.Message[string]) error {
		for _, msg := range msgs {
			fmt.Println(msg.Value)
		}
		hMu.Lock()
		handled = true
		hMu.Unlock()
		return nil
	}

	bat := NewDestination[string](FlushFunc[string](ff), FlushFrequency(1*time.Millisecond), FlushLength(2))

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)

	errc := make(chan error)

	go func(c context.Context, ec chan error) {
		ec <- bat.Run(c)
	}(ctx, errc)

	writeMsgs := []flow.Message[string]{
		flow.Message[string]{Value: "hi"},
		flow.Message[string]{Value: "hello"},
	}

	done := make(chan struct{})
	err := bat.Send(ctx, func() { close(done) }, writeMsgs[0])
	assert.NoError(t, err)
	time.Sleep(3 * time.Millisecond)

	hMu.Lock()
	assert.True(t, handled, "value should have been set!")
	hMu.Unlock()

	select {
	case err := <-errc:
		assert.NoError(t, err)
	case <-done:
	}
	cancel()

}

func TestBatcherErrors(t *testing.T) {
	flushErr := errors.New("flush error")
	var ff = func(c context.Context, msgs []flow.Message[string]) error {
		return flushErr
	}
	bat := NewDestination[string](FlushFunc[string](ff), FlushLength(1))
	errc := make(chan error)

	t.Run("flush errors return from run", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)

		go func(c context.Context, ec chan error) {
			ec <- bat.Run(c)
		}(ctx, errc)

		writeMsgs := []flow.Message[string]{
			flow.Message[string]{Value: "hi"},
		}

		done := make(chan struct{})
		err := bat.Send(ctx, func() { close(done) }, writeMsgs...)
		assert.NoError(t, err)

		select {
		case err := <-errc:
			assert.EqualError(t, err, "flush error")
		case <-done:
		}
		cancel()
	})

	t.Run("cancellation works", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)

		go func(c context.Context, ec chan error) {
			ec <- bat.Run(c)
		}(ctx, errc)

		cancel()
		select {
		case err := <-errc:
			assert.EqualError(t, err, "context canceled")
		}
	})

	t.Run("cancellation works in deadlock", func(t *testing.T) {

		var ff = func(c context.Context, msgs []flow.Message[string]) error {
			select {
			case <-c.Done():
				return c.Err()
			}
			return nil
		}
		bat := NewDestination[string](FlushFunc[string](ff), FlushLength(1))

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)

		errc := make(chan error)

		go func(c context.Context, ec chan error) {
			ec <- bat.Run(c)
		}(ctx, errc)

		writeMsgs := []flow.Message[string]{
			// will be blocked flushing
			flow.Message[string]{Value: "hi"},
			// will be stuck waiting for flush slot
			flow.Message[string]{Value: "hello"},
			// will be stuck waiting to write to msgs in Send
			flow.Message[string]{Value: "bonjour"},
		}

		done := make(chan struct{})
		err := bat.Send(ctx, func() { close(done) }, writeMsgs...)
		assert.NoError(t, err)

		cancel()
		select {
		case err := <-errc:
			assert.EqualError(t, err, "context canceled")
		}
	})
}
