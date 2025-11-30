package main

import (
	"fmt"
	"log"
	"net/http"

	"gitGost/internal/config"
	handler "gitGost/internal/http"
	"gitGost/internal/utils"

	"github.com/joho/godotenv"
)

func main() {
	// Load .env file (ignore error if file doesn't exist)
	_ = godotenv.Load()

	// Load configuration
	cfg := config.Load()

	// Initialize logger
	utils.InitLogger(cfg)

	// Setup router
	router := handler.SetupRouter(cfg)

	// Start server
	addr := fmt.Sprintf(":%s", cfg.Port)
	utils.Log("Starting server on %s", addr)
	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatal(err)
	}
}
