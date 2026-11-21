package subteez

import (
	"archive/zip"
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// makeZip builds a small in-memory ZIP served by the fake servers below.
func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	buffer := new(bytes.Buffer)
	writer := zip.NewWriter(buffer)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func newClientFor(server *httptest.Server) ISubteezAPI {
	return NewClient(server.URL)
}

func TestDownloadZipWithFilename(t *testing.T) {
	zipData := makeZip(t, map[string]string{"Movie.en.srt": "1\nhello\n"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("id") == "" {
			t.Errorf("request missing id query parameter: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Disposition", `attachment; filename="pack.zip"`)
		w.Write(zipData)
	}))
	defer server.Close()

	client := newClientFor(server)
	name, data, err := client.Download(SubtitleDownloadRequest{ID: "/subtitles/x/english/1"})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if name != "pack.zip" {
		t.Errorf("filename = %q, want pack.zip", name)
	}
	if !bytes.Equal(data, zipData) {
		t.Error("downloaded bytes differ from served fixture")
	}
}

func TestDownloadFallbackFilename(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// no Content-Disposition header at all
		w.Write([]byte("WEBVTT\n"))
	}))
	defer server.Close()

	client := newClientFor(server)
	name, _, err := client.Download(SubtitleDownloadRequest{ID: "/subtitles/movie-title/english/2625728"})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if name != "2625728" {
		t.Errorf("fallback filename = %q, want 2625728", name)
	}
}

func TestDownloadHTTPStatusErrors(t *testing.T) {
	cases := map[int]error{
		http.StatusNotFound:            ErrNotFound,
		http.StatusBadRequest:          ErrBadRequest,
		http.StatusInternalServerError: ErrServer,
	}
	for status, want := range cases {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))
		client := newClientFor(server)
		_, _, err := client.Download(SubtitleDownloadRequest{ID: "/x"})
		if !errors.Is(err, want) {
			t.Errorf("status %d: expected %v, got %v", status, want, err)
		}
		server.Close()
	}
}

func TestDownloadSizeLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// claim and serve one byte more than the allowed maximum
		oversized := make([]byte, MaxDownloadSize+1)
		w.Header().Set("Content-Length", strconv.Itoa(len(oversized)))
		w.Write(oversized)
	}))
	defer server.Close()

	client := newClientFor(server)
	_, _, err := client.Download(SubtitleDownloadRequest{ID: "/x"})
	if !errors.Is(err, ErrDownloadTooLarge) {
		t.Fatalf("expected ErrDownloadTooLarge, got %v", err)
	}
}

func TestDownloadInterruptedConnection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// hijack the connection and drop it mid-response: the client must
		// return a transport error rather than hang or accept partial data.
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Skip("hijacking not supported")
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			t.Fatal(err)
		}
		buf.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 1000\r\n\r\npartial")
		buf.Flush()
		conn.Close()
	}))
	defer server.Close()

	client := newClientFor(server)
	_, _, err := client.Download(SubtitleDownloadRequest{ID: "/x"})
	if err == nil {
		t.Fatal("expected an error on interrupted connection, got nil")
	}
	if strings.Contains(err.Error(), "too large") {
		t.Fatalf("interruption must surface as a transport error, got %v", err)
	}
}

func TestLanguageFromDownloadID(t *testing.T) {
	cases := map[string]string{
		"/subtitles/arcane/english/2625728":  "en",
		"/subtitles/arcane/farsi_persian/12": "fa",
		"/subtitles/arcane/german/9":         "de",
		"/subtitles/arcane/unknown-lang/9":   "",
		"not-a-full-id":                      "",
		"/subtitles/only/two":                "",
	}
	for id, want := range cases {
		if got := LanguageFromDownloadID(id); got != want {
			t.Errorf("LanguageFromDownloadID(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestDownloadConnectionReset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// accept then tear the connection down immediately, before any
		// response byte is written: a dead server must be a deterministic
		// transport error, not a hang or an empty successful download.
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, err := hj.Hijack()
			if err == nil {
				conn.Close()
				return
			}
		}
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	client := newClientFor(server)
	_, _, err := client.Download(SubtitleDownloadRequest{ID: "/x"})
	if err == nil {
		t.Fatal("expected a transport error on connection reset, got nil")
	}
}
