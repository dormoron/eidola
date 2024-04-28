package errs

import (
	"errors"
	"fmt"
)

var (
	// server error
	errServerListening = errors.New("failed to start listening")
	errSeverRegister   = errors.New("failed to registry service")
	errFailedServe     = errors.New("failed to serve")
	errServerClose     = errors.New("failed to close registry")
	// client error
	errClientCreateRegistry = errors.New("failed to create registry builder")
	errClientDial           = errors.New("failed to dial")
	// cluster error
	errClusterNoResponse = errors.New("no one received the response")
	// ratelimit
	errRateLimitClose = errors.New("limiter is closed")
)

func ErrServerListening(err error) error {
	return fmt.Errorf("%w: %w", errServerListening, err)
}

func ErrServerRegister(err error) error {
	return fmt.Errorf("%w: %w", errSeverRegister, err)
}

func ErrFailedServe(err error) error {
	return fmt.Errorf("%w: %w", errFailedServe, err)
}

func ErrServerClose(err error) error {
	return fmt.Errorf("%w: %w", errServerClose, err)
}

func ErrClientCreateRegistry(err error) error {
	return fmt.Errorf("%w: %w", errClientCreateRegistry, err)
}

func ErrClientDial(target string, err error) error {
	return fmt.Errorf("%w %q: %w", errClientDial, target, err)
}

func ErrClusterNoResponse(err error) error {
	return fmt.Errorf("%w, %w", errClusterNoResponse, err)
}

func ErrRateLimitClose() error {
	return errRateLimitClose
}
