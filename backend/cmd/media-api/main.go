package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/k-kanke/reaction-engine/backend/internal/db"
	"github.com/k-kanke/reaction-engine/backend/internal/media"
)

func main() {
	port := os.Getenv("MEDIA_API_PORT")
	if port == "" {
		port = "8081"
	}

	publicBaseURL := os.Getenv("MEDIA_API_PUBLIC_BASE_URL")
	if publicBaseURL == "" {
		publicBaseURL = "http://localhost:" + port
	}

	localMediaDir := os.Getenv("LOCAL_MEDIA_DIR")
	if localMediaDir == "" {
		localMediaDir = "./tmp/media"
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://reaction:reaction@localhost:5432/reaction?sslmode=disable"
	}

	signedURLTTL := 900 * time.Second
	if raw := os.Getenv("SIGNED_URL_TTL_SECONDS"); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil {
			signedURLTTL = time.Duration(seconds) * time.Second
		} else {
			log.Printf("media-api: invalid SIGNED_URL_TTL_SECONDS %q, using default", raw)
		}
	}

	ctx := context.Background()
	pool, err := db.NewPool(ctx, databaseURL)
	if err != nil {
		log.Fatalf("media-api: failed to connect to postgres: %v", err)
	}
	defer pool.Close()

	store := media.NewPGStore(pool)
	events := db.NewLocalEventStore(pool)
	handler := media.NewHandler(store, events, localMediaDir, publicBaseURL, signedURLTTL)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	handler.RegisterRoutes(mux)

	log.Printf("media-api listening on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}
