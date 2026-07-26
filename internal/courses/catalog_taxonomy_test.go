package courses

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func TestClassifyNewTopicTable(t *testing.T) {
	tests := []struct {
		title string
		want  string
	}{
		{"Основы Python для backend разработки", "development"},
		{"DevOps, QA и безопасность сервиса", "devops_security_qa"},
		{"Data science и нейросети для аналитики", "data_ai"},
		{"No-code автоматизация процессов", "nocode_automation"},
		{"UI дизайн интерфейсов в Figma", "design_ui_graphic"},
		{"Blender 3D motion и VFX сцены", "three_d_motion_vfx"},
		{"Фотосъемка и монтаж роликов", "photo_video"},
		{"Game design и разработка игр", "game_dev_design"},
		{"SMM, таргет и рекламный трафик", "marketing_ads_smm"},
		{"Заработок в интернете и онлайн-бизнес", "income_online"},
		{"Продажи на маркетплейсах и ecommerce", "ecommerce_marketplaces"},
		{"Управление бизнесом и командой", "business_management"},
		{"Продажи и клиентский сервис", "sales_service"},
		{"Инвестиции, крипта и личные финансы", "finance_crypto"},
		{"Психология и саморазвитие", "psychology_selfdev"},
		{"Отношения и семейные конфликты", "relationships_family"},
		{"Фитнес, питание и здоровье", "health_fitness_nutrition"},
		{"Макияж, стиль и уход за кожей", "beauty_style"},
		{"Английский язык для школы", "education_languages_school"},
		{"Родительство и развитие детей", "parenting_children"},
		{"Юридические сделки с недвижимостью", "law_realty"},
		{"Карьера, HR и фриланс", "career_hr_freelance"},
		{"Астрология и таро", "esoteric_astrology"},
		{"Публичные выступления и коммуникация", "communication_public_speaking"},
		{"Вокал, звук и Ableton", "music_audio_voice"},
		{"Писательское мастерство и акварель", "art_writing_hobbies"},
		{"Кулинария, шитье и уютный дом", "home_cooking_craft"},
		{"Пассивный доход онлайн на монетизации", "income_online"},
		{"Terraform и Kubernetes для cloud инфраструктуры", "devops_security_qa"},
		{"Дашборды Power BI и анализ данных", "data_ai"},
		{"Видеоконтент, цветокоррекция и Premiere", "photo_video"},
		{"Натальная карта и дизайн человека", "esoteric_astrology"},
		{"Язык тела и невербальная коммуникация", "communication_public_speaking"},
	}

	for _, test := range tests {
		t.Run(test.want, func(t *testing.T) {
			entry := validSourceEntry()
			entry.Title = test.title

			got := classify(entry)
			if !slices.Contains(got, test.want) {
				t.Fatalf("categories = %v, want %q", got, test.want)
			}
		})
	}
}

func TestClassifyTopicFalsePositiveGuards(t *testing.T) {
	tests := []struct {
		name string
		text string
		not  string
	}{
		{"generic video course", "Видеокурс по переговорам", "photo_video"},
		{"body language", "Язык тела и уверенность", "education_languages_school"},
		{"tone of voice", "Тон голоса в переговорах", "music_audio_voice"},
		{"3d secure", "3D Secure для интернет платежей", "three_d_motion_vfx"},
		{"family budget", "Семейный бюджет и инвестиции", "relationships_family"},
		{"electrical installation", "Монтаж электропроводки в доме", "photo_video"},
		{"project matrix", "Матрица управления проектом", "esoteric_astrology"},
		{"body language education", "Язык тела и невербальная коммуникация", "education_languages_school"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entry := validSourceEntry()
			entry.Title = test.text

			got := classify(entry)
			if slices.Contains(got, test.not) {
				t.Fatalf("categories = %v, must not contain %q", got, test.not)
			}
		})
	}
}

func TestClassifyTelegramSourceNoiseStaysOther(t *testing.T) {
	entry := validSourceEntry()
	entry.Title = "Источник Telegram канала"
	entry.RawBlock = "Исходный пост без тематических слов"

	got := classify(entry)
	if !slices.Equal(got, []string{"other"}) {
		t.Fatalf("categories = %v, want [other]", got)
	}
}

func TestClassifyDoesNotUseLinkProviderDomains(t *testing.T) {
	entry := validSourceEntry()
	entry.Title = "Практический курс"
	entry.RawBlock = "Материалы без тематических слов"
	entry.Links[0].Host = "python-design-ai.example.test"
	entry.Links[0].Provider = "python_design_ai"

	got := classify(entry)
	if !slices.Equal(got, []string{"other"}) {
		t.Fatalf("categories = %v, want [other]", got)
	}
}

