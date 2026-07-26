package courses

import (
	"html"
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
	text             string
	word             bool
	bracketed        bool
	protected        bool
	forceOverridable bool
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
	original := title
	title = normalizer.cleanup(title)
	tokens := normalizer.tokenize(title)
	normalizeWholeTitle := scoreCourseTitle(tokens) >= 4 ||
		normalizer.forceNormalizeWholeTitle(tokens)
	if !normalizeWholeTitle {
		return title, title != original
	}

	var normalized strings.Builder
	normalized.Grow(len(title))
	for _, token := range tokens {
		forcedToken := normalizer.isForcedToken(token)
		if !token.word ||
			(token.protected && !(token.forceOverridable && forcedToken)) ||
			!isLatinCourseTitleToken(token.text) {
			normalized.WriteString(token.text)
			continue
		}

		value, _, err := transform.String(
			courseTitleReverseTranslit.Transformer(),
			strings.ToLower(token.text),
		)
		if err != nil {
			return original, false
		}
		if startsWithASCIICapital(token.text) {
			first, size := utf8.DecodeRuneInString(value)
			value = string(unicode.ToUpper(first)) + value[size:]
		}
		normalized.WriteString(value)
	}

	result := normalized.String()
	return result, result != original
}

func (normalizer titleNormalizer) cleanup(title string) string {
	if normalizer.rules == nil {
		return title
	}
	original := title
	cleanup := normalizer.rules.structuralCleanup
	if cleanup.decodeHTMLEntities {
		title = unescapeStrictCourseTitleEntities(title)
	}
	if cleanup.dropZeroWidthFormatChars {
		title = strings.Map(func(r rune) rune {
			if unicode.Is(unicode.Cf, r) {
				return -1
			}
			return r
		}, title)
	}
	if cleanup.stripLeadingProviderNoise {
		title = stripLeadingProviderNoise(title)
	}
	if cleanup.underscoresAsSpaces {
		title = cleanupCourseTitleUnderscores(title)
	}
	if !hasVisibleCourseTitleContent(title) {
		return original
	}
	return title
}

func unescapeStrictCourseTitleEntities(title string) string {
	if !strings.Contains(title, "&") {
		return title
	}

	var decoded strings.Builder
	decoded.Grow(len(title))
	for offset := 0; offset < len(title); {
		relativeAmpersand := strings.IndexByte(title[offset:], '&')
		if relativeAmpersand < 0 {
			decoded.WriteString(title[offset:])
			break
		}

		ampersand := offset + relativeAmpersand
		decoded.WriteString(title[offset:ampersand])
		end, ok := strictCourseTitleEntityEnd(title, ampersand)
		if !ok {
			decoded.WriteByte('&')
			offset = ampersand + 1
			continue
		}
		decoded.WriteString(html.UnescapeString(title[ampersand:end]))
		offset = end
	}
	return decoded.String()
}

func strictCourseTitleEntityEnd(title string, ampersand int) (int, bool) {
	index := ampersand + 1
	if index >= len(title) {
		return 0, false
	}

	if title[index] == '#' {
		index++
		if index < len(title) && (title[index] == 'x' || title[index] == 'X') {
			index++
			digitsStart := index
			for index < len(title) && isASCIIHexDigit(title[index]) {
				index++
			}
			return index + 1, index > digitsStart && index < len(title) && title[index] == ';'
		}

		digitsStart := index
		for index < len(title) && title[index] >= '0' && title[index] <= '9' {
			index++
		}
		return index + 1, index > digitsStart && index < len(title) && title[index] == ';'
	}

	if !isASCIILetter(rune(title[index])) {
		return 0, false
	}
	index++
	for index < len(title) && isASCIIAlphaNumeric(rune(title[index])) {
		index++
	}
	return index + 1, index < len(title) && title[index] == ';'
}

func isASCIIHexDigit(value byte) bool {
	return value >= '0' && value <= '9' ||
		value >= 'a' && value <= 'f' ||
		value >= 'A' && value <= 'F'
}

func cleanupCourseTitleUnderscores(title string) string {
	if !strings.Contains(title, "_") {
		return title
	}

	embedded := len(strings.Fields(title)) > 1
	var cleaned strings.Builder
	cleaned.Grow(len(title))
	for offset := 0; offset < len(title); {
		r, size := utf8.DecodeRuneInString(title[offset:])
		if unicode.IsSpace(r) {
			cleaned.WriteString(title[offset : offset+size])
			offset += size
			continue
		}

		end := offset + size
		for end < len(title) {
			r, size = utf8.DecodeRuneInString(title[end:])
			if unicode.IsSpace(r) {
				break
			}
			end += size
		}
		field := title[offset:end]
		if preserveCourseTitleUnderscores(field, embedded) {
			cleaned.WriteString(field)
		} else {
			cleaned.WriteString(replaceCourseTitleFieldUnderscores(field))
		}
		offset = end
	}
	return cleaned.String()
}

func replaceCourseTitleFieldUnderscores(field string) string {
	var cleaned strings.Builder
	cleaned.Grow(len(field))
	for offset := 0; offset < len(field); {
		if field[offset] != '_' {
			_, size := utf8.DecodeRuneInString(field[offset:])
			cleaned.WriteString(field[offset : offset+size])
			offset += size
			continue
		}
		cleaned.WriteByte(' ')
		for offset < len(field) && field[offset] == '_' {
			offset++
		}
	}
	return cleaned.String()
}

