// Safe extraction and selection of subtitle files inside downloaded ZIP archives.
//
// The data handled by this package is untrusted: it comes from a network
// download. Every entry name is validated before use, every output path is
// confined to the target directory and every decompressed stream is bounded by
// fixed size limits, so a malformed or malicious archive can neither escape the
// target directory nor exhaust available memory or disk space.

package archive

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// supported subtitle file extensions (compared case-insensitively)
var subtitleExtensions = map[string]bool{
	".srt": true,
	".vtt": true,
	".ass": true,
}

// fixed resource limits, applied to every archive regardless of its size
const (
	// MaxEntries bounds the number of entries scanned inside one archive.
	MaxEntries = 10000
	// MaxEntrySize bounds the decompressed size of a single subtitle entry.
	MaxEntrySize int64 = 100 * 1024 * 1024 // 100 MiB
	// MaxTotalSize bounds the sum of declared decompressed sizes of all
	// subtitle entries inside one archive.
	MaxTotalSize int64 = 500 * 1024 * 1024 // 500 MiB
	// maxNameLength bounds the length of a generated output file name.
	maxNameLength = 200
)

// sentinel errors. callers can use errors.Is to react to them deterministically.
var (
	// ErrInvalidZip means the data claims to be a ZIP but cannot be parsed.
	ErrInvalidZip = errors.New("malformed zip archive")

	// ErrChecksum means the CRC-32 checksum of an entry does not match.
	ErrChecksum = errors.New("corrupted subtitle entry: checksum mismatch")

	// ErrTooManyEntries means the archive exceeds MaxEntries.
	ErrTooManyEntries = errors.New("archive contains too many entries")

	// ErrEntryTooLarge means a single entry exceeds MaxEntrySize.
	ErrEntryTooLarge = errors.New("subtitle entry is too large")

	// ErrArchiveTooLarge means the archive exceeds MaxTotalSize.
	ErrArchiveTooLarge = errors.New("archive content is too large")

	// ErrUnsafePath means an entry name escapes the archive root
	// (absolute path or ".." traversal) after separator normalization.
	ErrUnsafePath = errors.New("unsafe entry path in archive")

	// ErrNoSubtitle means the archive contains no SRT/VTT/ASS file.
	ErrNoSubtitle = errors.New("no subtitle (srt/vtt/ass) found in archive")

	// ErrNoMatch means Select found no entry matching the requested
	// language or name hint.
	ErrNoMatch = errors.New("no subtitle matches the requested language or name")

	// ErrFileExists means a CollisionError-free write was requested but the
	// target file already exists.
	ErrFileExists = errors.New("target file already exists")
)

// CollisionPolicy defines what WriteSubtitle does when the target file already
// exists on disk.
type CollisionPolicy int

const (
	// CollisionRename keeps the existing file and writes to
	// "name (1).ext", "name (2).ext", ...
	CollisionRename CollisionPolicy = iota
	// CollisionOverwrite truncates and replaces the existing file.
	CollisionOverwrite
	// CollisionError aborts the write and returns ErrFileExists.
	CollisionError
)

// SubtitleEntry is one selectable subtitle file found inside an archive.
type SubtitleEntry struct {
	// Index is the position of the entry inside the ZIP central directory.
	Index int
	// RawName is the entry name exactly as stored in the archive.
	RawName string
	// UniqueName is a sanitized, collision-free base name (no directory
	// components) safe to use as an output file name.
	UniqueName string
	// Language is the language code detected from the entry name, or "" when
	// none was detected.
	Language string
	// Size is the declared decompressed size in bytes.
	Size int64
}

// IsZip reports whether data begins with the local file header signature of a
// ZIP archive ("PK\x03\x04", including the empty/spanned variants). It never
// returns an error: non-ZIP data is simply a plain single-file download.
func IsZip(data []byte) bool {
	return len(data) >= 4 &&
		data[0] == 0x50 && data[1] == 0x4B &&
		(data[2] == 0x03 || data[2] == 0x05 || data[2] == 0x07) &&
		(data[3] == 0x04 || data[3] == 0x06 || data[3] == 0x08)
}

