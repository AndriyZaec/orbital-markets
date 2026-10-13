package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/api/middleware"
)

func TestPrepareReadsHTTPBodyBeforeStreamingProgress(t *testing.T) {
	for _, http2 := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			for _, chunked := range []bool{false, true} {
				name := "json"
				if stream {
					name = "stream"
				}
				if chunked {
					name += "_chunked"
				}
				if http2 {
					name += "_http2"
				}
				t.Run(name, func(t *testing.T) {
					f := newLocalPrepareFixture(t)
					server := httptest.NewUnstartedServer(middleware.Logging(f.s.logger)(http.HandlerFunc(f.s.handleLivePrepare)))
					server.EnableHTTP2 = http2
					if http2 {
						server.StartTLS()
					} else {
						server.Start()
					}
					defer server.Close()
					body, err := json.Marshal(f.body)
					if err != nil {
						t.Fatal(err)
					}
					request, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/live/prepare", bytes.NewReader(body))
					if err != nil {
						t.Fatal(err)
					}
					request.Header.Set("Content-Type", "application/json")
					if stream {
						request.Header.Set("Accept", "application/x-ndjson")
					}
					if chunked {
						request.ContentLength = -1
					}
					client := server.Client()
					client.Timeout = 3 * time.Second
					response, err := client.Do(request)
					if err != nil {
						t.Fatal(err)
					}
					defer response.Body.Close()
					if http2 && response.ProtoMajor != 2 {
						t.Fatalf("expected HTTP/2, got %s", response.Proto)
					}
					if stream && response.Header.Get("Content-Type") != "application/x-ndjson" {
						t.Fatal("missing streamed response content type")
					}
					decoder := json.NewDecoder(response.Body)
					var prepared localPreparedResult
					if stream {
						for {
							var event struct {
								Type, Stage string
								Status      int
								Body        json.RawMessage
							}
							if err := decoder.Decode(&event); err != nil {
								t.Fatalf("stream ended before result: %v", err)
							}
							if event.Type != "result" {
								continue
							}
							if event.Status != http.StatusOK {
								t.Fatalf("valid JSON body rejected over HTTP: status=%d body=%s", event.Status, event.Body)
							}
							if err := json.Unmarshal(event.Body, &prepared); err != nil {
								t.Fatal(err)
							}
							break
						}
					} else {
						if response.StatusCode != http.StatusOK {
							body, _ := io.ReadAll(response.Body)
							t.Fatalf("status=%d body=%s", response.StatusCode, body)
						}
						if err := decoder.Decode(&prepared); err != nil {
							t.Fatal(err)
						}
					}
					if prepared.SessionID == "" || len(prepared.SigningRequests) != 4 {
						t.Fatalf("incomplete prepare result: %+v", prepared)
					}
				})
			}
		}
	}
}

func TestPrepareMalformedHTTPBodyStillReportsStreamedError(t *testing.T) {
	f := newLocalPrepareFixture(t)
	server := httptest.NewServer(http.HandlerFunc(f.s.handleLivePrepare))
	defer server.Close()
	request, err := http.NewRequest(http.MethodPost, server.URL, bytes.NewBufferString(`{"leverage":`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", "application/x-ndjson")
	client := server.Client()
	client.Timeout = 3 * time.Second
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.Header.Get("Content-Type") != "application/x-ndjson" {
		t.Fatal("error stream lost its content type")
	}
	decoder := json.NewDecoder(response.Body)
	for {
		var event struct {
			Type   string
			Status int
			Body   struct{ Error string }
		}
		if err := decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
		if event.Type != "result" {
			continue
		}
		if event.Status != http.StatusBadRequest || event.Body.Error != "invalid request body" {
			t.Fatalf("malformed body result=%+v", event)
		}
		break
	}
}
