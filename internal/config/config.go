package config

import (
	"os"
	"path/filepath"
)

func GetConfigDir() (string, error) {
	var configDir string

	if homeDir, err := os.UserHomeDir(); err == nil {
		if _, err := os.Stat(filepath.Join(homeDir, "Library/Application Support")); err == nil {
			configDir = filepath.Join(homeDir, "Library/Application Support", "clmail")
		} else {
			configDir = filepath.Join(homeDir, ".config", "clmail")
		}
	} else {
		return "", err
	}

	if err := os.MkdirAll(configDir, 0700); err != nil {
		return "", err
	}

	return configDir, nil
}