// ListSubtitles opens the ZIP archive in data and returns every SRT/VTT/ASS
// entry it contains. Entries are ordered by their position in the central
// directory; UniqueName values are unique within the returned slice.
//
// All size limits and path checks are enforced here, before any entry data is
// decompressed.
func ListSubtitles(data []byte) ([]SubtitleEntry, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, ErrInvalidZip
	}

	if len(reader.File) > MaxEntries {
		return nil, ErrTooManyEntries
	}

	var entries []SubtitleEntry
	var totalSize int64
	usedNames := make(map[string]bool)

	for index, file := range reader.File {
		// only regular files can carry subtitles; directories, symlinks and
		// other special entries are ignored.
		if file.FileInfo().IsDir() || file.Mode()&os.ModeType != 0 {
			continue
		}

		base, ok := safeBaseName(file.Name)
		if !ok {
			return nil, ErrUnsafePath
		}

		ext := strings.ToLower(filepath.Ext(base))
		if !subtitleExtensions[ext] {
			continue
		}

		// declared size is checked up-front so a compression bomb is rejected
		// without allocating its decompressed content.
		if file.UncompressedSize64 > uint64(MaxEntrySize) {
			return nil, ErrEntryTooLarge
		}
		totalSize += int64(file.UncompressedSize64)
		if totalSize > MaxTotalSize {
			return nil, ErrArchiveTooLarge
		}

		unique := uniqueName(sanitizeName(base), usedNames)
		entries = append(entries, SubtitleEntry{
			Index:      index,
			RawName:    file.Name,
			UniqueName: unique,
			Language:   DetectLanguage(file.Name),
			Size:       int64(file.UncompressedSize64),
		})
	}

	if len(entries) == 0 {
		return nil, ErrNoSubtitle
	}
	return entries, nil
}

// Select chooses the best matching subtitle entry. When hint is non-empty,
// only entries whose name contains hint (case-insensitive, on the sanitized
// base name) are eligible and ErrNoMatch is returned if none match. Otherwise
// entries are ranked and the first of the highest rank is returned:
//
//  1. entries whose detected language equals language (case-insensitive)
//  2. entries whose name contains the language code as a token
//  3. everything else
//
// Ties inside a rank keep the archive order, which makes the result
// deterministic. language may be "", in which case the first eligible entry is
// selected.
func Select(entries []SubtitleEntry, language, hint string) (SubtitleEntry, error) {
	if len(entries) == 0 {
		return SubtitleEntry{}, ErrNoMatch
	}

	candidates := entries
	if hint != "" {
		candidates = nil
		needle := strings.ToLower(hint)
		for _, entry := range entries {
			if strings.Contains(strings.ToLower(entry.UniqueName), needle) {
				candidates = append(candidates, entry)
			}
		}
		if len(candidates) == 0 {
			return SubtitleEntry{}, ErrNoMatch
		}
	}

	if language == "" {
		return candidates[0], nil
	}

	lang := strings.ToLower(language)
	for _, entry := range candidates {
		if strings.ToLower(entry.Language) == lang {
			return entry, nil
		}
	}
	for _, entry := range candidates {
		if containsToken(strings.ToLower(entry.UniqueName), lang) {
			return entry, nil
		}
	}

	// an explicit language was requested but nothing matches it
	return SubtitleEntry{}, ErrNoMatch
}

// ReadEntry decompresses one subtitle entry. The returned bytes are bounded by
// MaxEntrySize and a CRC mismatch is reported as ErrChecksum.
func ReadEntry(data []byte, entry SubtitleEntry) ([]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, ErrInvalidZip
	}
	if entry.Index < 0 || entry.Index >= len(reader.File) {
		return nil, ErrInvalidZip
	}

	file := reader.File[entry.Index]
	if file.FileInfo().IsDir() || file.Mode()&os.ModeType != 0 {
		return nil, ErrUnsafePath
	}
	if _, ok := safeBaseName(file.Name); !ok {
		return nil, ErrUnsafePath
	}
	if file.UncompressedSize64 > uint64(MaxEntrySize) {
		return nil, ErrEntryTooLarge
	}

	stream, err := file.Open()
	if err != nil {
		return nil, mapZipError(err)
	}

	// read at most MaxEntrySize + 1 byte so an entry whose declared size lied
	// is still caught.
	content, err := io.ReadAll(io.LimitReader(stream, MaxEntrySize+1))
	// close explicitly to verify the CRC-32 of the consumed stream; a
	// mismatch is surfaced as a dedicated, deterministic error.
	closeErr := stream.Close()
	if err != nil {
		return nil, mapZipError(err)
	}
	if closeErr != nil {
		return nil, mapZipError(closeErr)
	}
	if int64(len(content)) > MaxEntrySize {
		return nil, ErrEntryTooLarge
	}
	return content, nil
}

