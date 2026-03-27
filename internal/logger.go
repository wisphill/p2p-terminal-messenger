package internal

import (
	"context"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

var baseLogger *zap.Logger

func Init() error {
	l, err := zap.NewProduction()
	if err != nil {
		return err
	}
	baseLogger = l
	return nil
}

func Sync() error {
	if baseLogger != nil {
		return baseLogger.Sync()
	}
	return nil
}

func TraceIDFromContext(ctx context.Context) string {
	span := trace.SpanContextFromContext(ctx)
	if span.HasTraceID() {
		return span.TraceID().String()
	}
	return ""
}

func LoggerFromContext(ctx context.Context) *zap.SugaredLogger {
	traceID := TraceIDFromContext(ctx)

	if traceID == "" {
		return baseLogger.Sugar()
	}

	return baseLogger.
		With(zap.String("trace_id", traceID)).
		Sugar()
}

func InforF(ctx context.Context, format string, args ...interface{}) {
	LoggerFromContext(ctx).Infof(format, args...)
}

func ErrorF(ctx context.Context, format string, args ...interface{}) {
	LoggerFromContext(ctx).Errorf(format, args...)
}
