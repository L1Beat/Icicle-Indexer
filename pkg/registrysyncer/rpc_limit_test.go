package registrysyncer

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// roundTripFunc serves responses in memory, so no socket is opened.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// endless yields an unterminated JSON string forever, like a hostile L1 RPC
// streaming a huge "result".
type endless struct{ started bool }

func (e *endless) Read(p []byte) (int, error) {
	n := 0
	if !e.started {
		n = copy(p, `{"result":"0x`)
		e.started = true
	}
	for ; n < len(p); n++ {
		p[n] = 'a'
	}
	return n, nil
}

func TestRPCResultCapsResponseSize(t *testing.T) {
	var read int64
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := io.NopCloser(countingReader{&endless{}, &read})
		return &http.Response{StatusCode: 200, Body: body, Header: http.Header{}, Request: r}, nil
	})}

	_, err := rpcResult(client, "http://l1.invalid/rpc", "eth_getCode", []interface{}{"0x00", "latest"})
	if err == nil {
		t.Fatal("expected an error for an oversized response")
	}
	if read > maxRPCResponseBytes+64*1024 {
		t.Fatalf("read %d bytes, cap is %d", read, maxRPCResponseBytes)
	}
}

func TestRPCResultSmallResponse(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":1,"result":"0x6080"}`))
		return &http.Response{StatusCode: 200, Body: body, Header: http.Header{}, Request: r}, nil
	})}
	got, err := rpcResult(client, "http://l1.invalid/rpc", "eth_getCode", []interface{}{"0x00", "latest"})
	if err != nil || got != "0x6080" {
		t.Fatalf("got %q, %v", got, err)
	}
}

type countingReader struct {
	r io.Reader
	n *int64
}

func (c countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	*c.n += int64(n)
	return n, err
}