// WriteSubtitle writes content into dir under entry.UniqueName, applying the
// given collision policy. The final on-disk path is always inside dir: the
// entry name carries no directory component and the resolved path is checked
// against dir as defence in depth.
func WriteSubtitle(dir string, entry SubtitleEntry, content []byte, policy CollisionPolicy) (string, error) {
	if filepath.IsAbs(entry.UniqueName) || strings.ContainsAny(entry.UniqueName, `/\`) {
		return "", ErrUnsafePath
	}

	// resolve the final name according to the collision policy
	finalPath := filepath.Join(dir, entry.UniqueName)
	switch policy {
	case CollisionError:
		if _, err := os.Stat(finalPath); !os.IsNotExist(err) {
			if err == nil {
				return "", ErrFileExists
			}
			return "", err
		}

	case CollisionRename:
		if _, err := os.Stat(finalPath); err == nil {
			finalPath = filepath.Join(dir, resolveDiskCollision(dir, entry.UniqueName))
		} else if !os.IsNotExist(err) {
			return "", err
		}

	case CollisionOverwrite:
		// the requested name is used as-is
	}
	if !withinDir(dir, finalPath) {
		return "", ErrUnsafePath
	}

	// open the target with the right guarantee: O_TRUNC for overwrites,
	// O_EXCL otherwise so a file appearing between the stat check and the open
	// can never be silently replaced.
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if policy != CollisionOverwrite {
		flags = os.O_CREATE | os.O_WRONLY | os.O_EXCL
	}
	file, err := os.OpenFile(finalPath, flags, 0o644)
	if err != nil {
		if os.IsExist(err) && policy == CollisionRename {
			// raced with another writer: find the next free name and retry once
			finalPath = filepath.Join(dir, resolveDiskCollision(dir, filepath.Base(finalPath)))
			if !withinDir(dir, finalPath) {
				return "", ErrUnsafePath
			}
			file, err = os.OpenFile(finalPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
		}
		if err != nil {
			return "", err
		}
	}

	if _, err = file.Write(content); err != nil {
		file.Close()
		return "", err
	}
	if err = file.Close(); err != nil {
		return "", err
	}
	return finalPath, nil
}

// ExtractSubtitle is the shared one-call flow used by the CLI: select, read,
// write. It returns the written file path.
func ExtractSubtitle(data []byte, dir, language, hint string, policy CollisionPolicy) (string, error) {
	entries, err := ListSubtitles(data)
	if err != nil {
		return "", err
	}
	entry, err := Select(entries, language, hint)
	if err != nil {
		return "", err
	}
	content, err := ReadEntry(data, entry)
	if err != nil {
		return "", err
	}
	return WriteSubtitle(dir, entry, content, policy)
}

// safeBaseName validates an archive entry name and returns its sanitizable
// base name. It rejects absolute paths and any ".." traversal after normalizing
// both kinds of path separators.
func safeBaseName(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	normalized := strings.ReplaceAll(name, `\`, "/")
	if filepath.IsAbs(normalized) || strings.HasPrefix(normalized, "/") {
		return "", false
	}
	cleaned := filepath.ToSlash(filepath.Clean(normalized))
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	for _, part := range strings.Split(cleaned, "/") {
		if part == ".." {
			return "", false
		}
	}
	base := filepath.Base(normalized)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return "", false
	}
	return base, true
}

// sanitizeName turns an archive base name into a safe file name: path
// separators and control characters are replaced with "_", reserved Windows
// device names are neutralized and the result is length-capped.
func sanitizeName(name string) string {
	// normalize both separator flavors before taking the base name
	name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	var builder strings.Builder
	builder.Grow(len(name))
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f:
			builder.WriteByte('_')
		case strings.ContainsRune(`/\:*?"<>|`, r):
			builder.WriteByte('_')
		default:
			builder.WriteRune(r)
		}
	}
	result := strings.TrimSpace(builder.String())
	result = strings.TrimRight(result, ". ")
	if result == "" {
		result = "subtitle"
	}

	// neutralize reserved Windows device names (case-insensitive), e.g. "CON.srt"
	stem := strings.TrimSuffix(result, filepath.Ext(result))
	if isReservedDevice(stem) {
		result = "_" + result
	}

	// cap by rune count so a multi-byte character is never split
	runes := []rune(result)
	if len(runes) > maxNameLength {
		ext := filepath.Ext(result)
		result = string(runes[:maxNameLength]) + ext
	}
	return result
}

func isReservedDevice(stem string) bool {
	switch strings.ToUpper(stem) {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	}
	return false
}

// uniqueName guarantees no two entries of one archive produce the same output
// name by appending " (n)" before the extension.
func uniqueName(name string, used map[string]bool) string {
	if !used[name] {
		used[name] = true
		return name
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for counter := 1; ; counter++ {
		candidate := strings.Builder{}
		candidate.WriteString(stem)
		candidate.WriteString(" (")
		candidate.WriteString(strconv.Itoa(counter))
		candidate.WriteString(")")
		candidate.WriteString(ext)
		if !used[candidate.String()] {
			used[candidate.String()] = true
			return candidate.String()
		}
	}
}

// resolveDiskCollision finds the first free "name (n).ext" inside dir.
func resolveDiskCollision(dir, name string) string {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for counter := 1; ; counter++ {
		candidate := stem + " (" + strconv.Itoa(counter) + ")" + ext
		if _, err := os.Stat(filepath.Join(dir, candidate)); os.IsNotExist(err) {
			return candidate
		}
	}
}

func withinDir(dir, target string) bool {
	rel, err := filepath.Rel(dir, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) &&
		!filepath.IsAbs(rel)
}

func containsToken(text, token string) bool {
	tokens := strings.FieldsFunc(text, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	})
	for _, t := range tokens {
		if t == token {
			return true
		}
	}
	return false
}

func mapZipError(err error) error {
	if errors.Is(err, zip.ErrChecksum) || errors.Is(err, zip.ErrAlgorithm) {
		return ErrChecksum
	}
	return ErrInvalidZip
}
