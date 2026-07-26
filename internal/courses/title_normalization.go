package courses

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mxmCherry/translit"
	"golang.org/x/text/transform"
)

var courseTitleReverseTranslit = translit.Map(map[string]string{
	"shch": "щ",
	"sch":  "щ",
	"yiy":  "ый",
	"yih":  "ых",
	"iya":  "ия",
	"aya":  "ая",
	"yo":   "ё",
	"zh":   "ж",
	"kh":   "х",
	"ts":   "ц",
	"ch":   "ч",
	"sh":   "ш",
	"yu":   "ю",
	"ya":   "я",
	"ey":   "ей",
	"ay":   "ай",
	"yi":   "ы",
	"iy":   "ий",
	"a":    "а",
	"b":    "б",
	"c":    "с",
	"d":    "д",
	"e":    "е",
	"f":    "ф",
	"g":    "г",
	"h":    "х",
	"i":    "и",
	"j":    "дж",
	"k":    "к",
	"l":    "л",
	"m":    "м",
	"n":    "н",
	"o":    "о",
	"p":    "п",
	"q":    "к",
	"r":    "р",
	"s":    "с",
	"t":    "т",
	"u":    "у",
	"v":    "в",
	"w":    "в",
	"x":    "кс",
	"y":    "ы",
	"z":    "з",
	"'":    "ь",
})

var protectedCourseTitleTokens = map[string]struct{}{
	"adobe":      {},
	"ableton":    {},
	"autodesk":   {},
	"live":       {},
	"javascript": {},
	"maya":       {},
	"python":     {},
	"react.js":   {},
	"skillbox":   {},
	"photoshop":  {},
}

var protectedCourseTitleExactTokens = map[string]struct{}{
	"Data":      {},
	"Science":   {},
	"Instagram": {},
	"Unity":     {},
	"Blender":   {},
	"Django":    {},
	"Pro":       {},
}

type courseTitleToken struct {
	text      string
	word      bool
	bracketed bool
	protected bool
}

func normalizeCourseTitle(title string) (string, bool) {
	tokens := tokenizeCourseTitle(title)
	forcedTokens := forcedCourseTitleTokens(tokens)
	score := scoreCourseTitle(tokens, forcedTokens)
	normalizeWholeTitle := score >= 4
	if !normalizeWholeTitle && len(forcedTokens) == 0 {
		return title, false
	}

	var normalized strings.Builder
	normalized.Grow(len(title))
	for index, token := range tokens {
		if !normalizeWholeTitle && !forcedTokens[index] {
			normalized.WriteString(token.text)
			continue
		}
		if !token.word || token.protected || !isLatinCourseTitleToken(token.text) {
			normalized.WriteString(token.text)
			continue
		}

		value, _, err := transform.String(
			courseTitleReverseTranslit.Transformer(),
			strings.ToLower(token.text),
		)
		if err != nil {
			return title, false
		}
		if startsWithASCIICapital(token.text) {
			first, size := utf8.DecodeRuneInString(value)
			value = string(unicode.ToUpper(first)) + value[size:]
		}
		normalized.WriteString(value)
	}

	result := normalized.String()
	return result, result != title
}

func tokenizeCourseTitle(title string) []courseTitleToken {
	tokens := make([]courseTitleToken, 0, len(strings.Fields(title))*2)
	bracketDepth := 0

	for len(title) > 0 {
		r, size := utf8.DecodeRuneInString(title)
		if isCourseTitleWordRune(r) {
			end := size
			for end < len(title) {
				next, nextSize := utf8.DecodeRuneInString(title[end:])
				if !isCourseTitleWordRune(next) {
					break
				}
				end += nextSize
			}
			text := title[:end]
			token := courseTitleToken{
				text:      text,
				word:      true,
				bracketed: bracketDepth > 0,
			}
			token.protected = protectCourseTitleToken(token)
			tokens = append(tokens, token)
			title = title[end:]
			continue
		}

		if r == '[' {
			bracketDepth++
		}
		tokens = append(tokens, courseTitleToken{text: title[:size], bracketed: bracketDepth > 0})
		if r == ']' && bracketDepth > 0 {
			bracketDepth--
		}
		title = title[size:]
	}

	protectNetworkCourseTitleFields(tokens)
	return tokens
}

func protectNetworkCourseTitleFields(tokens []courseTitleToken) {
	for start := 0; start < len(tokens); {
		end := start
		var field strings.Builder
		for end < len(tokens) && !courseTitleTokenIsSpace(tokens[end]) {
			field.WriteString(tokens[end].text)
			end++
		}

		value := field.String()
		if strings.Contains(value, "://") ||
			strings.Contains(value, "@") ||
			looksLikeCourseTitleDomain(value) {
			for index := start; index < end; index++ {
				if tokens[index].word {
					tokens[index].protected = true
				}
			}
		}

		start = end + 1
	}
}

