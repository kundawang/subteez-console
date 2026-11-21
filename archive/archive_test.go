package archive

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// --- fixture builders -----------------------------------------------------

// buildZip creates an in-memory ZIP from the given name/content pairs.
func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	buffer := new(bytes.Buffer)
	writer := zip.NewWriter(buffer)
	// deterministic order for readable assertions
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	for _, name := range names {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %q: %v", name, err)
		}
		if _, err := entry.Write([]byte(files[name])); err != nil {
			t.Fatalf("write zip entry %q: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buffer.Bytes()
}

// multilingualZip is the main fixture: one archive with several languages,
// nested directories, a non-subtitle file and a readme.
func multilingualZip(t *testing.T) []byte {
	return buildZip(t, map[string]string{
		"subs/Movie.english.srt":       "1\n00:00:01,000 --> 00:00:02,000\nEnglish line\n",
		"subs/Movie.farsi_persian.srt": "1\n00:00:01,000 --> 00:00:02,000\nPersian line\n",
		"subs/spanish/Movie.es.srt":    "1\n00:00:01,000 --> 00:00:02,000\nSpanish line\n",
		"subs/french/Movie [fr].ass":   "[Script Info]\nFrench ASS\n",
		"subs/german/Movie.deu.vtt":    "WEBVTT\nGerman VTT\n",
		"subs/notes.txt":               "not a subtitle",
		"readme.md":                    "readme",
	})
}

// --- IsZip ----------------------------------------------------------------

func TestIsZip(t *testing.T) {
	zipData := buildZip(t, map[string]string{"a.srt": "x"})
	if !IsZip(zipData) {
		t.Fatal("expected zip data to be detected")
	}
	if IsZip([]byte("WEBVTT\nplain subtitle")) {
		t.Fatal("plain text must not be detected as zip")
	}
	if IsZip([]byte("PK")) {
		t.Fatal("truncated signature must not be detected as zip")
	}
	if IsZip(nil) {
		t.Fatal("nil data must not be detected as zip")
	}
}

// --- ListSubtitles --------------------------------------------------------

func TestListSubtitlesMultilingual(t *testing.T) {
	data := multilingualZip(t)
	entries, err := ListSubtitles(data)
	if err != nil {
		t.Fatalf("ListSubtitles: %v", err)
	}
	if len(entries) != 5 {
		t.Fatalf("expected 5 subtitle entries, got %d: %+v", len(entries), entries)
	}

	// every output name must be a bare base name, unique and extension-valid
	seen := make(map[string]bool)
	for _, entry := range entries {
		if strings.ContainsAny(entry.UniqueName, `/\`) {
			t.Errorf("UniqueName %q must not contain path separators", entry.UniqueName)
		}
		if seen[entry.UniqueName] {
			t.Errorf("duplicate UniqueName %q", entry.UniqueName)
		}
		seen[entry.UniqueName] = true
		ext := strings.ToLower(filepath.Ext(entry.UniqueName))
		if !subtitleExtensions[ext] {
			t.Errorf("entry %q has unsupported extension %q", entry.UniqueName, ext)
		}
	}
}

func TestListSubtitlesLanguageDetection(t *testing.T) {
	data := multilingualZip(t)
	entries, err := ListSubtitles(data)
	if err != nil {
		t.Fatalf("ListSubtitles: %v", err)
	}
	got := make(map[string]string) // raw name -> detected language
	for _, entry := range entries {
		got[entry.RawName] = entry.Language
	}
	cases := map[string]string{
		"subs/Movie.english.srt":       "en",
		"subs/Movie.farsi_persian.srt": "fa",
		"subs/spanish/Movie.es.srt":    "es",
		"subs/french/Movie [fr].ass":   "fr",
		"subs/german/Movie.deu.vtt":    "de",
	}
	for raw, want := range cases {
		if got[raw] != want {
			t.Errorf("DetectLanguage(%q) = %q, want %q", raw, got[raw], want)
		}
	}
}

func TestListSubtitlesNoSubtitle(t *testing.T) {
	data := buildZip(t, map[string]string{"readme.txt": "hi", "image.png": "x"})
	_, err := ListSubtitles(data)
	if !errors.Is(err, ErrNoSubtitle) {
		t.Fatalf("expected ErrNoSubtitle, got %v", err)
	}
}

func TestListSubtitlesInvalid(t *testing.T) {
	if _, err := ListSubtitles([]byte("PKnot really a zip body")); !errors.Is(err, ErrInvalidZip) {
		t.Fatalf("expected ErrInvalidZip, got %v", err)
	}
}

// --- Zip Slip -------------------------------------------------------------

func TestZipSlipTraversal(t *testing.T) {
	malicious := map[string]string{
		"../escape.srt":         "x",
		"../../tmp/escape2.srt": "x",
	}
	data := buildZip(t, malicious)
	_, err := ListSubtitles(data)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("expected ErrUnsafePath for ../ entry, got %v", err)
	}
}

func TestZipSlipBackslashAndAbsolute(t *testing.T) {
	// backslash traversal and an absolute-looking unix path
	malicious := map[string]string{
		`..\windows\escape.srt`: "x",
	}
	data := buildZip(t, malicious)
	if _, err := ListSubtitles(data); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("expected ErrUnsafePath for backslash entry, got %v", err)
	}

	absolute := buildZip(t, map[string]string{"/etc/cron.d/evil.srt": "x"})
	if _, err := ListSubtitles(absolute); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("expected ErrUnsafePath for absolute entry, got %v", err)
	}
}

func TestWriteSubtitleStaysInsideDir(t *testing.T) {
	dir := t.TempDir()
	data := multilingualZip(t)
	entries, err := ListSubtitles(data)
	if err != nil {
		t.Fatalf("ListSubtitles: %v", err)
	}

	for _, entry := range entries {
		path, err := WriteSubtitle(dir, entry, []byte("content"), CollisionRename)
		if err != nil {
			t.Fatalf("WriteSubtitle %q: %v", entry.UniqueName, err)
		}
		if filepath.Dir(path) != dir {
			t.Fatalf("entry %q written outside target dir: %s", entry.RawName, path)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("stat written file: %v", err)
		}
	}
}

func TestWriteSubtitleRejectsPathBearingName(t *testing.T) {
	dir := t.TempDir()
	entry := SubtitleEntry{UniqueName: "../escape.srt"}
	if _, err := WriteSubtitle(dir, entry, nil, CollisionRename); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("expected ErrUnsafePath, got %v", err)
	}
	entry = SubtitleEntry{UniqueName: "nested/escape.srt"}
	if _, err := WriteSubtitle(dir, entry, nil, CollisionRename); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("expected ErrUnsafePath, got %v", err)
	}
}

// --- selection ------------------------------------------------------------

func TestSelectByLanguage(t *testing.T) {
	data := multilingualZip(t)
	entries, err := ListSubtitles(data)
	if err != nil {
		t.Fatalf("ListSubtitles: %v", err)
	}

	cases := map[string]string{
		"en": "Movie.english.srt",
		"fa": "Movie.farsi_persian.srt",
		"es": "Movie.es.srt",
		"fr": "Movie [fr].ass",
		"de": "Movie.deu.vtt",
	}
	for lang, wantRaw := range cases {
		entry, err := Select(entries, lang, "")
		if err != nil {
			t.Fatalf("Select(%q): %v", lang, err)
		}
		if entry.RawName != "subs/"+wantRaw && !strings.HasSuffix(entry.RawName, wantRaw) {
			t.Errorf("Select(%q) = %q, want suffix %q", lang, entry.RawName, wantRaw)
		}
	}
}

func TestSelectNoMatchingLanguage(t *testing.T) {
	data := multilingualZip(t)
	entries, _ := ListSubtitles(data)
	if _, err := Select(entries, "it", ""); !errors.Is(err, ErrNoMatch) {
		t.Fatalf("expected ErrNoMatch for missing language, got %v", err)
	}
}

func TestSelectByNameHint(t *testing.T) {
	data := buildZip(t, map[string]string{
		"Movie.en.forced.srt": "forced",
		"Movie.en.sdh.srt":    "sdh",
		"Movie.en.srt":        "normal",
	})
	entries, err := ListSubtitles(data)
	if err != nil {
		t.Fatalf("ListSubtitles: %v", err)
	}

	entry, err := Select(entries, "en", "forced")
	if err != nil {
		t.Fatalf("Select hint: %v", err)
	}
	if entry.RawName != "Movie.en.forced.srt" {
		t.Errorf("hint select = %q, want forced entry", entry.RawName)
	}

	if _, err := Select(entries, "en", "nonexistent"); !errors.Is(err, ErrNoMatch) {
		t.Fatalf("expected ErrNoMatch for unknown hint, got %v", err)
	}
}

func TestSelectDeterministicOrder(t *testing.T) {
	data := buildZip(t, map[string]string{
		"b.en.srt": "b",
		"a.en.srt": "a",
	})
	entries, _ := ListSubtitles(data)
	first, _ := Select(entries, "en", "")
	second, _ := Select(entries, "en", "")
	if first.RawName != second.RawName {
		t.Fatalf("Select is not deterministic: %q vs %q", first.RawName, second.RawName)
	}
	if first.RawName != "b.en.srt" {
		t.Fatalf("expected archive order preserved, got %q", first.RawName)
	}
}

func TestSelectEmptyLanguageFallsBackToFirst(t *testing.T) {
	data := multilingualZip(t)
	entries, _ := ListSubtitles(data)
	entry, err := Select(entries, "", "")
	if err != nil {
		t.Fatalf("Select with empty language: %v", err)
	}
	if entry.Index != entries[0].Index {
		t.Fatal("empty language should select the first entry")
	}
}

// --- duplicate names ------------------------------------------------------

func TestDuplicateBaseNamesAreUnique(t *testing.T) {
	data := buildZip(t, map[string]string{
		"dir1/Movie.srt": "one",
		"dir2/Movie.srt": "two",
		"dir3/Movie.srt": "three",
	})
	entries, err := ListSubtitles(data)
	if err != nil {
		t.Fatalf("ListSubtitles: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	names := map[string]bool{}
	for _, entry := range entries {
		if names[entry.UniqueName] {
			t.Fatalf("UniqueName collision: %q", entry.UniqueName)
		}
		names[entry.UniqueName] = true
	}
	if !names["Movie.srt"] || !names["Movie (1).srt"] || !names["Movie (2).srt"] {
		t.Fatalf("unexpected unique names: %v", names)
	}
}

// --- read & CRC -----------------------------------------------------------

func TestReadEntryContent(t *testing.T) {
	data := multilingualZip(t)
	entries, _ := ListSubtitles(data)
	entry, _ := Select(entries, "es", "")
	content, err := ReadEntry(data, entry)
	if err != nil {
		t.Fatalf("ReadEntry: %v", err)
	}
	if !strings.Contains(string(content), "Spanish line") {
		t.Fatalf("unexpected content: %q", content)
	}
}

func TestBadCRC(t *testing.T) {
	// build a STORED (uncompressed) entry: flipping a payload byte then keeps
	// the stream readable but guarantees a CRC-32 mismatch on close.
	buffer := new(bytes.Buffer)
	writer := zip.NewWriter(buffer)
	entry, err := writer.CreateHeader(&zip.FileHeader{
		Name:   "Movie.en.srt",
		Method: zip.Store,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(strings.Repeat("subtitle line\n", 32))
	if _, err := entry.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	good := buffer.Bytes()

	bad := append([]byte(nil), good...)
	offset := bytes.Index(bad, []byte("Movie.en.srt"))
	if offset < 0 {
		t.Fatal("fixture entry not found")
	}
	payloadOffset := offset + len("Movie.en.srt")
	if payloadOffset >= len(bad) {
		t.Fatal("fixture too small to corrupt")
	}
	bad[payloadOffset] ^= 0xFF

	entries, err := ListSubtitles(bad)
	if err != nil {
		t.Fatalf("listing a CRC-bad archive should stay lazy, got %v", err)
	}
	if _, err := ReadEntry(bad, entries[0]); !errors.Is(err, ErrChecksum) {
		t.Fatalf("expected ErrChecksum, got %v", err)
	}
}

// --- limits ---------------------------------------------------------------

func TestTooManyEntries(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < MaxEntries+1; i++ {
		files["entry-"+strconv.Itoa(i)+".srt"] = "x"
	}
	data := buildZip(t, files)
	_, err := ListSubtitles(data)
	if !errors.Is(err, ErrTooManyEntries) {
		t.Fatalf("expected ErrTooManyEntries, got %v", err)
	}
}

func TestEntryTooLargeDeclared(t *testing.T) {
	// a single entry whose declared uncompressed size exceeds the limit.
	// Build it manually with a fake data descriptor-free stored entry.
	buffer := new(bytes.Buffer)
	writer := zip.NewWriter(buffer)
	entry, err := writer.CreateHeader(&zip.FileHeader{
		Name:   "huge.srt",
		Method: zip.Store,
	})
	if err != nil {
		t.Fatal(err)
	}
	// write exactly MaxEntrySize+1 zeros (stored, so declared size is real)
	if _, err := entry.Write(make([]byte, MaxEntrySize+1)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ListSubtitles(buffer.Bytes()); !errors.Is(err, ErrEntryTooLarge) {
		t.Fatalf("expected ErrEntryTooLarge, got %v", err)
	}
}

func TestArchiveTotalSizeLimit(t *testing.T) {
	files := map[string]string{}
	// many entries each under the single-entry cap but over the total cap
	chunk := strings.Repeat("a", 1024*1024) // 1 MiB
	for i := 0; i < int(MaxTotalSize/(1024*1024))+1; i++ {
		files["entry-"+strconv.Itoa(i)+".srt"] = chunk
	}
	data := buildZip(t, files)
	_, err := ListSubtitles(data)
	if !errors.Is(err, ErrArchiveTooLarge) && !errors.Is(err, ErrTooManyEntries) {
		t.Fatalf("expected a size/entry limit error, got %v", err)
	}
}

// --- collision policies ---------------------------------------------------

func TestCollisionPolicies(t *testing.T) {
	dir := t.TempDir()
	entry := SubtitleEntry{UniqueName: "Movie.en.srt"}

	// first write always succeeds
	path, err := WriteSubtitle(dir, entry, []byte("one"), CollisionRename)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "Movie.en.srt" {
		t.Fatalf("unexpected first path %q", path)
	}

	// rename policy: existing file is preserved and a new name is generated
	renamed, err := WriteSubtitle(dir, entry, []byte("two"), CollisionRename)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(renamed) != "Movie.en (1).srt" {
		t.Fatalf("expected renamed target, got %q", filepath.Base(renamed))
	}
	original, err := os.ReadFile(path)
	if err != nil || string(original) != "one" {
		t.Fatal("rename policy must preserve the existing file")
	}

	// error policy: deterministic failure
	if _, err := WriteSubtitle(dir, entry, []byte("three"), CollisionError); !errors.Is(err, ErrFileExists) {
		t.Fatalf("expected ErrFileExists, got %v", err)
	}

	// overwrite policy: content is replaced
	overwritten, err := WriteSubtitle(dir, entry, []byte("three"), CollisionOverwrite)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(overwritten) != "Movie.en.srt" {
		t.Fatalf("overwrite must reuse the name, got %q", overwritten)
	}
	content, err := os.ReadFile(overwritten)
	if err != nil || string(content) != "three" {
		t.Fatalf("overwrite failed, content=%q err=%v", content, err)
	}
}

// --- end-to-end helper ----------------------------------------------------

func TestExtractSubtitleFlow(t *testing.T) {
	dir := t.TempDir()
	data := multilingualZip(t)

	path, err := ExtractSubtitle(data, dir, "fa", "", CollisionRename)
	if err != nil {
		t.Fatalf("ExtractSubtitle: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "Persian line") {
		t.Fatalf("wrong subtitle extracted: %q", content)
	}
	if filepath.Dir(path) != dir {
		t.Fatal("extraction escaped the target directory")
	}
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"normal.srt":     "normal.srt",
		"a/b/c.srt":      "c.srt",
		`a\b\c.srt`:      "c.srt",
		"bad:name?.srt":  "bad_name_.srt",
		"CON.srt":        "_CON.srt",
		"trailingdot.":   "trailingdot",
		"  spaced  .srt": "spaced  .srt",
	}
	for input, want := range cases {
		if got := sanitizeName(input); got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDetectLanguageVariants(t *testing.T) {
	cases := map[string]string{
		"Movie.eng.srt":           "en",
		"Movie.EN.srt":            "en",
		"Movie_es-spa.srt":        "es",
		"Movie [eng].srt":         "en",
		"Movie.german.forced.srt": "de",
		"Movie.enforced.srt":      "en",
		"random.title.srt":        "",
		"energy.srt":              "",
	}
	for name, want := range cases {
		if got := DetectLanguage(name); got != want {
			t.Errorf("DetectLanguage(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestEmptyArchive(t *testing.T) {
	data := buildZip(t, map[string]string{})
	if _, err := ListSubtitles(data); !errors.Is(err, ErrNoSubtitle) {
		t.Fatalf("expected ErrNoSubtitle for empty archive, got %v", err)
	}
}

func TestUnsupportedExtensionsIgnored(t *testing.T) {
	data := buildZip(t, map[string]string{
		"a.txt": "x",
		"b.jpg": "x",
		"c.SRT": "uppercase ext", // extension check is case-insensitive
		"d.VTT": "vtt body",
		"e.ASS": "ass body",
	})
	entries, err := ListSubtitles(data)
	if err != nil {
		t.Fatalf("ListSubtitles: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 supported subtitles, got %d: %+v", len(entries), entries)
	}
}

func TestDirectoryAndSpecialEntriesIgnored(t *testing.T) {
	buffer := new(bytes.Buffer)
	writer := zip.NewWriter(buffer)
	if _, err := writer.Create("directory/"); err != nil {
		t.Fatal(err)
	}
	entry, err := writer.Create("real.srt")
	if err != nil {
		t.Fatal(err)
	}
	entry.Write([]byte("real"))
	writer.Close()

	entries, err := ListSubtitles(buffer.Bytes())
	if err != nil {
		t.Fatalf("ListSubtitles: %v", err)
	}
	if len(entries) != 1 || entries[0].RawName != "real.srt" {
		t.Fatalf("directory entry must be ignored, got %+v", entries)
	}
}

func TestReadEntryIndexValidation(t *testing.T) {
	data := multilingualZip(t)
	if _, err := ReadEntry(data, SubtitleEntry{Index: 999}); !errors.Is(err, ErrInvalidZip) {
		t.Fatalf("expected ErrInvalidZip for out-of-range index, got %v", err)
	}
	if _, err := ReadEntry(data, SubtitleEntry{Index: -1}); !errors.Is(err, ErrInvalidZip) {
		t.Fatalf("expected ErrInvalidZip for negative index, got %v", err)
	}
}

func TestSelectOnEmptySlice(t *testing.T) {
	if _, err := Select(nil, "en", ""); !errors.Is(err, ErrNoMatch) {
		t.Fatalf("expected ErrNoMatch on empty entries, got %v", err)
	}
}

func TestExtractAllSupportedFormats(t *testing.T) {
	data := buildZip(t, map[string]string{
		"m.en.srt": "srt-body",
		"m.en.vtt": "WEBVTT\nvtt-body",
		"m.en.ass": "[Script Info]\nass-body",
	})
	dir := t.TempDir()
	for _, hint := range []string{"srt", "vtt", "ass"} {
		path, err := ExtractSubtitle(data, dir, "en", hint, CollisionRename)
		if err != nil {
			t.Fatalf("extract %s: %v", hint, err)
		}
		if filepath.Ext(path) != "."+hint {
			t.Errorf("hint %s resolved to %s", hint, path)
		}
	}
}

func TestSanitizeNameLengthCap(t *testing.T) {
	long := strings.Repeat("a", maxNameLength+50) + ".srt"
	got := sanitizeName(long)
	if len([]rune(got)) > maxNameLength+len(".srt") {
		t.Fatalf("capped name too long: %d runes", len([]rune(got)))
	}
	// the extension must survive the cap
	if filepath.Ext(got) != ".srt" {
		t.Fatalf("extension lost after capping: %q", got)
	}
	// a multi-byte name must not be cut in the middle of a rune
	unicodeName := strings.Repeat("字幕", maxNameLength) + ".srt"
	capped := sanitizeName(unicodeName)
	if !utf8.ValidString(capped) {
		t.Fatal("capped name is not valid UTF-8: a rune was split")
	}
}
