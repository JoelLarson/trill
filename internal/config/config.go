package config

import (
	"flag"
	"os"
)

type Config struct {
	Port    string
	ObsPort string
}

func Load() Config {
	port := envDefault("PORT", ":8080")
	obsPort := envDefault("OBS_PORT", ":8081")
	flag.StringVar(&port, "port", port, "HTTP listen address")
	flag.StringVar(&obsPort, "obs-port", obsPort, "Observability HTTP listen address")
	flag.Parse()
	return Config{Port: port, ObsPort: obsPort}
}

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
