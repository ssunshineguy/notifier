package config_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ssunshineguy/notifier/notifier/internal/config"
)

func TestHTTPBudgetDefaultsMatchShippedConfig(t *testing.T) {
	defaults, err := config.Load(writeConfig(t, validYAML), "api", "")
	if err != nil {
		t.Fatal(err)
	}
	shipped, err := config.Load("../../configs/config.yaml", "api", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, api := range []config.API{defaults.API, shipped.API} {
		if api.RequestTimeoutMS != 300 || api.WriteTimeoutMS != 1000 || api.IdleTimeoutMS != 60000 {
			t.Fatalf("HTTP budgets = %+v, want request=300ms write=1000ms idle=60000ms", api)
		}
	}
}

func TestLoadHTTPBudgets(t *testing.T) {
	for _, key := range []string{"request_timeout_ms", "write_timeout_ms", "idle_timeout_ms"} {
		for _, value := range []int{-1, 0, 75, 2147483647} {
			t.Run(fmt.Sprintf("%s/%d", key, value), func(t *testing.T) {
				cfg, err := config.Load(writeConfig(t, validYAML+fmt.Sprintf("api:\n  %s: %d\n", key, value)), "api", "")
				if value <= 0 {
					if err == nil || !strings.Contains(err.Error(), "api."+key+" must be positive") {
						t.Fatalf("Load = %v, want positive %s validation", err, key)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				got := map[string]int32{
					"request_timeout_ms": cfg.API.RequestTimeoutMS,
					"write_timeout_ms":   cfg.API.WriteTimeoutMS,
					"idle_timeout_ms":    cfg.API.IdleTimeoutMS,
				}[key]
				if int(got) != value {
					t.Fatalf("%s = %d, want %d", key, got, value)
				}
			})
		}
	}
}
