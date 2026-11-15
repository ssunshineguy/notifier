package config_test

import (
	"strings"
	"testing"

	"github.com/ssunshineguy/notifier/notifier/internal/config"
)

func TestTechnicalRetryDelay(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		want        int32
		invalid     bool
	}{
		{name: "default", want: 10},
		{name: "independent configured delay", value: "23", want: 23},
		{name: "zero", value: "0", invalid: true},
		{name: "negative", value: "-1", invalid: true},
		{name: "overflow", value: "2147483648", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			yaml := validYAML
			if tc.value != "" {
				yaml = strings.Replace(yaml, "notification:\n", "notification:\n  technical_retry_delay_sec: "+tc.value+"\n", 1)
			}
			path := writeConfig(t, yaml)
			got, err := config.Load(path, "api", "")
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid delay accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Notification.TechnicalRetryDelaySec != tc.want {
				t.Fatalf("delay=%d want=%d", got.Notification.TechnicalRetryDelaySec, tc.want)
			}
		})
	}
}
