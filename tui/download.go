// interactive TUI for file downloading

package tui

import (
	"fmt"
	"os"
	"subteez/archive"
	"subteez/messages"
	"subteez/subteez"

	"github.com/rivo/tview"
)

// must not be called from main loop and event handlers
func (c *Context) showDownloadDialog() {
	// show status dialog
	c.app.QueueUpdateDraw(func() {
		c.showStatusDialog(messages.DownloadingStatus)
	})

	// send request
	request := subteez.SubtitleDownloadRequest{
		ID: c.fileId,
	}
	name, data, err := c.client.Download(request)
	if err != nil {
		c.error = err
		c.app.Stop()
		return
	}

	// archives require selecting one of the packed subtitles; a plain
	// single-file download is written directly as before.
	if archive.IsZip(data) {
		c.handleArchiveDownload(data)
		return
	}

	c.writePlainFile(name, data)
}

// writePlainFile writes a non-archive download to disk and shows the result
// modal. Must not be called from main loop and event handlers.
func (c *Context) writePlainFile(name string, data []byte) {
	c.app.QueueUpdateDraw(func() {
		c.showStatusDialog(messages.WritingStatus)
	})

	file, err := os.Create(name)
	if err != nil {
		c.error = err
		c.app.Stop()
		return
	}
	defer file.Close()

	count, err := file.Write(data)
	if err != nil {
		c.error = err
		c.app.Stop()
		return
	}

	c.showResultModal(fmt.Sprintf(messages.FileWrittenDialogText, count, name))
}

// handleArchiveDownload lists the subtitles of a ZIP and lets the user pick
// one. Selection uses the same archive.Select logic as the CLI: the entry
// matching the requested language is pre-selected as the first list item.
// Must not be called from main loop and event handlers.
func (c *Context) handleArchiveDownload(data []byte) {
	entries, err := archive.ListSubtitles(data)
	if err != nil {
		c.showErrorModal(err)
		return
	}

	language := subteez.LanguageFromDownloadID(c.fileId)
	selectedIndex := 0
	if language != "" {
		// archive.Select implements the same language ranking as the CLI;
		// map the returned ZIP index back to its position in the filtered
		// entries slice so the list opens on the best match.
		if best, err := archive.Select(entries, language, ""); err == nil {
			for i, entry := range entries {
				if entry.Index == best.Index {
					selectedIndex = i
					break
				}
			}
		}
	}

	c.app.QueueUpdateDraw(func() {
		list := tview.NewList().SetSelectedFunc(func(index int, mainText, secondaryText string, shortcut rune) {
			entry := entries[index]
			go c.extractArchiveEntry(data, entry)
		})
		list.SetBorder(true).SetTitle(messages.SubtitleSelectTitle)

		for _, entry := range entries {
			languageTitle := entry.Language
			if languageTitle == "" {
				languageTitle = "-"
			}
			list.AddItem(entry.UniqueName, languageTitle, 0, nil)
		}
		list.SetCurrentItem(selectedIndex)

		header := tview.NewTextView().SetText(messages.SubtitleSelectHint)
		layout := tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(header, 1, 0, false).
			AddItem(list, 0, 1, true)
		c.pushView(layout)
	})
}

// extractArchiveEntry decompresses the chosen entry and writes it safely into
// the current directory. Must not be called from main loop and event handlers.
func (c *Context) extractArchiveEntry(data []byte, entry archive.SubtitleEntry) {
	c.app.QueueUpdateDraw(func() {
		c.showStatusDialog(messages.ExtractingStatus)
	})

	content, err := archive.ReadEntry(data, entry)
	if err != nil {
		c.showErrorModal(err)
		return
	}

	// never overwrite an existing file without the user noticing
	path, err := archive.WriteSubtitle(".", entry, content, archive.CollisionRename)
	if err != nil {
		c.showErrorModal(err)
		return
	}

	c.showResultModal(fmt.Sprintf(messages.SubtitleWrittenText, path))
}

// showResultModal presents a success message and returns to the previous view
// (or exits the application when the stack is exhausted).
func (c *Context) showResultModal(text string) {
	c.app.QueueUpdateDraw(func() {
		modal := tview.NewModal().
			SetText(text).
			AddButtons([]string{messages.ButtonOK}).
			SetDoneFunc(func(buttonIndex int, buttonLabel string) {
				if len(c.viewStack)-1 <= 0 {
					c.app.Stop()
					return
				}
				c.popView()
			})
		c.pushView(modal)
	})
}

// showErrorModal presents a recoverable error. It does not terminate the
// application: the user can go back to the previous view and retry.
func (c *Context) showErrorModal(err error) {
	c.app.QueueUpdateDraw(func() {
		modal := tview.NewModal().
			SetText(err.Error()).
			AddButtons([]string{messages.ButtonBack}).
			SetDoneFunc(func(buttonIndex int, buttonLabel string) {
				if !c.popView() {
					c.app.Stop()
				}
			})
		c.pushView(modal)
	})
}
