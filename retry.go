package goup

import (
	"context"
	"fmt"
	"time"

	"github.com/vthiery/retry"
)

func retryJob(f func() error) {
	// Define the retry strategy, with 10 attempts and an exponential backoff
	r := retry.New(
		retry.WithMaxAttempts(10),
		retry.WithBackoff(
			retry.NewExponentialBackoff(
				100*time.Millisecond, // minWait
				1*time.Hour,          // maxWait
				20*time.Millisecond,  // maxJitter
			),
		),
	)

	// A cancellable context can be used to stop earlier
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Define the function that can be retried
	fn := func(context.Context) error {
		return f()
	}

	// Call the `retry.Do` to attempt to perform `fn`
	if err := r.Do(ctx, fn); err != nil {
		fmt.Printf("failed to perform `fn`: %v\n", err)
	}
}
