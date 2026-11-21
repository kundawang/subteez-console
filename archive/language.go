// Language detection from archive entry names.
//
// Subtitle packs encode the language in the file name in many different ways:
// "Movie.en.srt", "Movie.english.srt", "Movie [eng].srt", "Movie_es-spa.srt"...
// DetectLanguage normalizes those variants to the two-letter codes used by the
// rest of the application.

package archive

import "strings"

// languageAliases maps every accepted token (lower-case) to its canonical
// two-letter code. Both ISO 639-1 ("en") and ISO 639-2 ("eng") codes and the
// English endonyms of the languages supported by the server are recognized.
var languageAliases = map[string]string{
	"en":      "en",
	"eng":     "en",
	"english": "en",

	"fa":      "fa",
	"fas":     "fa",
	"per":     "fa",
	"persian": "fa",
	"farsi":   "fa",

	"ar":     "ar",
	"ara":    "ar",
	"arabic": "ar",

	"hi":    "hi",
	"hin":   "hi",
	"hindi": "hi",

	"de":      "de",
	"ger":     "de",
	"deu":     "de",
	"german":  "de",
	"deutsch": "de",

	"fr":       "fr",
	"fra":      "fr",
	"fre":      "fr",
	"french":   "fr",
	"francais": "fr",

	"it":       "it",
	"ita":      "it",
	"italian":  "it",
	"italiano": "it",

	"pl":     "pl",
	"pol":    "pl",
	"polish": "pl",

	"ru":      "ru",
	"rus":     "ru",
	"russian": "ru",

	"es":      "es",
	"spa":     "es",
	"spanish": "es",
	"espanol": "es",

	"tr":      "tr",
	"tur":     "tr",
	"turkish": "tr",
	"turkce":  "tr",
}

// knownTags is the set of language tags matched at the beginning of a token,
// longest first, so that "english" wins over "en" and "eng" before it.
var knownTags = func() []string {
	tags := make([]string, 0, len(languageAliases))
	for tag := range languageAliases {
		tags = append(tags, tag)
	}
	// insertion-sort by descending length; the list is tiny and built once.
	for i := 1; i < len(tags); i++ {
		for j := i; j > 0 && len(tags[j]) > len(tags[j-1]); j-- {
			tags[j], tags[j-1] = tags[j-1], tags[j]
		}
	}
	return tags
}()

// DetectLanguage inspects the tokens of an entry name and returns the canonical
// code of the first language token found, or "" when none is recognized.
//
// Tokens are split on every non-alphanumeric character, so "Movie_es-spa.srt"
// yields ["movie", "es", "spa", "srt"]. A token is also accepted when it
// *starts* with a known tag followed by a recognized subtitle qualifier
// ("enforced", "enfor", "ensdh", "engsub"...), which covers glued tags without
// matching unrelated words like "energy".
func DetectLanguage(name string) string {
	base := strings.ToLower(name)
	for _, token := range strings.FieldsFunc(base, isTokenSeparator) {
		if code, ok := languageAliases[token]; ok {
			return code
		}
		for _, tag := range knownTags {
			if !strings.HasPrefix(token, tag) || len(token) == len(tag) {
				continue
			}
			rest := token[len(tag):]
			if isQualifier(rest) {
				return languageAliases[tag]
			}
		}
	}
	return ""
}

// isQualifier reports whether the part of a token following a language tag is a
// known subtitle qualifier rather than an unrelated word.
func isQualifier(rest string) bool {
	switch rest {
	case "forced", "for", "srt", "vtt", "ass", "sub", "subs",
		"subtitle", "subtitles", "sdh", "cc", "hi":
		return true
	}
	return false
}

func isTokenSeparator(r rune) bool {
	return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
}
