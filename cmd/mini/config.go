package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Host         string `yaml:"host"`
	SSHUser      string `yaml:"ssh_user"`
	OllamaPort   int    `yaml:"ollama_port"`
	LlamaCPPPort int    `yaml:"llamacpp_port"`
	DefaultModel string `yaml:"default_model"`
}

var defaultConfig = Config{
	Host:         "mac-mini.local",
	SSHUser:      currentUser(),
	OllamaPort:   11434,
	LlamaCPPPort: 8081,
	DefaultModel: "qwen2.5-coder:32b",
}

func (c *Config) OllamaURL() string {
	return fmt.Sprintf("http://%s:%d", c.Host, c.OllamaPort)
}

func (c *Config) LlamaCPPURL() string {
	return fmt.Sprintf("http://%s:%d", c.Host, c.LlamaCPPPort)
}

// LoadConfig loads config with priority: env > ~/.mini/config.yaml > ./config.yaml cli: section > defaults
func LoadConfig() Config {
	c := defaultConfig

	// Layer 1: ./config.yaml cli: section
	loadProjectConfig(&c)

	// Layer 2: ~/.mini/config.yaml (flat)
	loadUserConfig(&c)

	// Layer 3: env vars (highest priority)
	loadEnv(&c)

	return c
}

func loadProjectConfig(c *Config) {
	data, err := os.ReadFile("config.yaml")
	if err != nil {
		return
	}

	var root struct {
		CLI Config `yaml:"cli"`
	}
	if err := yaml.Unmarshal(data, &root); err != nil {
		return
	}

	merge(c, &root.CLI)
}

func loadUserConfig(c *Config) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	data, err := os.ReadFile(filepath.Join(home, ".mini", "config.yaml"))
	if err != nil {
		return
	}

	var user Config
	if err := yaml.Unmarshal(data, &user); err != nil {
		return
	}

	merge(c, &user)
}

func loadEnv(c *Config) {
	if v := os.Getenv("MINI_HOST"); v != "" {
		c.Host = v
	}
	if v := os.Getenv("MINI_SSH_USER"); v != "" {
		c.SSHUser = v
	}
	if v := os.Getenv("MINI_OLLAMA_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			c.OllamaPort = p
		}
	}
	if v := os.Getenv("MINI_LLAMACPP_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			c.LlamaCPPPort = p
		}
	}
	if v := os.Getenv("MINI_DEFAULT_MODEL"); v != "" {
		c.DefaultModel = v
	}
}

func currentUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "user"
}

// merge overwrites c with non-zero values from src
func merge(c *Config, src *Config) {
	if src.Host != "" {
		c.Host = src.Host
	}
	if src.SSHUser != "" {
		c.SSHUser = src.SSHUser
	}
	if src.OllamaPort != 0 {
		c.OllamaPort = src.OllamaPort
	}
	if src.LlamaCPPPort != 0 {
		c.LlamaCPPPort = src.LlamaCPPPort
	}
	if src.DefaultModel != "" {
		c.DefaultModel = src.DefaultModel
	}
}
