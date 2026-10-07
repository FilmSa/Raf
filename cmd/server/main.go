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

	"accesscontrol/internal/app"
)

func main() {
	cfg, err := app.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}
	store, err := app.OpenStore(cfg.Database)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	server := &http.Server{Addr: cfg.Addr, Handler: app.New(cfg, store).Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 25 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	if os.Getenv("ADMIN_PASSWORD") == "" {
		log.Printf("Generated administrator password: %s", cfg.AdminPassword)
	}
	log.Printf("Access Control: http://%s", cfg.Addr)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
