package download_command

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"subteez/archive"
	"subteez/config"
	"subteez/subteez"
	"testing"
)

// stubConfig is a minimal config.Config implementation for driving the
// command against a local fake server.
type stubConfig struct {
	server      string
	interactive bool
	script      bool
}

func (s *stubConfig) GetServer() string                           { return s.server }
func (s *stubConfig) SetServer(v string)                          { s.server = v }
func (s *stubConfig) GetLanguageFilters() []subteez.Language      { return nil }
func (s *stubConfig) SetLanguageFilters([]subteez.Language)       {}
func (s *stubConfig) ClearLanguageFilters()                       {}
func (s *stubConfig) AddLanguageFilter(subteez.Language) error    { return nil }
func (s *stubConfig) RemoveLanguageFilter(subteez.Language) error { return nil }
func (s *stubConfig) IsInteractive() bool                         { return s.interactive }
func (s *stubConfig) SetInteractive(v bool)                       { s.interactive = v }
func (s *stubConfig) IsScriptMode() bool                          { return s.script }
func (s *stubConfig) SetScriptMode(v bool)                        { s.script = v }

var _ config.Config = (*stubConfig)(nil)

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

// chdirTemp moves the test into its own empty working directory (downloads are
// written to ".") and restores the original directory on cleanup.
func chdirTemp(t *testing.T) string {
	t.Helper()
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })
	return dir
}

