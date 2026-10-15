package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/extra/bunotel"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"bfiber/internal/config"
	"bfiber/internal/database"
)

// TestAddQueryHooks_MountsBunotel 验证 AddQueryHooks 正确挂载官方 bunotel 钩子。
func TestAddQueryHooks_MountsBunotel(t *testing.T) {
	db := bun.NewDB(nil, pgdialect.New())

	cfg := config.DatabaseConfig{
		URL:           "postgres://postgres:secret@localhost:5432/bfiber_test?sslmode=disable",
		SlowThreshold: 200 * time.Millisecond,
		Debug:         true,
	}

	wrapped := database.AddQueryHooks(db, cfg)
	require.NotNil(t, wrapped)
	assert.Nil(t, database.AddQueryHooks(nil, cfg))
}

// TestBunotelQueryHook_ActiveSpan 验证官方 bunotel 钩子正确向链路注入 Span 与属性。
func TestBunotelQueryHook_ActiveSpan(t *testing.T) {
	spanRecorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	otel.SetTracerProvider(tp)
	defer func() { _ = tp.Shutdown(context.Background()) }()

	hook := bunotel.NewQueryHook(
		bunotel.WithDBName("bfiber_test"),
		bunotel.WithFormattedQueries(true),
	)

	tr := tp.Tracer("test")
	rootCtx, rootSpan := tr.Start(context.Background(), "root-operation")

	db := bun.NewDB(nil, pgdialect.New())
	event := &bun.QueryEvent{
		DB:            db,
		StartTime:     time.Now(),
		QueryTemplate: "SELECT * FROM users WHERE id = ?",
		Query:         "SELECT * FROM users WHERE id = 1",
	}

	queryCtx := hook.BeforeQuery(rootCtx, event)
	assert.NotEqual(t, rootCtx, queryCtx)

	hook.AfterQuery(queryCtx, event)
	rootSpan.End()

	spans := spanRecorder.Ended()
	require.Len(t, spans, 2)

	dbSpan := spans[0]
	assert.NotEmpty(t, dbSpan.Name())

	var hasDBName bool
	for _, attr := range dbSpan.Attributes() {
		if attr.Key == "db.name" && attr.Value.AsString() == "bfiber_test" {
			hasDBName = true
		}
	}
	assert.True(t, hasDBName, "应包含 db.name=bfiber_test 属性")
}