func preserveCourseTitleUnderscores(field string, embedded bool) bool {
	if !strings.Contains(field, "_") {
		return false
	}
	if strings.Contains(field, "://") ||
		strings.Contains(field, "@") ||
		strings.ContainsAny(field, `/\`) ||
		looksLikeCourseTitleDomain(strings.ReplaceAll(field, "_", "-")) {
		return true
	}
	if !embedded {
		return false
	}
	if strings.ContainsAny(field, "0123456789") {
		return true
	}
	for _, part := range strings.Split(field, "_") {
		if len(part) > 1 && isASCIIUppercaseIdentifierPart(part) {
			return true
		}
	}
	return false
}

func isASCIIUppercaseIdentifierPart(value string) bool {
	hasLetter := false
	for _, r := range value {
		switch {
		case r >= 'A' && r <= 'Z':
			hasLetter = true
		case r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return hasLetter
}

func hasVisibleCourseTitleContent(title string) bool {
	for _, r := range title {
		if !unicode.IsSpace(r) && !unicode.Is(unicode.Cf, r) {
			return true
		}
	}
	return false
}

func stripLeadingProviderNoise(title string) string {
	if hasPairedOuterCourseTitleQuotes(title) {
		return title
	}

	offset := 0
	removed := false
	for offset < len(title) {
		r, size := utf8.DecodeRuneInString(title[offset:])
		if !isCourseTitleQuote(r) {
			break
		}
		offset += size
		removed = true
	}

	ordinalStart := offset
	for offset < len(title) && title[offset] >= '0' && title[offset] <= '9' {
		offset++
	}
	if offset > ordinalStart && offset < len(title) && title[offset] == '.' {
		offset++
		removed = true
	} else {
		offset = ordinalStart
	}
	if !removed ||
		offset >= len(title) ||
		title[offset] != '[' {
		return title
	}
	closeOffset := strings.IndexByte(title[offset+1:], ']')
	if closeOffset <= 0 {
		return title
	}
	return title[offset:]
}

func hasPairedOuterCourseTitleQuotes(title string) bool {
	first, firstSize := utf8.DecodeRuneInString(title)
	last, lastSize := utf8.DecodeLastRuneInString(title)
	if firstSize == 0 || lastSize == 0 || len(title) <= firstSize+lastSize {
		return false
	}

	switch first {
	case '\'', '"':
		return last == first
	case '‘':
		return last == '’'
	case '“':
		return last == '”'
	case '„':
		return last == '“' || last == '”'
	case '«':
		return last == '»'
	default:
		return false
	}
}

func isCourseTitleQuote(r rune) bool {
	switch r {
	case '\'', '"', '‘', '’', '“', '”', '„', '«', '»':
		return true
	default:
		return false
	}
}

func (normalizer titleNormalizer) forceNormalizeWholeTitle(tokens []courseTitleToken) bool {
	if normalizer.rules == nil {
		return false
	}
	for _, token := range tokens {
		if normalizer.isForcedToken(token) &&
			(!token.protected || token.forceOverridable) {
			return true
		}
	}
	for _, phrase := range normalizer.rules.forceNormalizePhrasesCI {
		if courseTitleContainsPhrase(tokens, phrase) {
			return true
		}
	}
	return false
}

func (normalizer titleNormalizer) isForcedToken(token courseTitleToken) bool {
	if normalizer.rules == nil || !token.word {
		return false
	}
	_, forced := normalizer.rules.forceNormalizeTokensCI[strings.ToLower(token.text)]
	return forced
}

func courseTitleContainsPhrase(tokens []courseTitleToken, phrase string) bool {
	words := strings.Fields(phrase)
	if len(words) == 0 {
		return false
	}
	for start, token := range tokens {
		if !token.word || token.protected || !strings.EqualFold(token.text, words[0]) {
			continue
		}
		index := start
		matches := true
		for _, word := range words[1:] {
			var ok bool
			index, ok = nextWhitespaceSeparatedCourseTitleWord(tokens, index)
			if !ok || tokens[index].protected || !strings.EqualFold(tokens[index].text, word) {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
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
			token.protected, token.forceOverridable = normalizer.protectToken(token)
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
					tokens[index].forceOverridable = false
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

func (normalizer titleNormalizer) protectToken(token courseTitleToken) (protected, forceOverridable bool) {
	if token.bracketed {
		return true, false
	}

	lower := strings.ToLower(token.text)
	if normalizer.rules != nil {
		if _, protected := normalizer.rules.protectedTokensExact[token.text]; protected {
			return true, false
		}
		if _, protected := normalizer.rules.protectedTokensCI[lower]; protected {
			return true, false
		}
		for _, substring := range normalizer.rules.protectedSubstringsCI {
			if strings.Contains(lower, substring) {
				return true, false
			}
		}
	}
	if strings.ContainsAny(token.text, "0123456789_@/:") {
		return true, false
	}
	if startsWithASCIICapital(token.text) && containsEnglishCourseTitleMarker(lower) {
		return true, true
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
				return true, true
			}
		}
	}
	if letters > 0 && upper == letters {
		return true, true
	}
	return false, false
}

func scoreCourseTitle(tokens []courseTitleToken) int {
	score := 0
	for _, token := range tokens {
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
