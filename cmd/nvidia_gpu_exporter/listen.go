package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"github.com/prometheus/exporter-toolkit/web"
)

func listenTCP(
	ctx context.Context,
	server *http.Server,
	flags *web.FlagConfig,
	network string,
	logger *slog.Logger,
) (retErr error) {
	listeners := make([]net.Listener, 0, len(*flags.WebListenAddresses))

	for _, address := range *flags.WebListenAddresses {
		var lc net.ListenConfig

		listener, err := lc.Listen(ctx, network, address)
		if err != nil {
			return fmt.Errorf("failed to listen on %s: %w", address, err)
		}

		listeners = append(listeners, listener)
	}

	defer func() {
		for _, listener := range listeners {
			if err := listener.Close(); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("failed to close listener: %w", err))
			}
		}
	}()

	if err := web.ServeMultiple(listeners, server, flags, logger); err != nil {
		return fmt.Errorf("failed to serve: %w", err)
	}

	return nil
}
