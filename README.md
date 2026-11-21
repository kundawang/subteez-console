# Subteez Console
This is a console version of [Subteez App](https://play.google.com/store/apps/details?id=madamin.subtitles).
You can use it to search and download subtitles from [Subteez server](https://github.com/minusium/subteez-subscene-proxy), or write scripts to do automated tasks.

# Usage

```
Usage: subteez [-help | -tui] <Command> <Arguments>

Options:

    -help [command] : Show help message

    -tui            : Enable interactive TUI mode

    -script         : Enable script mode
                      Script mode disables log and formats output for using in scripts

Commands:

    config      : Change configuration

    search      : Search for movie or series titles

    files       : List all available subtitle files

    download    : Download a subtitle file

```

# Screenshots
* Interactive Mode:

![screenshot 1](assets/1.png)

![screenshot 2](assets/2.png)

![screenshot 3](assets/3.png)

![screenshot 4](assets/4.png)


# Examples:

* Use interactive TUI mode:
```
> subteez -tui search

> subteez -tui search arcane

```

* Search and download subtitles for "Arcane: League of Legends" series:
```
> subteez search arcane
Title: Arcane: League of Legends - First Season (366 files)
ID:    arcane-league-of-legends-first-season

> subteez files arcane-league-of-legends-first-season
ID:       2625728
Language: English
Title:    Arcane Season 01
Author:   NguyenBich

> subteez download arcane-league-of-legends-first-season English 2625728

00:00:00 requesting page "/subtitles/arcane-league-of-legends-first-season/english/2625728" from server

00:00:04 subtitle "arcane-league-of-legends-first-season_HI_english-2625728.srt" extracted from archive and saved

```

### Archive extraction

Downloads are often multi-language ZIP packs. When a download is detected as a
ZIP, Subteez Console lists the SRT/VTT/ASS files inside it and extracts the one
matching the requested language automatically; a plain, single-file download is
saved unchanged.

* When an archive contains several subtitles of the same language, an optional
name hint selects among them:

```
> subteez download arcane-league-of-legends-first-season English 2625728 forced

```

* In TUI mode the packed subtitles are shown in a selection list and the best
language match is pre-selected.
* Extracted files use a sanitized base name (archive directory components and
unsafe paths are never written outside the current directory). If the target
name already exists, the file is saved as `name (1).ext` instead of being
overwritten.
* Archives that exceed the built-in size/entry limits, fail checksum
verification, contain no supported subtitle or no matching language fail with a
specific error.


* Set tui mode to be enabled without `-tui` option:
```
> subteez config set tui true

```

* Print subtitle file content to console. For archived downloads the selected
subtitle (not the raw ZIP) is streamed to stdout:
```
> subteez -script download arcane-league-of-legends-first-season en 2625728
1
00:00:19,375 --> 00:00:23,125
[foreboding music plays]

2
00:00:29,000 --> 00:00:32,375
[woman vocalizing]

3
00:00:39,541 --> 00:00:42,250
[water gurgles]

4
00:00:43,250 --> 00:00:45,458
[Silco] <i>Ever wonder
what it's like to drown?</i>

```
