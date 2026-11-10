package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStreamOutlivesNormalResponseWriteTimeout(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		controller := streamController(w)
		if err := writeStream(w, controller, "data: first\n\n"); err != nil {
			return
		}
		time.Sleep(80 * time.Millisecond)
		if err := writeStream(w, controller, "data: second\n\n"); err != nil {
			t.Errorf("healthy stream expired: %v", err)
		}
	}))
	server.Config.WriteTimeout = 20 * time.Millisecond
	server.Start()
	defer server.Close()
	client := server.Client()
	client.Timeout = time.Second
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || !strings.Contains(string(body), "second") {
		t.Fatalf("stream truncated: %q err=%v", body, err)
	}
}
