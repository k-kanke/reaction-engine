package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	port := os.Getenv("IMAGE_ANALYSIS_WORKER_DEBUG_PORT")
	if port == "" {
		port = "8082"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	log.Printf("image-analysis-worker started, debug endpoint on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}
