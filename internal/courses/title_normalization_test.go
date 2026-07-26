package courses

import "testing"

func TestNormalizeCourseTitle(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		changed bool
	}{
		{
			name:    "character illustration",
			input:   "Personazhnaya illyustratsiya",
			want:    "Персонажная иллюстрация",
			changed: true,
		},
		{
			name:    "photography from scratch",
			input:   "Fotografiya s nulya",
			want:    "Фотография с нуля",
			changed: true,
		},
		{
			name:    "systems analyst from scratch",
			input:   "Sistemnyiy analitik s nulya",
			want:    "Системный аналитик с нуля",
			changed: true,
		},
		{
			name:    "mathematics for financiers",
			input:   "Matematika dlya finansistov",
			want:    "Математика для финансистов",
			changed: true,
		},
		{
			name:    "data analyst with protected technical token",
			input:   "Analitik dannyih na Python",
			want:    "Аналитик данных на Python",
			changed: true,
		},
		{
			name:    "interior design with soft sign",
			input:   "Dekorirovanie v dizayne inter'era",
			want:    "Декорирование в дизайне интерьера",
			changed: true,
		},
		{
			name:    "technical tokens and hyphenated transliteration",
			input:   "JavaScript-freymvork React.js",
			want:    "JavaScript-фреймворк React.js",
			changed: true,
		},
		{
			name:    "dotted technical token in transliterated title",
			input:   "Razrabotka na React.js s nulya",
			want:    "Разработка на React.js с нуля",
			changed: true,
		},
		{
			name:    "c before number means Cyrillic preposition",
			input:   "Ableton Live c 0 do PRO",
			want:    "Ableton Live с 0 до PRO",
			changed: true,
		},
		{
			name:    "provider prefix remains protected",
			input:   "1.[Skillbox] Personazhnaya illyustratsiya v Adobe Photoshop",
			want:    "1.[Skillbox] Персонажная иллюстрация в Adobe Photoshop",
			changed: true,
		},
		{
			name:  "technical product",
			input: "Ableton Live",
			want:  "Ableton Live",
		},
		{
			name:  "bracketed metadata",
			input: "Concept Art [2020, RUS]",
			want:  "Concept Art [2020, RUS]",
		},
		{
			name:  "English framework title",
			input: "JavaScript framework React.js",
			want:  "JavaScript framework React.js",
		},
		{
			name:  "English data engineering title",
			input: "Python Data Engineering",
			want:  "Python Data Engineering",
		},
		{
			name:  "provider only",
			input: "Skillbox",
			want:  "Skillbox",
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

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, changed := normalizeCourseTitle(test.input)
			if got != test.want {
				t.Fatalf("normalizeCourseTitle(%q) = %q, want %q", test.input, got, test.want)
			}
			if changed != test.changed {
				t.Fatalf("normalizeCourseTitle(%q) changed = %t, want %t", test.input, changed, test.changed)
			}
		})
	}
}

func TestNormalizeCourseTitleProtectsNonTransliterationTokens(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "URL",
			input: "Kurs dlya https://example.com/ya-path",
			want:  "Курс для https://example.com/ya-path",
		},
		{
			name:  "email",
			input: "Kurs dlya user-name@example.com",
			want:  "Курс для user-name@example.com",
		},
		{
			name:  "domain",
			input: "Kurs dlya my-site.name",
			want:  "Курс для my-site.name",
		},
		{
			name:  "file identifier",
			input: "Kurs dlya archive_5881340_RUS_rutracker",
			want:  "Курс для archive_5881340_RUS_rutracker",
		},
		{
			name:  "digit-containing token",
			input: "Kurs dlya course2024",
			want:  "Курс для course2024",
		},
		{
			name:  "all capitals",
			input: "Kurs dlya PRO",
			want:  "Курс для PRO",
		},
		{
			name:  "internal mixed case",
			input: "Kurs dlya DataFrame",
			want:  "Курс для DataFrame",
		},
		{
			name:  "bracketed Latin text",
			input: "Kurs dlya [English Name]",
			want:  "Курс для [English Name]",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, changed := normalizeCourseTitle(test.input)
			if got != test.want {
				t.Fatalf("normalizeCourseTitle(%q) = %q, want %q", test.input, got, test.want)
			}
			if !changed {
				t.Fatalf("normalizeCourseTitle(%q) changed = false, want true", test.input)
			}
		})
	}
}

