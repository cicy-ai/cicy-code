package main

import (
	"log"
	"net/http"
	"os"

	"github.com/cicy-ai/cicy-skills/internal/api"
	"github.com/cicy-ai/cicy-skills/internal/config"
)

func main() {
	configPath := os.Getenv("CICY_SKILLS_CONFIG")
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	log.Printf("cicy-skillsd listening on %s", cfg.Listen)
	if err := http.ListenAndServe(cfg.Listen, api.NewServer(cfg)); err != nil {
		log.Fatal(err)
	}
}
