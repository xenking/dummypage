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

type courseTitleToken struct {
	text      string
	word      bool
	bracketed bool
	protected bool
}

type titleNormalizer struct {
	rules *TitleRules
}

func newTitleNormalizer(rules *TitleRules) titleNormalizer {
	return titleNormalizer{rules: cloneTitleRules(rules)}
}

func normalizeCourseTitle(title string) (string, bool) {
	return newTitleNormalizer(nil).Normalize(title)
}

func (normalizer titleNormalizer) Normalize(title string) (string, bool) {
	tokens := normalizer.tokenize(title)
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

func (normalizer titleNormalizer) tokenize(title string) []courseTitleToken {
	tokens := make([]courseTitleToken, 0, len(strings.Fields(title))*2)
	bracketDepth := 0
	parenDepth := 0

	for len(title) > 0 {
		r, size := utf8.DecodeRuneInString(title)
		if isCourseTitleWordRuneAt(title, 0) {
			end := size
			for end < len(title) {
				_, nextSize := utf8.DecodeRuneInString(title[end:])
				if !isCourseTitleWordRuneAt(title, end) {
					break
				}
				end += nextSize
			}
			text := title[:end]
			token := courseTitleToken{
				text:      text,
				word:      true,
				bracketed: bracketDepth > 0 || parenDepth > 0,
			}
			token.protected = normalizer.protectToken(token)
			tokens = append(tokens, token)
			title = title[end:]
			continue
		}

		if r == '[' {
			bracketDepth++
		} else if r == '(' {
			parenDepth++
		}
		tokens = append(tokens, courseTitleToken{text: title[:size], bracketed: bracketDepth > 0 || parenDepth > 0})
		if r == ']' && bracketDepth > 0 {
			bracketDepth--
		} else if r == ')' && parenDepth > 0 {
			parenDepth--
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

func isCourseTitleWordRuneAt(value string, offset int) bool {
	r, _ := utf8.DecodeRuneInString(value[offset:])
	if r == '\'' {
		if offset == 0 {
			return false
		}
		prev, _ := utf8.DecodeLastRuneInString(value[:offset])
		next, _ := utf8.DecodeRuneInString(value[offset+1:])
		return isASCIILetter(prev) && isASCIILetter(next)
	}
	return unicode.IsLetter(r) ||
		unicode.IsDigit(r) ||
		r == '_' ||
		r == '@' ||
		r == ':' ||
		r == '/'
}

func (normalizer titleNormalizer) protectToken(token courseTitleToken) bool {
	if token.bracketed {
		return true
	}

	lower := strings.ToLower(token.text)
	if normalizer.rules != nil {
		if _, protected := normalizer.rules.protectedTokensExact[token.text]; protected {
			return true
		}
		if _, protected := normalizer.rules.protectedTokensCI[lower]; protected {
			return true
		}
		for _, substring := range normalizer.rules.protectedSubstringsCI {
			if strings.Contains(lower, substring) {
				return true
			}
		}
	}
	if strings.ContainsAny(token.text, "0123456789_@/:") {
		return true
	}
	if startsWithASCIICapital(token.text) && containsEnglishCourseTitleMarker(lower) {
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
		score += scoreCourseTitleMarkers(lower)
		if strings.Contains(lower, "'") {
			score += 2
		}
		switch lower {
		case "v", "s", "na", "do", "iz", "po", "za", "c":
			score++
		}
		if containsEnglishCourseTitleMarker(lower) {
			score -= 3
		}
	}
	return score
}

func containsEnglishCourseTitleMarker(value string) bool {
	for _, marker := range []string{"th", "tion", "ing", "ment"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func scoreCourseTitleMarkers(value string) int {
	markers := []struct {
		text  string
		score int
	}{
		{"onlayn", 4},
		{"shch", 3},
		{"dlya", 4},
		{"nulya", 3},
		{"yih", 3},
		{"yiy", 3},
		{"zh", 3},
		{"kh", 3},
		{"sch", 1},
		{"aya", 1},
		{"iya", 1},
		{"ch", 1},
		{"sh", 1},
		{"ya", 1},
		{"yu", 1},
		{"yo", 1},
		{"ay", 1},
	}

	score := 0
	for index := 0; index < len(value); {
		bestText := ""
		bestScore := 0
		for _, marker := range markers {
			if strings.HasPrefix(value[index:], marker.text) && len(marker.text) > len(bestText) {
				bestText = marker.text
				bestScore = marker.score
			}
		}
		if bestText == "" {
			index++
			continue
		}
		score += bestScore
		index += len(bestText)
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