func TestNormalizeCourseTitleReviewRegressions(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		changed bool
	}{
		{
			name:    "forced freymvork leaves English context alone",
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
			name:  "hyphenated digit-containing token is not a phrase",
			input: "c-course2024-do",
			want:  "c-course2024-do",
		},
		{
			name:    "single uppercase letter remains protected",
			input:   "R dlya data",
			want:    "R для дата",
			changed: true,
		},
		{
			name:    "terminal period is punctuation",
			input:   "Kurs dlya programmistov.",
			want:    "Курс для программистов.",
			changed: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, changed := normalizeCourseTitle(test.input)
			if got != test.want {
				t.Fatalf("normalizeCourseTitle(%q) = %q, want %q", test.input, got, test.want)
			}
			if changed != test.changed {
				t.Fatalf("normalizeCourseTitle(%q) changed = %t, want %t", test.input, changed, test.changed)
			}
		})
	}
}

func TestNormalizeCourseTitleLeavesAmbiguousMarkersAlone(t *testing.T) {
	for _, title := range []string{
		"Autodesk Maya",
		"Yandex Cloud",
		"Yoga for beginners",
		"Mayan Art",
		"Miyazaki Storyboarding",
	} {
		t.Run(title, func(t *testing.T) {
			got, changed := normalizeCourseTitle(title)
			if got != title {
				t.Fatalf("normalizeCourseTitle(%q) = %q, want unchanged", title, got)
			}
			if changed {
				t.Fatalf("normalizeCourseTitle(%q) changed = true, want false", title)
			}
		})
	}
}

func TestNormalizeCourseTitlePrivateAuditRegressions(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		changed bool
	}{
		{
			name:  "school and version title",
			input: "XYZ School Blender 2.8 Intro",
			want:  "XYZ School Blender 2.8 Intro",
		},
		{
			name:  "malformed school provider prefix",
			input: "Broadcast Design School] Broadcast Starter Pack",
			want:  "Broadcast Design School] Broadcast Starter Pack",
		},
		{
			name:  "German title with sch roots",
			input: "Typisch!? Ein Büro in Deutschland",
			want:  "Typisch!? Ein Büro in Deutschland",
		},
		{
			name:  "computer vision school",
			input: "Computer Vision School",
			want:  "Computer Vision School",
		},
		{
			name:    "math basics with technical phrase",
			input:   "skillbox] Osnovyi matematiki dlya Data Science",
			want:    "skillbox] Основы математики для Data Science",
			changed: true,
		},
		{
			name:    "promotion with social network",
			input:   "Prodvizhenie v Instagram )",
			want:    "Продвижение в Instagram )",
			changed: true,
		},
		{
			name:    "game developer with engine",
			input:   "Professiya razrabotchik igr na Unity",
			want:    "Профессия разработчик игр на Unity",
			changed: true,
		},
		{
			name:    "uppercase transliteration digraph",
			input:   "SHahmatyi s nulya",
			want:    "Шахматы с нуля",
			changed: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, changed := normalizeCourseTitle(test.input)
			if got != test.want {
				t.Fatalf("normalizeCourseTitle(%q) = %q, want %q", test.input, got, test.want)
			}
			if changed != test.changed {
				t.Fatalf("normalizeCourseTitle(%q) changed = %t, want %t", test.input, changed, test.changed)
			}
		})
	}
}

func TestNormalizeCourseTitlePreservesDottedTokens(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		changed bool
	}{
		{
			input:   "Razrabotka na React.js s nulya",
			want:    "Разработка на React.js с нуля",
			changed: true,
		},
		{
			input: "site.name",
			want:  "site.name",
		},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, changed := normalizeCourseTitle(test.input)
			if got != test.want {
				t.Fatalf("normalizeCourseTitle(%q) = %q, want %q", test.input, got, test.want)
			}
			if changed != test.changed {
				t.Fatalf("normalizeCourseTitle(%q) changed = %t, want %t", test.input, changed, test.changed)
			}
		})
	}
}
