package config_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ssunshineguy/notifier/notifier/internal/config"
)

func TestLoadMaxConnections(t *testing.T) {
	for _, setting := range []string{"", "null", "1", "64", "1024", "0", "-1"} {
		t.Run("value="+setting, func(t *testing.T) {
			data := validYAML
			if setting != "" {
				data += "api:\n  max_connections: " + setting + "\n"
			}
			cfg, err := config.Load(writeConfig(t, data), "api", "")
			if setting == "0" || setting == "-1" {
				if err == nil || !strings.Contains(err.Error(), "api.max_connections must be positive") {
					t.Fatalf("Load = %v, want positive max_connections validation", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if setting == "" || setting == "null" {
				shipped, err := config.Load("../../configs/config.yaml", "api", "")
				if err != nil {
					t.Fatal(err)
				}
				if cfg.API.MaxConnections <= 0 || cfg.API.MaxConnections != shipped.API.MaxConnections {
					t.Fatalf("default max_connections=%d, shipped=%d", cfg.API.MaxConnections, shipped.API.MaxConnections)
				}
			} else if got := fmt.Sprint(cfg.API.MaxConnections); got != setting {
				t.Fatalf("max_connections=%s, want %s", got, setting)
			}
		})
	}
}
