// main of "download" command

package download_command

import (
	"fmt"
	"log"
	"os"
	"strings"
	"subteez/archive"
	"subteez/config"
	"subteez/errors"
	"subteez/messages"
	"subteez/subteez"
	"subteez/tui"
)

func Main(args []string, cfg config.Config) error {
	if len(args) < 2 {
		return errors.ErrNotEnoughArguments
	}

	id := args[1]
	nameHint := ""

	// generate full address, if given address is not a full address
	if !strings.HasPrefix(id, "/subtitles/") {
		if len(args) < 4 {
			return errors.ErrNotEnoughArguments
		}
		language, err := subteez.ParseLanguage(args[2])
		if err != nil {
			return err
		}
		languageDownloadPathPart, err := language.GetDownloadPathPart()
		if err != nil {
			return err
		}
		id = fmt.Sprintf("/subtitles/%s/%s/%s", args[1], languageDownloadPathPart, args[3])

		// optional 5th argument: a name hint used to disambiguate archives
		// containing several subtitles of the requested language
		if len(args) >= 5 {
			nameHint = args[4]
		}
	} else if len(args) >= 3 {
		// full-id form: any extra positional argument is treated as a hint
		nameHint = args[2]
	}

	// run interactive mode if it's enabled
	client := subteez.NewClient(cfg.GetServer())
	if cfg.IsInteractive() {
		context := tui.Context{}
		context.Initialize(cfg)
		go context.NavigateToDownload(id)
		return context.Run()
	}

	if !cfg.IsScriptMode() {
		log.Printf(messages.GettingPage, id)
	}

	// send request
	request := subteez.SubtitleDownloadRequest{
		ID: id,
	}
	name, data, err := client.Download(request)
	if err != nil {
		return err
	}

	// a ZIP archive is unpacked and the subtitle matching the requested
	// language (or the optional name hint) is extracted; plain downloads keep
	// their original single-file behavior.
	if archive.IsZip(data) {
		return handleArchive(cfg, data, id, nameHint)
	}

	// write downloaded file directly to stdout if script mode is enabled
	if cfg.IsScriptMode() {
		_, err := os.Stdout.Write(data)
		return err
	}

	// write downloaded file to disk
	file, err := os.Create(name)
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err = file.Write(data); err != nil {
		return err
	}
	log.Printf(messages.FileWritten, name)

	return nil
}

// handleArchive extracts the matching subtitle out of a downloaded ZIP. In
// script mode the subtitle content is streamed to stdout (never the raw ZIP);
// otherwise a safe, unique file is written into the current directory.
func handleArchive(cfg config.Config, data []byte, id, nameHint string) error {
	language := subteez.LanguageFromDownloadID(id)

	if cfg.IsScriptMode() {
		entries, err := archive.ListSubtitles(data)
		if err != nil {
			return err
		}
		entry, err := archive.Select(entries, language, nameHint)
		if err != nil {
			return err
		}
		content, err := archive.ReadEntry(data, entry)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(content)
		return err
	}

	// existing files are never overwritten silently: a unique "name (n).ext"
	// is generated instead (CollisionRename).
	path, err := archive.ExtractSubtitle(data, ".", language, nameHint, archive.CollisionRename)
	if err != nil {
		return err
	}
	log.Printf(messages.SubtitleExtracted, path)
	return nil
}