func fakeZipServer(t *testing.T, zipData []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="pack.zip"`)
		w.Write(zipData)
	}))
}

func multilingualFixture(t *testing.T) []byte {
	return makeZip(t, map[string]string{
		"subs/Movie.english.srt":       "english subtitle\n",
		"subs/Movie.farsi_persian.srt": "persian subtitle\n",
		"subs/nested/Movie.es.srt":     "spanish subtitle\n",
		"subs/Movie [fr].ass":          "french ass\n",
		"readme.txt":                   "not a subtitle",
	})
}

func TestCLIDownloadArchiveSelectsRequestedLanguage(t *testing.T) {
	dir := chdirTemp(t)
	server := fakeZipServer(t, multilingualFixture(t))
	defer server.Close()

	cfg := &stubConfig{server: server.URL}
	args := []string{"download", "movie-title", "English", "2625728"}
	if err := Main(args, cfg); err != nil {
		t.Fatalf("Main: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "*.srt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly one extracted srt, got %v", matches)
	}
	content, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "english subtitle\n" {
		t.Fatalf("wrong language extracted: %q", content)
	}
}

func TestCLIDownloadArchiveFullIDForm(t *testing.T) {
	dir := chdirTemp(t)
	server := fakeZipServer(t, multilingualFixture(t))
	defer server.Close()

	cfg := &stubConfig{server: server.URL}
	args := []string{"download", "/subtitles/movie-title/farsi_persian/2625728"}
	if err := Main(args, cfg); err != nil {
		t.Fatalf("Main: %v", err)
	}

	matches, _ := filepath.Glob(filepath.Join(dir, "*.srt"))
	if len(matches) != 1 {
		t.Fatalf("expected one extracted srt, got %v", matches)
	}
	content, _ := os.ReadFile(matches[0])
	if string(content) != "persian subtitle\n" {
		t.Fatalf("expected persian subtitle, got %q", content)
	}
}

func TestCLIDownloadArchiveNameHint(t *testing.T) {
	dir := chdirTemp(t)
	zipData := makeZip(t, map[string]string{
		"Movie.en.forced.srt": "forced\n",
		"Movie.en.sdh.srt":    "sdh\n",
		"Movie.en.srt":        "normal\n",
	})
	server := fakeZipServer(t, zipData)
	defer server.Close()

	cfg := &stubConfig{server: server.URL}
	args := []string{"download", "movie-title", "en", "1", "forced"}
	if err := Main(args, cfg); err != nil {
		t.Fatalf("Main: %v", err)
	}

	matches, _ := filepath.Glob(filepath.Join(dir, "*.srt"))
	if len(matches) != 1 {
		t.Fatalf("expected one srt, got %v", matches)
	}
	content, _ := os.ReadFile(matches[0])
	if string(content) != "forced\n" {
		t.Fatalf("name hint not honored, got %q", content)
	}
}

func TestCLIDownloadArchiveNoMatchingLanguage(t *testing.T) {
	chdirTemp(t)
	server := fakeZipServer(t, multilingualFixture(t))
	defer server.Close()

	cfg := &stubConfig{server: server.URL}
	// Italian is not present in the fixture
	args := []string{"download", "movie-title", "Italian", "1"}
	err := Main(args, cfg)
	if !errors.Is(err, archive.ErrNoMatch) {
		t.Fatalf("expected archive.ErrNoMatch, got %v", err)
	}
}

func TestCLIDownloadArchiveWithoutSubtitles(t *testing.T) {
	chdirTemp(t)
	zipData := makeZip(t, map[string]string{"readme.txt": "hi", "image.png": "x"})
	server := fakeZipServer(t, zipData)
	defer server.Close()

	cfg := &stubConfig{server: server.URL}
	args := []string{"download", "movie-title", "English", "1"}
	if err := Main(args, cfg); !errors.Is(err, archive.ErrNoSubtitle) {
		t.Fatalf("expected archive.ErrNoSubtitle, got %v", err)
	}
}

func TestCLIDownloadArchiveCollisionRename(t *testing.T) {
	dir := chdirTemp(t)
	server := fakeZipServer(t, multilingualFixture(t))
	defer server.Close()

	cfg := &stubConfig{server: server.URL}
	args := []string{"download", "movie-title", "English", "1"}

	if err := Main(args, cfg); err != nil {
		t.Fatal(err)
	}
	// second run must not overwrite the first extracted file
	if err := Main(args, cfg); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	srtCount := 0
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".srt" {
			srtCount++
		}
	}
	if srtCount != 2 {
		t.Fatalf("expected 2 distinct srt files after collision, got %d", srtCount)
	}
}

func TestCLIDownloadPlainFileKeepsLegacyBehavior(t *testing.T) {
	dir := chdirTemp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="single.srt"`)
		w.Write([]byte("plain single file\n"))
	}))
	defer server.Close()

	cfg := &stubConfig{server: server.URL}
	args := []string{"download", "movie-title", "English", "1"}
	if err := Main(args, cfg); err != nil {
		t.Fatalf("Main: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "single.srt"))
	if err != nil {
		t.Fatalf("plain file not written under original name: %v", err)
	}
	if string(content) != "plain single file\n" {
		t.Fatalf("unexpected content %q", content)
	}
}

func TestCLIDownloadScriptModeStreamsSubtitleNotZip(t *testing.T) {
	chdirTemp(t)
	server := fakeZipServer(t, multilingualFixture(t))
	defer server.Close()

	// capture stdout while running in script mode
	original := os.Stdout
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = write

	cfg := &stubConfig{server: server.URL, script: true}
	args := []string{"download", "movie-title", "es", "1"}
	runErr := Main(args, cfg)

	_ = write.Close()
	os.Stdout = original

	captured, _ := io.ReadAll(read)
	if runErr != nil {
		t.Fatalf("Main: %v", runErr)
	}
	if bytes.HasPrefix(captured, []byte("PK")) {
		t.Fatal("script mode must stream the extracted subtitle, not the raw ZIP")
	}
	if string(captured) != "spanish subtitle\n" {
		t.Fatalf("unexpected stdout content: %q", captured)
	}
}

func TestCLIDownloadServerError(t *testing.T) {
	chdirTemp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	cfg := &stubConfig{server: server.URL}
	args := []string{"download", "movie-title", "English", "1"}
	if err := Main(args, cfg); !errors.Is(err, subteez.ErrNotFound) {
		t.Fatalf("expected subteez.ErrNotFound, got %v", err)
	}
}