func looksLikeCourseTitleDomain(value string) bool {
	value = strings.Trim(value, `"'()[]{}<>,;:!?`)
	labels := strings.Split(value, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if label == "" ||
			!isASCIIAlphaNumeric(rune(label[0])) ||
			!isASCIIAlphaNumeric(rune(label[len(label)-1])) {
			return false
		}
		for _, r := range label {
			if !isASCIIAlphaNumeric(r) && r != '-' {
				return false
			}
		}
	}
	return true
}

func courseTitleTokenIsSpace(token courseTitleToken) bool {
	r, _ := utf8.DecodeRuneInString(token.text)
	return !token.word && unicode.IsSpace(r)
}

func isCourseTitleWordRune(r rune) bool {
	return unicode.IsLetter(r) ||
		unicode.IsDigit(r) ||
		r == '\'' ||
		r == '_' ||
		r == '@' ||
		r == ':' ||
		r == '/'
}

func protectCourseTitleToken(token courseTitleToken) bool {
	if token.bracketed {
		return true
	}

	lower := strings.ToLower(token.text)
	if _, protected := protectedCourseTitleExactTokens[token.text]; protected {
		return true
	}
	if _, protected := protectedCourseTitleTokens[lower]; protected {
		return true
	}
	if strings.Contains(lower, "school") {
		return true
	}
	if strings.ContainsAny(token.text, "0123456789_@/:") {
		return true
	}

	letters := 0
	upper := 0
	allowUpperSHPrefix := len(token.text) > 2 && strings.HasPrefix(token.text, "SH")
	for index, r := range token.text {
		if !isASCIILetter(r) {
			continue
		}
		letters++
		if r >= 'A' && r <= 'Z' {
			upper++
			if index > 0 && !(index == 1 && allowUpperSHPrefix) {
				return true
			}
		}
	}
	return letters > 0 && upper == letters
}

func scoreCourseTitle(tokens []courseTitleToken, forcedTokens map[int]bool) int {
	score := 0
	for index, token := range tokens {
		if forcedTokens[index] {
			continue
		}
		if !token.word || token.protected || !isLatinCourseTitleToken(token.text) {
			continue
		}

		lower := strings.ToLower(token.text)
		for _, marker := range []string{
			"shch", "zh", "kh", "yih", "yiy", "dlya", "nulya",
		} {
			if strings.Contains(lower, marker) {
				score += 3
			}
		}
		if strings.Contains(lower, "sch") {
			score++
		} else {
			for _, marker := range []string{"ch", "sh"} {
				if strings.Contains(lower, marker) {
					score++
				}
			}
		}
		for _, marker := range []string{
			"ya", "yu", "yo", "ay", "aya", "iya",
		} {
			if strings.Contains(lower, marker) {
				score++
			}
		}
		if strings.Contains(lower, "'") {
			score += 2
		}
		switch lower {
		case "v", "s", "na", "do", "iz", "po", "za", "c":
			score++
		}
		for _, marker := range []string{"th", "tion", "ing", "ment"} {
			if strings.Contains(lower, marker) {
				score -= 3
			}
		}
	}
	return score
}

func forcedCourseTitleTokens(tokens []courseTitleToken) map[int]bool {
	forced := make(map[int]bool)
	for index := range tokens {
		if tokens[index].word &&
			!tokens[index].protected &&
			strings.EqualFold(tokens[index].text, "freymvork") {
			forced[index] = true
		}
	}

	for index := range tokens {
		if !tokens[index].word ||
			tokens[index].protected ||
			!strings.EqualFold(tokens[index].text, "c") {
			continue
		}
		number, ok := nextWhitespaceSeparatedCourseTitleWord(tokens, index)
		if !ok || !isASCIIDigits(tokens[number].text) {
			continue
		}
		last, ok := nextWhitespaceSeparatedCourseTitleWord(tokens, number)
		if !ok ||
			tokens[last].protected ||
			!strings.EqualFold(tokens[last].text, "do") {
			continue
		}
		forced[index] = true
		forced[last] = true
	}
	return forced
}

func nextWhitespaceSeparatedCourseTitleWord(tokens []courseTitleToken, index int) (int, bool) {
	index++
	if index >= len(tokens) || !courseTitleTokenIsSpace(tokens[index]) {
		return 0, false
	}
	for index < len(tokens) && courseTitleTokenIsSpace(tokens[index]) {
		index++
	}
	return index, index < len(tokens) && tokens[index].word
}

func isLatinCourseTitleToken(token string) bool {
	hasLetter := false
	for _, r := range token {
		switch {
		case isASCIILetter(r):
			hasLetter = true
		case r != '\'':
			return false
		}
	}
	return hasLetter
}

func startsWithASCIICapital(value string) bool {
	return len(value) > 0 && value[0] >= 'A' && value[0] <= 'Z'
}

func isASCIILetter(r rune) bool {
	return r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z'
}

func isASCIIAlphaNumeric(r rune) bool {
	return isASCIILetter(r) || r >= '0' && r <= '9'
}

func isASCIIDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
