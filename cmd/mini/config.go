package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Host         string `yaml:"host"`
	SSHUser      string `yaml:"ssh_user"`
	OllamaPort   int    `yaml:"ollama_port"`
	LlamaCPPPort int    `yaml:"llamacpp_port"`
	DefaultModel string `yaml:"default_model"`
}

func (c *Config) OllamaURL() string {
	return fmt.Sprintf("http://%s:%d", c.Host, c.OllamaPort)
}

func (c *Config) LlamaCPPURL() string {
	return fmt.Sprintf("http://%s:%d", c.Host, c.LlamaCPPPort)
}

// LoadConfig loads config from ./config.yaml cli: section, then ~/.mini/config.yaml.
// Errors out if required fields are missing.
func LoadConfig() Config {
	var c Config

	// Layer 1: ./config.yaml cli: section
	loadProjectConfig(&c)

	// Layer 2: ~/.mini/config.yaml (flat)
	loadUserConfig(&c)

	// Validate required fields
	var missing []string
	if c.Host == "" {
		missing = append(missing, "host")
	}
	if c.SSHUser == "" {
		missing = append(missing, "ssh_user")
	}
	if c.OllamaPort == 0 {
		missing = append(missing, "ollama_port")
	}
	if c.DefaultModel == "" {
		missing = append(missing, "default_model")
	}
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "missing config: %v\n", missing)
		fmt.Fprintf(os.Stderr, "set in ./config.yaml (cli: section) or ~/.mini/config.yaml\n")
		os.Exit(1)
	}

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
