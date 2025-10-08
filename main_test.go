package main

import (
	"testing"
)

func TestLoadConfig(t *testing.T) {
	config := loadConfig()

	// Test that config loads without panicking
	if config.Token != "" && config.Repository == "" {
		t.Error("Expected repository to be set when token is set")
	}
}

func TestGetEnv(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		defaultValue string
		want         string
	}{
		{
			name:         "returns default when env not set",
			key:          "NONEXISTENT_KEY",
			defaultValue: "default",
			want:         "default",
		},
		{
			name:         "returns empty default",
			key:          "NONEXISTENT_KEY",
			defaultValue: "",
			want:         "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getEnv(tt.key, tt.defaultValue)
			if got != tt.want {
				t.Errorf("getEnv() = %v, want %v", got, tt.want)
			}
		})
	}
}
