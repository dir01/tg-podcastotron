package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "tg-podcastotron"

// StartSpan creates a new span with the given name and attributes.
func StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	tracer := otel.Tracer(tracerName)
	return tracer.Start(ctx, name, trace.WithAttributes(attrs...))
}

// RecordError records an error on the span and sets the span status to error.
func RecordError(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
}

// Attribute helper functions for common attributes

// AttrUserID creates an attribute for user ID.
func AttrUserID(userID string) attribute.KeyValue {
	return attribute.String("user.id", userID)
}

// AttrEpisodeID creates an attribute for episode ID.
func AttrEpisodeID(episodeID string) attribute.KeyValue {
	return attribute.String("episode.id", episodeID)
}

// AttrFeedID creates an attribute for feed ID.
func AttrFeedID(feedID string) attribute.KeyValue {
	return attribute.String("feed.id", feedID)
}

// AttrJobType creates an attribute for job type.
func AttrJobType(jobType string) attribute.KeyValue {
	return attribute.String("job.type", jobType)
}

// AttrURL creates an attribute for URL.
func AttrURL(url string) attribute.KeyValue {
	return attribute.String("url", url)
}

// AttrError creates an attribute for error message.
func AttrError(err error) attribute.KeyValue {
	if err != nil {
		return attribute.String("error", err.Error())
	}
	return attribute.String("error", "")
}

// AttrHTTPMethod creates an attribute for HTTP method.
func AttrHTTPMethod(method string) attribute.KeyValue {
	return attribute.String("http.method", method)
}

// AttrHTTPStatusCode creates an attribute for HTTP status code.
func AttrHTTPStatusCode(code int) attribute.KeyValue {
	return attribute.Int("http.status_code", code)
}
