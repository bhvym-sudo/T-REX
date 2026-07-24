package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"trex/backend/internal/app"
	"trex/backend/internal/server"
)

func main() {
	paths := app.ResolvePaths()
	if err := paths.Ensure(); err != nil {
		log.Fatal(err)
	}
	if err := server.EnsureConfig(paths); err != nil {
		log.Fatal(err)
	}
	instance, err := server.New(paths)
	if err != nil {
		log.Fatal(err)
	}
	errs := make(chan error, 1)
	go func() { errs <- instance.ListenAndServe() }()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-errs:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	case <-signals:
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_ = instance.Shutdown(ctx)
}
