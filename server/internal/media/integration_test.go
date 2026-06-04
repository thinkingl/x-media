package media

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestHTTPFLVOutput_FileToFLVFlow(t *testing.T) {
	output, err := NewHTTPFLVOutput(&OutputConfig{
		ID:   "flv_integration_test",
		Type: "httpflv",
		Addr: ":0",
	})
	assert.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	err = output.StartWithFile(ctx, testFixturePath(t, "../../test/fixtures/h265_test.mp4"))
	assert.NoError(t, err)
	assert.Equal(t, StreamStatusRunning, output.Status())

	mux := http.NewServeMux()
	mux.Handle(output.GetRoutePath(), output)
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + output.GetRoutePath())
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, "video/x-flv", resp.Header.Get("Content-Type"))

	buf := make([]byte, 4096)
	n, err := io.ReadAtLeast(resp.Body, buf, 13)
	assert.NoError(t, err, "should read at least FLV header (13 bytes)")
	assert.GreaterOrEqual(t, n, 13, "should receive FLV header")

	assert.Equal(t, byte(0x46), buf[0], "FLV magic byte 'F'")
	assert.Equal(t, byte(0x4C), buf[1], "FLV magic byte 'L'")
	assert.Equal(t, byte(0x56), buf[2], "FLV magic byte 'V'")

	err = output.Stop()
	assert.NoError(t, err)
	assert.Equal(t, StreamStatusStopped, output.Status())
}

func TestHTTPFLVOutput_MultipleClients(t *testing.T) {
	output, err := NewHTTPFLVOutput(&OutputConfig{
		ID:   "flv_multi_client_test",
		Type: "httpflv",
		Addr: ":0",
	})
	assert.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	err = output.StartWithFile(ctx, testFixturePath(t, "../../test/fixtures/h265_test.mp4"))
	assert.NoError(t, err)

	mux := http.NewServeMux()
	mux.Handle(output.GetRoutePath(), output)
	server := httptest.NewServer(mux)
	defer server.Close()

	done := make(chan bool, 2)
	for i := 0; i < 2; i++ {
		go func() {
			resp, err := http.Get(server.URL + output.GetRoutePath())
			if err != nil {
				done <- false
				return
			}
			defer resp.Body.Close()

			buf := make([]byte, 13)
			_, err = io.ReadAtLeast(resp.Body, buf, 13)
			done <- err == nil && buf[0] == 0x46
		}()
	}

	success := 0
	for i := 0; i < 2; i++ {
		select {
		case ok := <-done:
			if ok {
				success++
			}
		case <-time.After(10 * time.Second):
			t.Fatal("timeout waiting for client")
		}
	}
	assert.Equal(t, 2, success, "both clients should receive FLV data")

	output.Stop()
}
