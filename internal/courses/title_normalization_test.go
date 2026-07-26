package courses

import "testing"

func TestTitleNormalizerTransliteratesGenericTitles(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		changed bool
	}{
		{
			name:    "cooking for beginners",
			input:   "Kulinariya dlya novichkov",
			want:    "Кулинария для новичков",
			changed: true,
		},
		{
			name:    "programming from scratch",
			input:   "Programmirovanie s nulya",
			want:    "Программирование с нуля",
			changed: true,
		},
		{
			name:    "basic course from scratch",
			input:   "Bazovyiy kurs s nulya",
			want:    "Базовый курс с нуля",
			changed: true,
		},
		{
			name:    "ordinal and provider prefix",
			input:   "7.[Provider] Geometriya dlya novichkov",
			want:    "7.[Provider] Геометрия для новичков",
			changed: true,
		},
		{
			name:    "online hyphenated seminar",
			input:   "Onlayn-seminar «Upravlenie proektami»",
			want:    "Онлайн-семинар «Управление проектами»",
			changed: true,
		},
		{
			name:    "interior design with soft sign",
			input:   "Dekorirovanie v dizayne inter'era",
			want:    "Декорирование в дизайне интерьера",
			changed: true,
		},
		{
			name:    "technical phrase with dotted token",
			input:   "Razrabotka na react.js s nulya",
			want:    "Разработка на react.js с нуля",
			changed: true,
		},
		{
			name:  "domain",
			input: "site.name",
			want:  "site.name",
		},
		{
			name:  "archive file identifier",
			input: "archive_5881340_RUS_rutracker",
			want:  "archive_5881340_RUS_rutracker",
		},
		{
			name:  "already Cyrillic",
			input: "Профессия Архитектор ПО",
			want:  "Профессия Архитектор ПО",
		},
	}

	normalizer := newTitleNormalizer(nil)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, changed := normalizer.Normalize(test.input)
			if got != test.want {
				t.Fatalf("Normalize(%q) = %q, want %q", test.input, got, test.want)
			}
			if changed != test.changed {
				t.Fatalf("Normalize(%q) changed = %t, want %t", test.input, changed, test.changed)
			}
		})
	}
}

func TestTitleNormalizerUsesLoadedProtectionRules(t *testing.T) {
	rules := &TitleRules{
		protectedTokensCI:    map[string]struct{}{"nimbus": {}},
		protectedTokensExact: map[string]struct{}{"Vector": {}},
		protectedSubstringsCI: []string{
			"academy",
		},
	}
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "case insensitive token",
			input: "Kurs dlya Nimbus s nulya",
			want:  "Курс для Nimbus с нуля",
		},
		{
			name:  "exact case token",
			input: "Kurs dlya Vector s nulya",
			want:  "Курс для Vector с нуля",
		},
		{
			name:  "case insensitive substring",
			input: "Kurs dlya North Academy s nulya",
			want:  "Курс для North Academy с нуля",
		},
	}

	normalizer := newTitleNormalizer(rules)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, changed := normalizer.Normalize(test.input)
			if got != test.want {
				t.Fatalf("Normalize(%q) = %q, want %q", test.input, got, test.want)
			}
			if !changed {
				t.Fatalf("Normalize(%q) changed = false, want true", test.input)
			}
		})
	}
}

func TestTitleNormalizerProtectsNonTransliterationTokens(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "URL",
			input: "Kurs dlya https://example.com/ya-path s nulya",
			want:  "Курс для https://example.com/ya-path с нуля",
		},
		{
			name:  "email",
			input: "Kurs dlya user-name@example.com s nulya",
			want:  "Курс для user-name@example.com с нуля",
		},
		{
			name:  "domain",
			input: "Kurs dlya my-site.name s nulya",
			want:  "Курс для my-site.name с нуля",
		},
		{
			name:  "file identifier",
			input: "Kurs dlya archive_5881340_RUS_rutracker s nulya",
			want:  "Курс для archive_5881340_RUS_rutracker с нуля",
		},
		{
			name:  "digit-containing token",
			input: "Kurs dlya course2024 s nulya",
			want:  "Курс для course2024 с нуля",
		},
		{
			name:  "all capitals",
			input: "Kurs dlya PRO s nulya",
			want:  "Курс для PRO с нуля",
		},
		{
			name:  "internal mixed case",
			input: "Kurs dlya DataFrame s nulya",
			want:  "Курс для DataFrame с нуля",
		},
		{
			name:  "square bracketed Latin text",
			input: "Kurs dlya [English Name] s nulya",
			want:  "Курс для [English Name] с нуля",
		},
		{
			name:  "balanced parenthesized Latin text",
			input: "Kurs dlya (English Name) s nulya",
			want:  "Курс для (English Name) с нуля",
		},
	}

	normalizer := newTitleNormalizer(nil)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, changed := normalizer.Normalize(test.input)
			if got != test.want {
				t.Fatalf("Normalize(%q) = %q, want %q", test.input, got, test.want)
			}
			if !changed {
				t.Fatalf("Normalize(%q) changed = false, want true", test.input)
			}
		})
	}
}

func TestTitleNormalizerReviewRegressions(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		changed bool
	}{
		{
			name:    "forced transliteration leaves English context alone",
			input:   "English freymvork guide",
			want:    "English фреймворк guide",
			changed: true,
		},
		{
			name:    "forced c number do leaves English context alone",
			input:   "English c 0 do guide",
			want:    "English с 0 до guide",
			changed: true,
		},
		{
			name:  "marker scoring uses longest non-overlapping occurrences",
			input: "Maya Bay Kayak",
			want:  "Maya Bay Kayak",
		},
		{
			name:  "hyphenated digit-containing token is not a phrase",
			input: "c-course2024-do",
			want:  "c-course2024-do",
		},
		{
			name:    "single uppercase letter remains protected",
			input:   "R dlya data s nulya",
			want:    "R для дата с нуля",
			changed: true,
		},
		{
			name:    "terminal period is punctuation",
			input:   "Kurs dlya programmistov s nulya.",
			want:    "Курс для программистов с нуля.",
			changed: true,
		},
		{
			name:    "boundary apostrophes remain quotes",
			input:   "Kurs dlya 'interer' s nulya",
			want:    "Курс для 'интерер' с нуля",
			changed: true,
		},
		{
			name:    "apostrophe between ASCII letters stays in word",
			input:   "Kurs dlya inter'era",
			want:    "Курс для интерьера",
			changed: true,
		},
		{
			name:    "uppercase transliteration digraph",
			input:   "SHahmatyi s nulya",
			want:    "Шахматы с нуля",
			changed: true,
		},
	}

	normalizer := newTitleNormalizer(nil)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, changed := normalizer.Normalize(test.input)
			if got != test.want {
				t.Fatalf("Normalize(%q) = %q, want %q", test.input, got, test.want)
			}
			if changed != test.changed {
				t.Fatalf("Normalize(%q) changed = %t, want %t", test.input, changed, test.changed)
			}
		})
	}
}

func TestNormalizeCourseTitleUsesNilRules(t *testing.T) {
	got, changed := normalizeCourseTitle("Kurs dlya programmistov s nulya")
	if got != "Курс для программистов с нуля" {
		t.Fatalf("normalizeCourseTitle() = %q, want %q", got, "Курс для программистов с нуля")
	}
	if !changed {
		t.Fatal("normalizeCourseTitle() changed = false, want true")
	}
}
