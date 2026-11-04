package bot

import (
	"errors"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"tg-podcastotron/telemetry"
)

// telegramPollTimeout is the long polling timeout of getUpdates. It is the default
// of the library, and the HTTP client waits as long.
const telegramPollTimeout = time.Minute

// telegramHTTPClient is the HTTP client of the bot. It makes a span for each Bot
// API call, and it removes the bot token from errors.
type telegramHTTPClient struct {
	client *http.Client
}

func newTelegramHTTPClient() *telegramHTTPClient {
	return &telegramHTTPClient{client: &http.Client{
		Timeout:   telegramPollTimeout,
		Transport: tracingTransport{base: http.DefaultTransport},
	}}
}

// Do sends the request. The URL of a Bot API call has the bot token in its path,
// and http.Client puts the URL into its errors. Thus the token went into the logs
// when a request failed. Do replaces it with "<token>".
func (c *telegramHTTPClient) Do(req *http.Request) (*http.Response, error) {
	resp, err := c.client.Do(req)
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		urlErr.URL = redactToken(urlErr.URL)
	}
	return resp, err
}

var tokenInPath = regexp.MustCompile(`/bot[^/]+/`)

func redactToken(s string) string {
	return tokenInPath.ReplaceAllString(s, "/bot<token>/")
}

// tracingTransport makes a span for each Bot API call, like otelhttp does for S3
// and mediary. The span has only the name of the method, not the URL, because the
// URL has the bot token. getUpdates (long polling) gets no span: it runs each
// minute also when nothing happens.
type tracingTransport struct {
	base http.RoundTripper
}

func (t tracingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	method := path.Base(req.URL.Path)
	if method == "getUpdates" {
		return t.base.RoundTrip(req)
	}
	ctx, span := telemetry.StartSpan(req.Context(), "telegram."+method, attribute.String("telegram.method", method))
	defer span.End()

	resp, err := t.base.RoundTrip(req.WithContext(ctx))
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err // without the URL
		}
		telemetry.RecordError(span, err)
		return nil, err
	}
	span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))
	if resp.StatusCode >= 400 {
		span.SetStatus(codes.Error, http.StatusText(resp.StatusCode))
	}
	return resp, nil
}
