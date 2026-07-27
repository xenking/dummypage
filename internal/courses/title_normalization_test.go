package courses

import (
	"strings"
	"testing"
)

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
			input: "fixture_2468135_LANG_bundle",
			want:  "fixture_2468135_LANG_bundle",
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
		forceNormalizeTokensCI: map[string]struct{}{},
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
			input: "Kurs dlya fixture_2468135_LANG_bundle s nulya",
			want:  "Курс для fixture_2468135_LANG_bundle с нуля",
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

func TestTitleNormalizerHasNoHardcodedForceRules(t *testing.T) {
	input := "Neutral klyuchmarker guide"
	got, changed := newTitleNormalizer(nil).Normalize(input)
	if got != input || changed {
		t.Fatalf("Normalize(%q) = (%q, %t), want unchanged without private rules", input, got, changed)
	}
}

func TestTitleNormalizerForceRulesNormalizeWholeTitle(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		rules   *TitleRules
		changed bool
	}{
		{
			name:  "force token",
			input: "Neutral klyuchmarker guide",
			want:  "Неутрал ключмаркер гуиде",
			rules: &TitleRules{
				forceNormalizeTokensCI: map[string]struct{}{"klyuchmarker": {}},
			},
			changed: true,
		},
		{
			name:  "case insensitive force phrase",
			input: "Neutral Novyiy stil guide",
			want:  "Неутрал Новый стил гуиде",
			rules: &TitleRules{
				forceNormalizeTokensCI:  map[string]struct{}{},
				forceNormalizePhrasesCI: []string{"novyiy stil"},
			},
			changed: true,
		},
		{
			name:  "protected technical and network tokens",
			input: "Neutral klyuchmarker C++ .NET 1C JavaScript React.js https://example.test/put [Provider Name] guide",
			want:  "Неутрал ключмаркер C++ .NET 1C JavaScript React.js https://example.test/put [Provider Name] гуиде",
			rules: &TitleRules{
				forceNormalizeTokensCI: map[string]struct{}{"klyuchmarker": {}},
			},
			changed: true,
		},
		{
			name:  "bracketed provider force token is ignored",
			input: "English [klyuchmarker] guide",
			want:  "English [klyuchmarker] guide",
			rules: &TitleRules{
				forceNormalizeTokensCI: map[string]struct{}{"klyuchmarker": {}},
			},
		},
		{
			name:  "URL force token is ignored",
			input: "English https://example.test/?klyuchmarker guide",
			want:  "English https://example.test/?klyuchmarker guide",
			rules: &TitleRules{
				forceNormalizeTokensCI: map[string]struct{}{"klyuchmarker": {}},
			},
		},
		{
			name:  "domain force token is ignored",
			input: "English klyuchmarker.example guide",
			want:  "English klyuchmarker.example guide",
			rules: &TitleRules{
				forceNormalizeTokensCI: map[string]struct{}{"klyuchmarker": {}},
			},
		},
		{
			name:  "bracketed provider force phrase is ignored",
			input: "English [novyiy stil] guide",
			want:  "English [novyiy stil] guide",
			rules: &TitleRules{
				forceNormalizeTokensCI:  map[string]struct{}{},
				forceNormalizePhrasesCI: []string{"novyiy stil"},
			},
		},
		{
			name:  "force phrase with protected final token is ignored",
			input: "English alpha PRO guide",
			want:  "English alpha PRO guide",
			rules: &TitleRules{
				forceNormalizeTokensCI:  map[string]struct{}{},
				forceNormalizePhrasesCI: []string{"alpha pro"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, changed := newTitleNormalizer(test.rules).Normalize(test.input)
			if got != test.want {
				t.Fatalf("Normalize(%q) = %q, want %q", test.input, got, test.want)
			}
			if changed != test.changed {
				t.Fatalf("Normalize(%q) changed = %t, want %t", test.input, changed, test.changed)
			}
		})
	}
}

func TestTitleNormalizerForceTokenOverridesOnlyHeuristicProtection(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		rules   *TitleRules
		changed bool
	}{
		{
			name:  "uppercase force token with English suffix",
			input: "English Klyuching guide",
			want:  "Енглиш Ключинг гуиде",
			rules: &TitleRules{
				forceNormalizeTokensCI: map[string]struct{}{"klyuching": {}},
			},
			changed: true,
		},
		{
			name:  "mixed case force token",
			input: "English KlyuchMarker guide",
			want:  "Енглиш Ключмаркер гуиде",
			rules: &TitleRules{
				forceNormalizeTokensCI: map[string]struct{}{"klyuchmarker": {}},
			},
			changed: true,
		},
		{
			name:  "bracketed force token stays protected",
			input: "English [Klyuching] guide",
			want:  "English [Klyuching] guide",
			rules: &TitleRules{
				forceNormalizeTokensCI: map[string]struct{}{"klyuching": {}},
			},
		},
		{
			name:  "domain force token stays protected",
			input: "English Klyuching.example guide",
			want:  "English Klyuching.example guide",
			rules: &TitleRules{
				forceNormalizeTokensCI: map[string]struct{}{"klyuching": {}},
			},
		},
		{
			name:  "explicitly protected force token stays protected",
			input: "English Klyuching guide",
			want:  "English Klyuching guide",
			rules: &TitleRules{
				protectedSubstringsCI:  []string{"klyuch"},
				forceNormalizeTokensCI: map[string]struct{}{"klyuching": {}},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, changed := newTitleNormalizer(test.rules).Normalize(test.input)
			if got != test.want {
				t.Fatalf("Normalize(%q) = %q, want %q", test.input, got, test.want)
			}
			if changed != test.changed {
				t.Fatalf("Normalize(%q) changed = %t, want %t", test.input, changed, test.changed)
			}
		})
	}
}

