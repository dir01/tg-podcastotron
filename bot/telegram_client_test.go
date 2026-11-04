package bot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

const secretToken = "123456:SECRET-token"

func TestTelegramHTTPClient__Spans(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(previous) })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			w.WriteHeader(http.StatusTooManyRequests)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := newTelegramHTTPClient()
	for _, method := range []string{"getUpdates", "sendMessage", "getMe"} {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/bot"+secretToken+"/"+method, nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}

	spans := recorder.Ended()
	if len(spans) != 2 || spans[0].Name() != "telegram.sendMessage" || spans[1].Name() != "telegram.getMe" {
		names := []string{}
		for _, s := range spans {
			names = append(names, s.Name())
		}
		t.Fatalf("spans = %v, want telegram.sendMessage and telegram.getMe (no getUpdates)", names)
	}
	if spans[0].Status().Code.String() != "Error" {
		t.Errorf("status of a 429 = %v, want Error", spans[0].Status())
	}
	for _, s := range spans {
		for _, a := range s.Attributes() {
			if strings.Contains(a.Value.String(), "SECRET") {
				t.Errorf("span %s has the token in %s", s.Name(), a.Key)
			}
		}
	}
}

func TestTelegramHTTPClient__ErrorHasNoToken(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL
	server.Close() // the request fails on the network

	req, _ := http.NewRequest(http.MethodPost, url+"/bot"+secretToken+"/sendMessage", nil)
	_, err := newTelegramHTTPClient().Do(req)
	if err == nil {
		t.Fatal("no error")
	}
	if strings.Contains(err.Error(), "SECRET") || !strings.Contains(err.Error(), "/bot<token>/sendMessage") {
		t.Errorf("error = %q, want the URL without the token", err)
	}
}
