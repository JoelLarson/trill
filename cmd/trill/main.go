package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"

	"trill/internal/codex"
	"trill/internal/config"
	"trill/internal/server"
	"trill/internal/service"
	"trill/internal/store"
)

//go:embed ui/*
var uiFS embed.FS

func main() {
	cfg := config.Load()

	store := store.NewMemoryStore()
	model := codex.NewCLIClient()
	svc := service.New(store, model)
	srv := server.New(svc)

	mux := http.NewServeMux()
	srv.RegisterMux(mux)

	uiSub, err := fs.Sub(uiFS, "ui")
	if err != nil {
		log.Fatalf("embed fs error: %v", err)
	}
	uiHandler := http.FileServer(http.FS(uiSub))
	mux.Handle("/", uiHandler)

	log.Printf("Agent manager listening on %s\n", cfg.Port)
	log.Fatal(http.ListenAndServe(cfg.Port, mux))
}
