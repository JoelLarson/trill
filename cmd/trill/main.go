package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"sync"

	"trill/internal/codex"
	"trill/internal/config"
	"trill/internal/obs"
	"trill/internal/server"
	"trill/internal/service"
	"trill/internal/store"
)

//go:embed ui/* obsui/*
var uiFS embed.FS

func main() {
	cfg := config.Load()

	store := store.NewMemoryStore()
	model := codex.NewCLIClient()
	broker := obs.NewBroker()
	prompts, err := service.LoadPrompts("prompts")
	if err != nil {
		log.Fatalf("failed to load prompts: %v", err)
	}
	svc := service.New(store, model, broker)
	svc.Prompts = prompts
	srv := server.New(svc)

	mux := http.NewServeMux()
	srv.RegisterMux(mux)

	uiSub, err := fs.Sub(uiFS, "ui")
	if err != nil {
		log.Fatalf("embed fs error: %v", err)
	}
	uiHandler := http.FileServer(http.FS(uiSub))
	mux.Handle("/", uiHandler)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Printf("Agent manager listening on %s\n", cfg.Port)
		log.Fatal(http.ListenAndServe(cfg.Port, mux))
	}()

	obsMux := http.NewServeMux()
	obsMux.Handle("/events", http.HandlerFunc(broker.SSEHandler))
	obsSub, err := fs.Sub(uiFS, "obsui")
	if err != nil {
		log.Fatalf("embed obs fs error: %v", err)
	}
	obsMux.Handle("/", http.FileServer(http.FS(obsSub)))
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Printf("Observability listening on %s\n", cfg.ObsPort)
		log.Fatal(http.ListenAndServe(cfg.ObsPort, obsMux))
	}()
	wg.Wait()
}
