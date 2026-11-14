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

	"precip-calendar/api/internal/httpapi"
	"precip-calendar/api/internal/provider"
	"precip-calendar/api/internal/store"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	ttl, err := time.ParseDuration(env("HOT_TTL", "30m"))
	if err != nil {
		log.Fatalf("HOT_TTL: %v", err)
	}
	fieldTTL, err := time.ParseDuration(env("FIELD_TTL", "3h"))
	if err != nil {
		log.Fatalf("FIELD_TTL: %v", err)
	}
	srv := &httpapi.Server{
		Source:  provider.NewOpenMeteo(),
		Hot:     store.NewHot(ttl),
		Fields:  store.NewHot(fieldTTL),
		Archive: store.Disk{Dir: env("ARCHIVE_DIR", "data/archive")},
		WebDir:  os.Getenv("WEB_DIR"),
		Now:     time.Now,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go srv.Janitor(ctx, time.Minute)

	httpSrv := &http.Server{Addr: env("ADDR", ":8787"), Handler: srv.Router()}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdown)
	}()
	log.Printf("listening on %s (hot ttl %s)", httpSrv.Addr, ttl)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
