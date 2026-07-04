package main

import (
	"log"
	"net/http"
	"os"

	"github.com/k-kanke/reaction-engine/backend/internal/gateway"
	"github.com/k-kanke/reaction-engine/backend/internal/redis"
)

func main() {
	port := os.Getenv("GATEWAY_PORT")
	if port == "" {
		port = "8080"
	}

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	redisClient := redis.NewClient(redisAddr)
	defer redisClient.Close()

	handler := gateway.NewHandler(redisClient)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/ws", handler.ServeWS)

	log.Printf("gateway listening on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}
