//go:build integration

package postgres_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ssunshineguy/notifier/internal/core"
	"github.com/ssunshineguy/notifier/internal/logging"
	"github.com/ssunshineguy/notifier/internal/storage/postgres"
)

func TestSQLLogsRedactClientDetails(t *testing.T) {
	pool := startPostgres(t)
	store := postgres.NewStore(pool, 2*time.Second)
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil)).With("request_id", "trace-42")
	ctx := logging.WithLogger(t.Context(), logger)

	const clientID = "private-client"
	const callbackURL = "https://example.test/?token=private-token"
	if _, err := pool.Exec(ctx,
		"INSERT INTO notifier.t_client (client_id, callback_url, max_lifetime_sec) VALUES ($1, $2, 60)",
		clientID, callbackURL); err != nil {
		t.Fatal(err)
	}
	client, err := store.GetClient(ctx, clientID)
	if err != nil || client.CallbackURL != callbackURL {
		t.Fatalf("GetClient = %+v, %v", client, err)
	}
	if _, err := store.GetClient(ctx, "private-missing"); !errors.Is(err, core.ErrClientNotFound) {
		t.Fatalf("GetClient for missing ID = %v", err)
	}
	_, err = store.CreateNotification(ctx, core.Notification{
		ClientID: clientID, ExternalID: "private-external",
		Start: time.Now(), Deadline: time.Time{}, NextScheduled: time.Now(),
	})
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != "23514" {
		t.Fatalf("CreateNotification = %v, want a check violation", err)
	}
	pool.Close()
	if _, err := store.GetClient(ctx, clientID); err == nil {
		t.Fatal("expected a storage error")
	}

	for _, raw := range []string{"private-", "WHERE client_id", "INSERT INTO", "$1"} {
		if strings.Contains(output.String(), raw) {
			t.Fatalf("found %q in SQL logs: %s", raw, output.String())
		}
	}
	type entry struct {
		Message   string          `json:"msg"`
		Level     string          `json:"level"`
		QueryName string          `json:"query_name"`
		SQL       json.RawMessage `json:"sql"`
		Duration  *int64          `json:"duration_ms"`
		RequestID string          `json:"request_id"`
		Outcome   string          `json:"outcome"`
		SQLState  string          `json:"sqlstate"`
		RowCount  int             `json:"row_count"`
		Result    struct {
			Client struct {
				ID          string `json:"client_id"`
				CallbackURL string `json:"callback_url"`
				MaxLifetime int    `json:"max_lifetime_sec"`
			} `json:"client"`
		} `json:"result"`
	}
	wantQueries := []string{"select_client", "select_client", "insert_notification", "select_client"}
	decoder := json.NewDecoder(&output)
	entries := make([]entry, len(wantQueries)*2)
	for i := range entries {
		event := &entries[i]
		if err := decoder.Decode(event); err != nil {
			t.Fatal(err)
		}
		queryName := wantQueries[i/2]
		want := "Start SQL " + queryName
		if i%2 == 1 {
			if event.Duration == nil || *event.Duration < 0 {
				t.Fatalf("missing or invalid SQL duration: %+v", event)
			}
			want = fmt.Sprintf("End SQL %s duration %d ms", queryName, *event.Duration)
		}
		if event.Message != want || event.Level != "INFO" || event.RequestID != "trace-42" ||
			event.QueryName != queryName || event.SQL != nil {
			t.Errorf("unexpected log entry: %+v", event)
		}
	}
	if decoder.More() {
		t.Error("unexpected additional SQL log entry")
	}
	success := entries[1]
	if success.Outcome != "ok" || success.RowCount != 1 || success.Result.Client.ID != "[redacted]" ||
		success.Result.Client.CallbackURL != "[redacted]" || success.Result.Client.MaxLifetime != 60 {
		t.Errorf("unexpected SQL result: %+v", success)
	}
	if entries[3].Outcome != "no_rows" || entries[3].RowCount != 0 || entries[5].Outcome != "error" || entries[5].SQLState != "23514" || entries[7].Outcome != "error" {
		t.Errorf("unexpected empty and error results: %+v, %+v, %+v", entries[3], entries[5], entries[7])
	}
}

func TestCheckConnectionLogLevels(t *testing.T) {
	pool := startPostgres(t)
	store := postgres.NewStore(pool, 2*time.Second)
	var logs bytes.Buffer
	var level slog.LevelVar
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: &level})).With("request_id", "health-42")
	ctx := logging.WithLogger(t.Context(), logger)

	if err := store.CheckConnection(ctx); err != nil {
		t.Fatal(err)
	}
	if logs.Len() != 0 {
		t.Fatalf("success at INFO emitted logs: %s", &logs)
	}

	type entry struct {
		Message   string          `json:"msg"`
		Level     string          `json:"level"`
		QueryName string          `json:"query_name"`
		SQL       json.RawMessage `json:"sql"`
		Duration  *int64          `json:"duration_ms"`
		RequestID string          `json:"request_id"`
		Outcome   string          `json:"outcome"`
		Result    map[string]int  `json:"result"`
	}
	assertEntries := func(want ...entry) {
		t.Helper()
		decoder := json.NewDecoder(&logs)
		for _, expected := range want {
			var actual entry
			if err := decoder.Decode(&actual); err != nil {
				t.Fatal(err)
			}
			wantMessage := "Start SQL check_connection"
			if expected.Outcome != "" {
				if actual.Duration == nil || *actual.Duration < 0 {
					t.Fatalf("missing or invalid SQL duration: %+v", actual)
				}
				wantMessage = fmt.Sprintf("End SQL check_connection duration %d ms", *actual.Duration)
			}
			if actual.Message != wantMessage || actual.Level != expected.Level ||
				actual.Outcome != expected.Outcome || actual.Result["value"] != expected.Result["value"] ||
				actual.QueryName != "check_connection" || actual.SQL != nil || actual.RequestID != "health-42" {
				t.Errorf("unexpected log entry: %+v", actual)
			}
		}
		if decoder.More() {
			t.Error("unexpected additional SQL log entry")
		}
		logs.Reset()
	}

	level.Set(slog.LevelDebug)
	if err := store.CheckConnection(ctx); err != nil {
		t.Fatal(err)
	}
	assertEntries(entry{Level: "DEBUG"},
		entry{Level: "DEBUG", Outcome: "ok", Result: map[string]int{"value": 1}})

	level.Set(slog.LevelInfo)
	pool.Close()
	if err := store.CheckConnection(ctx); err == nil {
		t.Fatal("expected a storage error")
	}
	assertEntries(entry{Level: "INFO", Outcome: "error"})
}