func TestTitleNormalizerStructuralCleanup(t *testing.T) {
	rules := &TitleRules{
		forceNormalizeTokensCI: map[string]struct{}{},
		structuralCleanup: titleStructuralCleanup{
			decodeHTMLEntities:        true,
			dropZeroWidthFormatChars:  true,
			underscoresAsSpaces:       true,
			stripLeadingProviderNoise: true,
		},
	}
	tests := []struct {
		name    string
		input   string
		want    string
		changed bool
	}{
		{
			name:    "entity filename noise and provider prefix",
			input:   `"'9.[Provider]_Kurs&nbsp;dlya​ novichkov`,
			want:    "[Provider] Курс\u00a0для новичков",
			changed: true,
		},
		{
			name:    "no underscore preserves exact whitespace",
			input:   "Advanced  Go\tGuide\u00a0Notes",
			want:    "Advanced  Go\tGuide\u00a0Notes",
			changed: false,
		},
		{
			name:    "underscore replacement preserves inter-field whitespace",
			input:   "Alpha__Beta  Gamma\tDelta\u00a0Notes",
			want:    "Alpha Beta  Gamma\tDelta\u00a0Notes",
			changed: true,
		},
		{
			name:    "cleanup only counts as change",
			input:   "Alpha__Beta",
			want:    "Alpha Beta",
			changed: true,
		},
		{
			name:    "ordinary single underscores become spaces",
			input:   "Alpha_Beta_Gamma",
			want:    "Alpha Beta Gamma",
			changed: true,
		},
		{
			name:    "URL underscore stays intact",
			input:   "https://example.test/my_course",
			want:    "https://example.test/my_course",
			changed: false,
		},
		{
			name:    "email underscore stays intact",
			input:   "Contact user_name@example.test today",
			want:    "Contact user_name@example.test today",
			changed: false,
		},
		{
			name:    "domain underscore stays intact",
			input:   "sub_domain.example",
			want:    "sub_domain.example",
			changed: false,
		},
		{
			name:    "technical file identifier stays intact",
			input:   "Open fixture_2468135_LANG today",
			want:    "Open fixture_2468135_LANG today",
			changed: false,
		},
		{
			name:    "whole technical-looking slug is cleaned",
			input:   "Fixture_2468135_LANG",
			want:    "Fixture 2468135 LANG",
			changed: true,
		},
		{
			name:    "malformed entity unchanged",
			input:   "Fish &bogus; Chips",
			want:    "Fish &bogus; Chips",
			changed: false,
		},
		{
			name:    "semicolonless entity-like URL query remains exact",
			input:   "https://example.test/?asset=1&copy=2",
			want:    "https://example.test/?asset=1&copy=2",
			changed: false,
		},
		{
			name:    "strict named entity is decoded",
			input:   "Alpha &amp; Beta",
			want:    "Alpha & Beta",
			changed: true,
		},
		{
			name:    "strict decimal numeric entity is decoded",
			input:   "Alpha &#169; Beta",
			want:    "Alpha © Beta",
			changed: true,
		},
		{
			name:    "strict hexadecimal numeric entity is decoded",
			input:   "Alpha &#xA9; Beta",
			want:    "Alpha © Beta",
			changed: true,
		},
		{
			name:    "format-only cleanup falls back to original",
			input:   "\u200b",
			want:    "\u200b",
			changed: false,
		},
		{
			name:    "separator-only cleanup falls back to original",
			input:   "___",
			want:    "___",
			changed: false,
		},
		{
			name:    "stray quote without bracketed provider stays",
			input:   `'English guide`,
			want:    `'English guide`,
			changed: false,
		},
		{
			name:    "paired outer quotes around provider title stay balanced",
			input:   `"[Provider] Course"`,
			want:    `"[Provider] Course"`,
			changed: false,
		},
		{
			name:    "paired curly outer quotes around provider title stay balanced",
			input:   `“[Provider] Course”`,
			want:    `“[Provider] Course”`,
			changed: false,
		},
		{
			name:    "ordinal without bracketed provider stays",
			input:   "12.English guide",
			want:    "12.English guide",
			changed: false,
		},
	}

	normalizer := newTitleNormalizer(rules)
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

func TestTitleNormalizerIsIdempotent(t *testing.T) {
	rules, err := LoadTitleRules(strings.NewReader(`{
		"schema_version": "title-normalization-rules/v2",
		"protected_tokens_ci": [],
		"protected_tokens_exact": [],
		"protected_substrings_ci": [],
		"force_normalize_tokens_ci": ["klyuchmarker"],
		"force_normalize_phrases_ci": [],
		"structural_cleanup": {
			"decode_html_entities": true,
			"drop_zero_width_format_chars": true,
			"underscores_as_spaces": true,
			"strip_leading_provider_noise": true
		}
	}`))
	if err != nil {
		t.Fatalf("load rules: %v", err)
	}
	normalizer := newTitleNormalizer(rules)
	first, changed := normalizer.Normalize(`7.[Provider]_Kurs dlya klyuchmarker`)
	if !changed {
		t.Fatal("first Normalize() changed = false, want true")
	}
	second, changed := normalizer.Normalize(first)
	if second != first || changed {
		t.Fatalf("second Normalize() = (%q, %t), want (%q, false)", second, changed, first)
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
