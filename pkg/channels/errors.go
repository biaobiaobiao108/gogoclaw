package channels

import "errors"

var (
	// ErrNotRunning indicates the channel is not running.
	// Manager will not retry.
	ErrNotRunning = errors.New("channel not running")

	// ErrRateLimit indicates the platform returned a rate-limit response (e.g. HTTP 429).
	// Manager will wait a fixed delay and retry.
	ErrRateLimit = errors.New("rate limited")

	// ErrTemporary indicates a failure that is safe to retry because the channel
	// supplied a stable idempotency key or knows the platform did not accept the
	// request.
	ErrTemporary = errors.New("temporary failure")

	// ErrDeliveryUnknown indicates the request may have reached the platform but
	// no conclusive response was received, or only part of a multi-part delivery
	// completed. Retrying can create duplicates, so Manager will not retry it.
	ErrDeliveryUnknown = errors.New("delivery state unknown")

	// ErrSendFailed indicates a permanent failure (e.g. invalid chat ID, 4xx non-429).
	// Manager will not retry.
	ErrSendFailed = errors.New("send failed")
)
