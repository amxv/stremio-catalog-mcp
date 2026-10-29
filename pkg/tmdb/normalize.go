package tmdb

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/longbridgeapp/opencc"
	"golang.org/x/text/unicode/norm"
)

var (
	qualifierSuffix = regexp.MustCompile(`\s*\(([^()]*)\)\s*$`)
	chineseOnce     sync.Once
	chineseFold     *opencc.OpenCC
)

type titleQuery struct {
	Name    string
	Year    int
	Country string
	Kind    string
	Anime   bool
}

// Parse only explicit trailing qualifiers. Parentheses inside a real title,
// such as (500) Days of Summer, are untouched.
func parseTitleQuery(raw string) titleQuery {
	spec := titleQuery{Name: strings.TrimSpace(raw)}
	for {
		sub := qualifierSuffix.FindStringSubmatchIndex(spec.Name)
		if sub == nil {
			break
		}
		qualifier := strings.TrimSpace(spec.Name[sub[2]:sub[3]])
		recognized := true
		switch strings.ToLower(qualifier) {
		case "us", "usa", "american":
			spec.Country = "US"
		case "uk", "british", "gb":
			spec.Country = "GB"
		case "movie", "film":
			spec.Kind = "movie"
		case "tv", "series", "show", "tv series":
			spec.Kind = "tv"
		case "anime":
			spec.Anime = true
			// Anime includes both animated films and television series.
		default:
			if len(qualifier) == 4 && qualifier[0] >= '1' && qualifier[0] <= '2' {
				year, err := strconv.Atoi(qualifier)
				if err == nil && year >= 1870 && year <= 2100 {
					spec.Year = year
				} else {
					recognized = false
				}
			} else {
				recognized = false
			}
		}
		if !recognized {
			break
		}
		spec.Name = strings.TrimSpace(spec.Name[:sub[0]])
	}
	return spec
}

func simplifyChinese(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
		return s
	}
	chineseOnce.Do(func() {
		chineseFold, _ = opencc.New("t2s")
	})
	if chineseFold == nil {
		return s
	}
	converted, err := chineseFold.Convert(s)
	if err != nil {
		return s
	}
	return converted
}

// titleWords is a shared Unicode-aware canonicalization pipeline. Decompose
// diacritics, fold width/case, convert traditional Chinese to simplified, and
// use punctuation as word boundaries. No edit distance or spellcheck.
func titleWords(s string) []string {
	s = norm.NFKD.String(simplifyChinese(s))
	var words []string
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		word := b.String()
		switch word {
		case "one":
			word = "1"
		case "two":
			word = "2"
		case "three":
			word = "3"
		case "four":
			word = "4"
		case "five":
			word = "5"
		case "six":
			word = "6"
		case "seven":
			word = "7"
		case "eight":
			word = "8"
		case "nine":
			word = "9"
		case "ten":
			word = "10"
		case "eleven":
			word = "11"
		case "twelve":
			word = "12"
		}
		words = append(words, word)
		b.Reset()
	}
	for _, r := range s {
		if unicode.IsMark(r) {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		} else {
			flush()
		}
	}
	flush()
	return words
}

func normalize(s string) string  { return strings.Join(titleWords(s), "") }
func sameTitle(a, b string) bool { return normalize(a) == normalize(b) }

func relaxedKey(s string) string {
	words := titleWords(s)
	if len(words) > 1 && (words[0] == "the" || words[0] == "a" || words[0] == "an") {
		words = words[1:]
	}
	for i, w := range words {
		// Transliteration commonly spells long A as "aa" (Baahubali),
		// while macrons decompose to a single A.
		words[i] = strings.ReplaceAll(w, "aa", "a")
	}
	return strings.Join(words, "")
}

func shortTitle(query, candidate string) bool {
	needle, full := titleWords(query), titleWords(candidate)
	// Do not call single-word prefixes exact matches: Alien is not Alien³,
	// and a standalone title must not pull unrelated sequels into a result.
	if len(needle) < 2 || len(needle) >= len(full) {
		return false
	}
	for i, word := range needle {
		if full[i] != word {
			return false
		}
	}
	return true
}
