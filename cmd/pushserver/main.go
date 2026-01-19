package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/wurp/ourcloud-fcm-push-gateway/internal/config"
	"github.com/wurp/ourcloud-fcm-push-gateway/internal/handler"
	"github.com/wurp/ourcloud-fcm-push-gateway/internal/ourcloud"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to configuration file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize OurCloud client
	ocClient := ourcloud.NewClient(cfg.OurCloud.GRPCAddress)
	if err := ocClient.Connect(); err != nil {
		log.Fatalf("Failed to connect to OurCloud node: %v", err)
	}
	defer ocClient.Close()

	log.Printf("Connected to OurCloud node at %s", cfg.OurCloud.GRPCAddress)

	// Initialize handlers
	pushHandler := handler.NewPushHandler(ocClient)

	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)

	// Routes
	r.Get("/health", makeHealthHandler(ocClient))
	r.Post("/push", pushHandler.HandlePush)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      r,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	// Start server in goroutine
	go func() {
		log.Printf("Starting server on port %d", cfg.Server.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server stopped")
}

// HealthResponse represents the JSON response from the health endpoint.
type HealthResponse struct {
	Status   string `json:"status"`
	OurCloud string `json:"ourcloud,omitempty"`
}

func makeHealthHandler(ocClient *ourcloud.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		resp := HealthResponse{
			Status:   "ok",
			OurCloud: "ok",
		}

		// Check OurCloud connectivity
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		if err := ocClient.HealthCheck(ctx); err != nil {
			resp.Status = "degraded"
			resp.OurCloud = fmt.Sprintf("error: %v", err)
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(resp)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}
}