func TestBuildGzipPropagatesDominantAuthorTopicToOtherEntries(t *testing.T) {
	source := validSource(t, sourceEntryWithIdentity("1:1:0", "1:1", "Python backend", "Fixture Author"))
	source.Messages = append(source.Messages,
		sourceMessage{MessageID: "1:2", TelegramMessageID: 2, URL: "https://messages.example.test/source/2"},
		sourceMessage{MessageID: "1:3", TelegramMessageID: 3, URL: "https://messages.example.test/source/3"},
		sourceMessage{MessageID: "1:4", TelegramMessageID: 4, URL: "https://messages.example.test/source/4"},
	)
	source.CatalogEntries = []sourceEntry{
		sourceEntryWithIdentity("1:1:0", "1:1", "Python backend", "Fixture Author"),
		sourceEntryWithIdentity("1:2:0", "1:2", "Разработка на Go", "Fixture Author"),
		sourceEntryWithIdentity("1:3:0", "1:3", "Нейтральная практика", "Fixture Author"),
		sourceEntryWithIdentity("1:4:0", "1:4", "Нейтральная серия", "Fixture Author"),
	}
	setSourceCounts(&source)

	var output bytes.Buffer
	if _, err := BuildGzip(sourceReader(t, source), &output); err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	catalog := decodeBuiltCatalog(t, &output)

	otherCount := 0
	developmentCount := 0
	for _, entry := range catalog.Entries {
		if slices.Contains(entry.Categories, "other") {
			otherCount++
		}
		if slices.Contains(entry.Categories, "development") {
			developmentCount++
		}
	}
	if otherCount != 0 || developmentCount != 4 {
		t.Fatalf("other/development counts = %d/%d, want 0/4", otherCount, developmentCount)
	}
}

func TestBuildGzipPropagatesDominantAuthorTopicToSingleOtherEntry(t *testing.T) {
	source := validSource(t, sourceEntryWithIdentity("1:1:0", "1:1", "Python backend", "Fixture Author"))
	source.Messages = append(source.Messages,
		sourceMessage{MessageID: "1:2", TelegramMessageID: 2, URL: "https://messages.example.test/source/2"},
		sourceMessage{MessageID: "1:3", TelegramMessageID: 3, URL: "https://messages.example.test/source/3"},
	)
	source.CatalogEntries = []sourceEntry{
		sourceEntryWithIdentity("1:1:0", "1:1", "Python backend", "Fixture Author"),
		sourceEntryWithIdentity("1:2:0", "1:2", "Разработка на Go", "Fixture Author"),
		sourceEntryWithIdentity("1:3:0", "1:3", "Нейтральная практика", "Fixture Author"),
	}
	setSourceCounts(&source)

	var output bytes.Buffer
	if _, err := BuildGzip(sourceReader(t, source), &output); err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	catalog := decodeBuiltCatalog(t, &output)

	for _, entry := range catalog.Entries {
		if slices.Equal(entry.Categories, []string{"other"}) {
			t.Fatalf("entry %q remained other, want dominant author topic", entry.Title)
		}
	}
}

func TestBuildGzipDoesNotPropagateAuthorTopicBelowThreshold(t *testing.T) {
	source := validSource(t, sourceEntryWithIdentity("1:1:0", "1:1", "Python backend", "Fixture Author"))
	source.Messages = append(source.Messages,
		sourceMessage{MessageID: "1:2", TelegramMessageID: 2, URL: "https://messages.example.test/source/2"},
		sourceMessage{MessageID: "1:3", TelegramMessageID: 3, URL: "https://messages.example.test/source/3"},
		sourceMessage{MessageID: "1:4", TelegramMessageID: 4, URL: "https://messages.example.test/source/4"},
		sourceMessage{MessageID: "1:5", TelegramMessageID: 5, URL: "https://messages.example.test/source/5"},
	)
	source.CatalogEntries = []sourceEntry{
		sourceEntryWithIdentity("1:1:0", "1:1", "Python backend", "Fixture Author"),
		sourceEntryWithIdentity("1:2:0", "1:2", "Инвестиции и крипта", "Fixture Author"),
		sourceEntryWithIdentity("1:3:0", "1:3", "Маркетинг и реклама", "Fixture Author"),
		sourceEntryWithIdentity("1:4:0", "1:4", "Нейтральная практика", "Fixture Author"),
		sourceEntryWithIdentity("1:5:0", "1:5", "Нейтральная серия", "Fixture Author"),
	}
	setSourceCounts(&source)

	var output bytes.Buffer
	if _, err := BuildGzip(sourceReader(t, source), &output); err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	catalog := decodeBuiltCatalog(t, &output)

	otherCount := 0
	for _, entry := range catalog.Entries {
		if slices.Equal(entry.Categories, []string{"other"}) {
			otherCount++
		}
	}
	if otherCount != 2 {
		t.Fatalf("other-only count = %d, want 2", otherCount)
	}
}

func sourceEntryWithIdentity(entryID, messageID, title, author string) sourceEntry {
	entry := validSourceEntry()
	entry.EntryID = entryID
	entry.MessageID = messageID
	entry.SourceMessageIDs = []string{messageID}
	entry.Title = title
	entry.RawBlock = title
	entry.Credit.Author = &author
	entry.Links[0].URL = "https://files.example.test/" + strings.ReplaceAll(entryID, ":", "-")
	return entry
}
