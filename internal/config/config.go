package config

import (
	"flag"
	"os"
)

type Config struct {
	Port string
}

func Load() Config {
	port := envDefault("PORT", ":8080")
	flag.StringVar(&port, "port", port, "HTTP listen address")
	flag.Parse()
	return Config{Port: port}
}

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
