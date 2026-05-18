package main

import (
	"fmt"
	"os"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Install InstallConfig           `toml:"install"`
	Remotes map[string]RemoteConfig `toml:"remotes"`
}

type InstallConfig struct {
	Dir     string `toml:"dir"`
	Created string `toml:"created"`
	Version string `toml:"version"`
}

type RemoteConfig struct {
	Enabled      bool   `toml:"enabled"`
	RcloneRemote string `toml:"rclone_remote,omitempty"`
	Bucket       string `toml:"bucket,omitempty"`
}

func loadConfig(path string) (*Config, error) {
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("reading vgit.toml: %w", err)
	}
	return &cfg, nil
}

func saveConfig(path string, cfg *Config) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("creating config file: %w", err)
	}
	if err := toml.NewEncoder(f).Encode(cfg); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("encoding config: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("closing config file: %w", err)
	}
	return os.Rename(tmp, path)
}

func newConfig(dir string) *Config {
	return &Config{
		Install: InstallConfig{
			Dir:     dir,
			Created: time.Now().UTC().Format(time.RFC3339),
			Version: Version,
		},
		Remotes: map[string]RemoteConfig{},
	}
}
