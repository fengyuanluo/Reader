package fetcher

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"reader/internal/domain/fetch"
)

func TestHTTPFetcherTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte("late"))
	}))
	defer server.Close()

	f := NewHTTPFetcher(WithAllowPrivateNetwork(true), WithTimeout(5*time.Millisecond))
	_, err := f.Fetch(context.Background(), fetch.Request{URL: server.URL})
	if err == nil {
		t.Fatal("Fetch() error is nil, want timeout")
	}
}

func TestHTTPFetcherOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("too-large"))
	}))
	defer server.Close()

	f := NewHTTPFetcher(WithAllowPrivateNetwork(true), WithMaxBodyBytes(3))
	_, err := f.Fetch(context.Background(), fetch.Request{URL: server.URL})
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("Fetch() error = %v, want too large", err)
	}
}

func TestHTTPFetcherTooManyRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/next", http.StatusFound)
	}))
	defer server.Close()

	f := NewHTTPFetcher(WithAllowPrivateNetwork(true), WithMaxRedirects(1))
	_, err := f.Fetch(context.Background(), fetch.Request{URL: server.URL})
	if err == nil || !strings.Contains(err.Error(), "too many redirects") {
		t.Fatalf("Fetch() error = %v, want too many redirects", err)
	}
}

func TestHTTPFetcherBlocksRedirectToPrivateIP(t *testing.T) {
	publicIP := net.ParseIP("93.184.216.34")
	privateIP := net.ParseIP("127.0.0.1")
	resolver := func(host string) ([]net.IP, error) {
		switch host {
		case "public.example.test":
			return []net.IP{publicIP}, nil
		case "private.example.test":
			return []net.IP{privateIP}, nil
		default:
			return nil, errors.New("unexpected host")
		}
	}
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"http://private.example.test/secret"}},
			Body:       http.NoBody,
			Request:    req,
		}, nil
	})
	client := &http.Client{Transport: transport}
	f := NewHTTPFetcher(WithHTTPClient(client), WithResolver(resolver), WithMaxRedirects(5))

	_, err := f.Fetch(context.Background(), fetch.Request{URL: "http://public.example.test/start"})
	if !errors.Is(err, fetch.ErrBlocked) {
		t.Fatalf("Fetch() error = %v, want ErrBlocked", err)
	}
}

func TestCheckHostBlocksMetadataIP(t *testing.T) {
	resolver := func(host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("169.254.169.254")}, nil
	}
	if err := checkHostWithResolver("metadata.example.test", resolver); !errors.Is(err, fetch.ErrBlocked) {
		t.Fatalf("checkHostWithResolver() error = %v, want ErrBlocked", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
