package courses

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	sourceSchema  = "telegram-webk-channel-export/v3"
	catalogSchema = "courses-catalog/v2"
)

type sourceExport struct {
	SchemaVersion string        `json:"schema_version"`
	ExportedAt    string        `json:"exported_at"`
	Source        catalogSource `json:"source"`
	Stats         struct {
		Retrieval struct {
			ExportedMessageCount int `json:"exported_message_count"`
		} `json:"retrieval"`
		Parsing struct {
			CatalogEntryCount  int `json:"catalog_entry_count"`
			ParsedLinkCount    int `json:"parsed_link_count"`
			PasswordValueCount int `json:"password_value_count"`
		} `json:"parsing"`
	} `json:"stats"`
	Messages       []sourceMessage `json:"messages"`
	CatalogEntries []sourceEntry   `json:"catalog_entries"`
}

type SourceInput struct {
	Reader io.Reader
	Name   string
}

type BuildOptions struct {
	TorrentDir       string
	TitleRules       *TitleRules
	LinkTombstones   *LinkTombstones
	LinkSuppressions *LinkSuppressions
	LinkEnrichment   *LinkEnrichmentCache
}

type catalogSource struct {
	ChannelID    int64  `json:"channel_id"`
	Title        string `json:"title"`
	WebURL       string `json:"web_url"`
	Participants int64  `json:"participants_count"`
}

type sourceMessage struct {
	MessageID         string `json:"message_id"`
	TelegramMessageID int64  `json:"telegram_message_id"`
	URL               string `json:"url"`
	Media             struct {
		Type     string `json:"type"`
		Document struct {
			FileName string `json:"file_name"`
			MIMEType string `json:"mime_type"`
		} `json:"document"`
	} `json:"media"`
}

type sourceEntry struct {
	EntryID          string     `json:"entry_id"`
	MessageID        string     `json:"message_id"`
	SourceMessageIDs []string   `json:"source_message_ids"`
	AddedAt          string     `json:"added_at"`
	Origin           string     `json:"origin"`
	Title            string     `json:"title"`
	Year             *int       `json:"year"`
	YearRange        *yearRange `json:"year_range"`
	Availability     string     `json:"availability"`
	Passwords        []string   `json:"passwords"`
	Notes            *string    `json:"notes"`
	RawBlock         string     `json:"raw_block"`
	Credit           struct {
		Author *string `json:"author"`
	} `json:"credit"`
	Links []sourceLink `json:"links"`
}

type yearRange struct {
	From int `json:"from"`
	To   int `json:"to"`
}

type sourceLink struct {
	URL            string   `json:"url"`
	Host           string   `json:"host"`
	Provider       string   `json:"provider"`
	Kind           string   `json:"kind"`
	Role           string   `json:"role"`
	Primary        bool     `json:"primary"`
	Label          *string  `json:"label"`
	NormalizedFrom string   `json:"normalized_from"`
	Sources        []string `json:"sources"`
}

type Catalog struct {
	SchemaVersion string             `json:"schema_version"`
	SourceSchema  string             `json:"source_schema"`
	ExportedAt    string             `json:"exported_at"`
	Source        catalogSource      `json:"source"`
	Stats         CatalogStats       `json:"stats"`
	Categories    []CategoryMetadata `json:"categories"`
	Formats       []FormatMetadata   `json:"formats"`
	Entries       []CatalogEntry     `json:"entries"`
}

type CatalogStats struct {
	Messages                   int `json:"messages"`
	SourceEntries              int `json:"source_entries"`
	SourceLinks                int `json:"source_links"`
	SourcePasswords            int `json:"source_passwords"`
	StructuralLinksRemoved     int `json:"structural_links_removed"`
	TombstonedLinksRemoved     int `json:"tombstoned_links_removed"`
	SuppressedLinksRemoved     int `json:"suppressed_links_removed"`
	EntriesWithoutLinksRemoved int `json:"entries_without_links_removed"`
	Entries                    int `json:"entries"`
	Links                      int `json:"links"`
	EnrichedLinks              int `json:"enriched_links"`
	Passwords                  int `json:"passwords"`
	NormalizedTitles           int `json:"normalized_titles"`
}

type CategoryMetadata struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Count  int    `json:"count"`
	Hidden bool   `json:"hidden,omitempty"`
}

type FormatMetadata struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

type CatalogEntry struct {
	ID              string          `json:"id"`
	Title           string          `json:"title"`
	TitleOriginal   *string         `json:"title_original,omitempty"`
	Author          *string         `json:"author"`
	Year            *int            `json:"year"`
	YearRange       *yearRange      `json:"year_range"`
	FirstAddedAt    string          `json:"first_added_at"`
	LastAddedAt     string          `json:"last_added_at"`
	Origins         []string        `json:"origins"`
	Availability    []string        `json:"availability"`
	Categories      []string        `json:"categories"`
	PrimaryCategory string          `json:"primary_category"`
	Formats         []string        `json:"formats"`
	PrimaryFormat   string          `json:"primary_format"`
	FormatSources   []string        `json:"format_sources"`
	Links           []CatalogLink   `json:"links"`
	Passwords       []string        `json:"passwords"`
	Notes           []string        `json:"notes"`
	Sources         []CatalogSource `json:"sources"`
}

type CatalogSource struct {
	EntryID           string   `json:"entry_id"`
	MessageID         string   `json:"message_id"`
	TelegramMessageID int64    `json:"telegram_message_id"`
	MessageURL        string   `json:"message_url"`
	SourceMessageIDs  []string `json:"source_message_ids"`
	AddedAt           string   `json:"added_at"`
	Origin            string   `json:"origin"`
	Availability      string   `json:"availability"`
}

type CatalogLink struct {
	URL      string       `json:"url"`
	Host     string       `json:"host"`
	Provider string       `json:"provider"`
	Kind     string       `json:"kind"`
	Role     string       `json:"role"`
	Primary  bool         `json:"primary"`
	Label    *string      `json:"label"`
	Content  *LinkContent `json:"content,omitempty"`
}

type categoryDefinition struct {
	ID       string
	Label    string
	Hidden   bool
	Keywords []string
}

type formatDefinition struct {
	ID    string
	Label string
}

var (
	urlPattern          = regexp.MustCompile(`(?i)(?:https?|magnet):\S+`)
	httpURLPattern      = regexp.MustCompile(`(?i)https?://\S+`)
	bareDomainPattern   = regexp.MustCompile(`(?i)^(?:[\p{L}\p{N}-]+\.)+[\p{L}]{2,}$`)
	nonSearchChars      = regexp.MustCompile(`[^\p{L}\p{N}+#]+`)
	spacePattern        = regexp.MustCompile(`\s+`)
	danglingYearPattern = regexp.MustCompile(`\s+\((\d{4})$`)
	shortDatePattern    = regexp.MustCompile(`^\d{1,2}[./]\d{1,2}(?:[./]\d{2,4})?$`)
	categoryDefinitions = []categoryDefinition{
		{
			ID: "development", Label: "Разработка",
			Keywords: []string{
				"python", "javascript", "typescript", "java", "react", "vue", "node", "php", "go",
				"golang", "rust", "c++", "c#", ".net", "swift", "kotlin", "android", "ios",
				"flutter", "frontend", "backend", "fullstack", "html", "css", "sql", "mysql",
				"postgresql", "api", "разработ*", "программир*", "верстк*", "битрикс", "bitrix",
				"1c", "1с", "asp net", "backend разработ*", "веб разработ*", "programming",
				"django", "laravel", "nestjs", "nextjs", "next js", "wordpress", "leetcode",
				"алгоритм*", "структуры данных", "front end", "front-end", "veb verstka", "veb-verstka",
			},
		},
		{
			ID: "devops_security_qa", Label: "DevOps, безопасность, QA",
			Keywords: []string{
				"devops", "linux", "docker", "kubernetes", "k8s", "ci cd", "gitlab ci",
				"qa", "тестирован*", "автотест*", "selenium", "playwright", "кибербезопас*",
				"информационная безопасность", "пентест", "pentest", "хакинг", "security",
				"nginx", "ansible", "terraform", "sre", "bug bounty", "взлом*", "хакер*",
				"анонимност*", "darknet", "cisco", "тестировщик*", "системный администратор",
				"социальная инженерия", "devtools", "i2p", "freenet",
			},
		},
		{
			ID: "data_ai", Label: "Данные и AI",
			Keywords: []string{
				"data science", "data scientist", "data analyst", "аналитик данных", "анализ данных",
				"machine learning", "машинное обучение", "нейросет*", "нейронк*", "chatgpt", "gpt",
				"llm", "ai", "искусственный интеллект", "prompt", "midjourney", "stable diffusion",
				"power bi", "tableau", "excel аналитик", "эксель аналитик", "аналитик bi",
				"pandas", "python для анализа", "дашборд*", "big data", "sql аналитик",
				"excel", "эксель", "power query", "power pivot", "аналитика", "статистик*", "анализ данных",
			},
		},
		{
			ID: "nocode_automation", Label: "No-code и автоматизация",
			Keywords: []string{
				"no code", "no-code", "nocode", "ноукод", "airtable", "make.com", "zapier",
				"integromat", "автоматизац*", "чат бот*", "чатбот*", "ботостроен*", "tilda",
				"notion", "glide", "bubble", "webflow",
			},
		},
		{
			ID: "design_ui_graphic", Label: "Дизайн, UI, графика",
			Keywords: []string{
				"дизайн*", "designer", "ux", "ui", "figma", "photoshop", "illustrator",
				"graphic design", "графическ*", "иллюстрац*", "логотип", "брендинг",
				"типограф*", "лендинг", "web design", "визуал*", "sketch",
				"дизайнер", "дизайнера", "дизайнеров", "интерфейс*", "ui ux", "adobe xd",
			},
		},
		{
			ID: "three_d_motion_vfx", Label: "3D, motion, VFX",
			Keywords: []string{
				"3d", "blender", "cinema 4d", "maya", "zbrush", "houdini", "motion",
				"motion design", "vfx", "after effects", "анимац*", "моушн", "моделирован*",
				"визуализац*", "рендер", "cgi", "hard surface", "toon boom",
				"revit", "autocad", "архвиз*", "архитектурная визуализац*", "проектирован*",
			},
		},
		{
			ID: "photo_video", Label: "Фото и видео",
			Keywords: []string{
				"фотограф*", "фотосъемк*", "фото съемк*", "предметная съемк*", "ретуш*",
				"lightroom", "capture one", "premiere", "davinci", "видеосъемк*", "видео съемк*",
				"монтаж ролик*", "видеомонтаж", "постобработ*", "операторск*", "цветокоррекц*",
				"видеопродакшн", "видеоконтент", "reels съемк*", "youtube монтаж", "монтаж",
				"фильммейкинг", "снимать видео", "кино на коленке",
			},
		},
		{
			ID: "game_dev_design", Label: "GameDev и геймдизайн",
			Keywords: []string{
				"game dev", "gamedev", "game design", "геймдизайн", "разработка игр",
				"игровой дизайн", "unity", "unreal engine", "godot", "level design",
				"геймплей", "игровая механик*",
			},
		},
		{
			ID: "marketing_ads_smm", Label: "Маркетинг, реклама, SMM",
			Keywords: []string{
				"маркет*", "smm", "таргет*", "трафик", "seo", "контекст*", "директ",
				"google ads", "facebook ads", "instagram", "reels", "tiktok", "youtube",
				"продвиж*", "копирайт*", "контент", "воронк*", "лиды", "арбитраж",
				"бренд", "блог", "инфобизнес", "реклам*", "targetolog", "marketolog",
				"email маркетинг", "контент маркетинг", "яндекс директ", "performance marketing",
			},
		},
		{
			ID: "income_online", Label: "Заработок и онлайн-бизнес",
			Keywords: []string{
				"заработок", "заработать", "зарабатывать", "доход онлайн", "онлайн доход",
				"пассивный доход", "удаленный доход", "монетизац*", "онлайн бизнес",
				"онлайн-бизнес", "интернет бизнес", "заработок в интернете", "доход в интернете",
				"зарабатывай*", "заработка", "дополнительный доход", "источник дохода", "миллион",
			},
		},
		{
			ID: "ecommerce_marketplaces", Label: "E-commerce и маркетплейсы",
			Keywords: []string{
				"ecommerce", "e-commerce", "интернет магазин", "маркетплейс*", "wildberries",
				"ozon", "яндекс маркет", "авито", "селлер", "карточки товаров",
				"товарный бизнес", "продажи на маркетплейсах", "shopify", "интернет-магазин",
				"amazon fba", "карточка товара", "таобао", "taobao", "marketplace", "marketpleys",
				"marketpleysami", "alipay", "байер", "fiverr", "товарка", "wechat",
			},
		},
		{ID: "business_management", Label: "Бизнес и управление", Keywords: []string{"бизнес", "предпринимат*", "стартап", "менеджмент", "управлен*", "руководител*", "команд*", "product manager", "project manager", "scrum", "agile", "стратег*", "операцион*", "юнит экономик*", "финмодел*", "producer", "продюсер*"}},
		{ID: "sales_service", Label: "Продажи и клиентский сервис", Keywords: []string{"продаж*", "sales", "переговор*", "crm", "клиентский сервис", "клиент*", "аккаунт менедж*", "скрипты продаж", "продающ*", "сервис"}},
		{ID: "finance_crypto", Label: "Финансы, инвестиции, крипта", Keywords: []string{"инвестиц*", "инвестирование", "трейдинг", "трейдер*", "trading", "бирж*", "акци*", "облигац*", "крипт*", "crypto", "bitcoin", "nft", "defi", "binance", "финанс*", "налог*", "бухгалтер*", "капитал", "портфел*", "forex", "теханализ", "экономик*", "бюджет", "деньги", "кредит*", "страхован*", "учет финансов", "торговая система", "дивиденд*"}},
		{ID: "psychology_selfdev", Label: "Психология и саморазвитие", Keywords: []string{"психолог*", "психотерап*", "самооценк*", "эмоци*", "тревог*", "стресс", "депрес*", "мышление", "осознан*", "медитац*", "мотивац*", "привычк*", "саморазвит*", "коуч*", "личностный рост", "харизм*", "выгоран*", "границы", "терапия", "терапи*", "травм*", "гештальт*", "расстановк*", "метафорическ*", "созависим*", "уверенность", "emdr", "самогипноз", "социофоб*", "гипнотерап*", "схематерап*", "спиральная динамика"}},
		{ID: "relationships_family", Label: "Отношения и семья", Keywords: []string{"отношен*", "семейные отношен*", "семейный конфликт*", "пара", "партнер*", "знакомств*", "секс", "любовь", "брак", "развод", "женственност*", "мужчина", "мужчины", "женщина", "женщины", "девушка", "оргазм*", "либидо", "конфликты в паре"}},
		{ID: "health_fitness_nutrition", Label: "Здоровье, фитнес, питание", Keywords: []string{"здоров*", "питан*", "диет*", "нутрици*", "фитнес", "трениров*", "йога", "массаж", "похуд*", "сон", "гормон*", "медицин*", "остеопат*", "реабилитац*", "беремен*", "роды", "фармаколог*", "метаболизм", "пилатес", "осанка", "биохакинг", "анатоми*", "тестостерон", "витамин*", "биомеханик*", "иммунитет", "позвоноч*", "воркаут", "биохими*", "дофамин", "подтягиван*", "мышц*"}},
		{ID: "beauty_style", Label: "Красота и стиль", Keywords: []string{"косметолог*", "макияж", "визаж", "маникюр", "ногт*", "лицо", "кожа", "волос*", "ресниц*", "makeup", "косметик*", "стиль", "стилист*", "гардероб", "имидж", "бров*", "парикмахер*", "уход за кожей", "плетение кос", "наращивание"}},
		{ID: "education_languages_school", Label: "Образование, языки, школа", Keywords: []string{"английск*", "english", "немецк*", "испанск*", "японск*", "французск*", "китайск*", "чешск*", "иностранный язык", "егэ", "огэ", "школ*", "учител*", "педагог*", "репетитор*", "математик*", "matematika", "физик*", "истори*", "литератур*", "образован*", "методист", "ielts", "toefl", "университет", "обучение детей", "русский язык", "grammar", "грамматик*", "american accent"}},
		{ID: "parenting_children", Label: "Дети и родительство", Keywords: []string{"дети", "детей", "ребенок", "ребенка", "детск*", "родител*", "родительство", "мама", "мамы", "дошкольник*", "подрост*", "воспитан*", "материнств*", "детская психология", "развитие ребенка", "алалия"}},
		{
			ID: "law_realty", Label: "Право, недвижимость",
			Keywords: []string{
				"юрист*", "юридическ*", "право", "закон*", "договор*", "суд", "ип", "ооо",
				"самозанят*", "недвижим*", "риелтор*", "ипотек*", "аренд*", "госзакуп*", "имуществ*",
				"должник*", "строительств*", "планировка",
			},
		},
		{ID: "career_hr_freelance", Label: "Карьера, HR, фриланс", Keywords: []string{"карьер*", "резюме", "собеседован*", "работа", "фриланс", "удален*", "професси*", "стажиров*", "hr", "рекрут*", "linkedin", "портфолио", "soft skills", "профориентац*"}},
		{ID: "esoteric_astrology", Label: "Эзотерика и астрология", Keywords: []string{"астрол*", "астро", "таро", "нумеролог*", "эзотер*", "руны", "чакр*", "магия", "ритуал*", "регресс*", "карм*", "транзит*", "хорар*", "матрица судьбы", "матрица денег", "карта желаний", "ведическ*", "рейки", "натальная карта", "дизайн человека"}},
		{ID: "communication_public_speaking", Label: "Коммуникация и публичные выступления", Keywords: []string{"публичные выступ*", "выступлен*", "оратор*", "коммуникац*", "презентац*", "голос в переговорах", "речь", "сторителлинг", "storytelling", "нетворкинг", "переговоры", "язык тела", "невербальн*", "юмор", "шутить"}},
		{ID: "music_audio_voice", Label: "Музыка, аудио, голос", Keywords: []string{"музык*", "вокал", "петь", "гитар*", "fingerstyle", "битмейк*", "ableton", "fl studio", "сведен*", "мастеринг", "звук", "фортепиан*", "электроник*"}},
		{ID: "art_writing_hobbies", Label: "Искусство, письмо, хобби", Keywords: []string{"писател*", "сценари*", "режиссер*", "искусств*", "живопис*", "акварел*", "рисован*", "рисунок", "карандаш", "портрет", "аниме", "скетч*", "танц*", "шахмат*", "хобби", "каллиграф*", "иллюстрация", "проза", "поэзия"}},
		{ID: "home_cooking_craft", Label: "Дом, кулинария, рукоделие", Keywords: []string{"кулинар*", "готовк*", "рецепт*", "шить*", "пошив", "вязан*", "рукодел*", "интерьер*", "ремонт", "дом", "декор", "кондитер*", "выпечк*", "торт", "торты", "пирог*", "хлеб", "мыловар*", "мыло", "кофейня", "шеф повар", "вязание", "садовод*", "электромонтаж"}},
		{ID: "other", Label: "Другое"},
		{
			ID: "service", Label: "Служебное", Hidden: true,
			Keywords: []string{"инструкция как качать", "инструкция как скачать", "как скачать по magnet"},
		},
	}
	formatDefinitions = []formatDefinition{
		{ID: "course", Label: "Курс"},
		{ID: "workshop", Label: "Воркшоп / практикум"},
		{ID: "live_recording", Label: "Запись эфира"},
		{ID: "marathon", Label: "Марафон / челлендж"},
		{ID: "book_guide", Label: "Книга / гайд"},
		{ID: "templates_assets", Label: "Шаблоны / ассеты"},
		{ID: "bundle_library", Label: "Бандл / библиотека"},
		{ID: "club_membership", Label: "Клуб / подписка"},
		{ID: "audio", Label: "Аудио"},
		{ID: "torrent_attachment", Label: "Torrent-вложение"},
		{ID: "unspecified", Label: "Тип не указан"},
	}
)

func BuildGzip(input io.Reader, output io.Writer) (CatalogStats, error) {
	return BuildGzipWithTorrentDir(input, output, "")
}

func BuildGzipWithTorrentDir(input io.Reader, output io.Writer, torrentDir string) (CatalogStats, error) {
	return BuildGzipFromSources([]SourceInput{{Reader: input, Name: "input"}}, output, torrentDir)
}

func BuildGzipFromSources(inputs []SourceInput, output io.Writer, torrentDir string) (CatalogStats, error) {
	return BuildGzipFromSourcesWithOptions(inputs, output, BuildOptions{TorrentDir: torrentDir})
}

func BuildGzipFromSourcesWithOptions(inputs []SourceInput, output io.Writer, options BuildOptions) (CatalogStats, error) {
	if len(inputs) == 0 {
		return CatalogStats{}, errors.New("no source exports provided")
	}
	sources := make([]sourceExport, 0, len(inputs))
	for index, input := range inputs {
		name := strings.TrimSpace(input.Name)
		if name == "" {
			name = fmt.Sprintf("input %d", index+1)
		}
		if input.Reader == nil {
			return CatalogStats{}, fmt.Errorf("%s: source reader is nil", name)
		}
		source, err := decodeSourceExport(input.Reader)
		if err != nil {
			return CatalogStats{}, fmt.Errorf("%s: %w", name, err)
		}
		sources = append(sources, source)
	}
	source, err := mergeSourceExports(sources)
	if err != nil {
		return CatalogStats{}, err
	}
	return buildGzipFromSource(source, output, options)
}

func decodeSourceExport(input io.Reader) (sourceExport, error) {
	var source sourceExport
	decoder := json.NewDecoder(input)
	if err := decoder.Decode(&source); err != nil {
		return sourceExport{}, fmt.Errorf("decode source export: %w", err)
	}
	if source.SchemaVersion != sourceSchema {
		return sourceExport{}, fmt.Errorf("source schema %q, want %q", source.SchemaVersion, sourceSchema)
	}
	if len(source.CatalogEntries) == 0 {
		return sourceExport{}, errors.New("source export has no catalog entries")
	}
	if err := validateSourceCounts(source); err != nil {
		return sourceExport{}, err
	}
	return source, nil
}

func buildGzipFromSource(source sourceExport, output io.Writer, options BuildOptions) (CatalogStats, error) {
	messages := make(map[string]sourceMessage, len(source.Messages))
	for _, message := range source.Messages {
		if strings.TrimSpace(message.MessageID) == "" {
			return CatalogStats{}, errors.New("source message has no stable identity")
		}
		if _, exists := messages[message.MessageID]; exists {
			return CatalogStats{}, fmt.Errorf("duplicate source message %q", message.MessageID)
		}
		messages[message.MessageID] = message
	}
	torrentLinks, err := buildTorrentLinks(options.TorrentDir, source.Messages)
	if err != nil {
		return CatalogStats{}, err
	}

	clusters := newDisjointSet(len(source.CatalogEntries))
	identityOwners := make(map[string]int, len(source.CatalogEntries))
	titleLinkOwners := make(map[string]int, len(source.CatalogEntries))
	for index, entry := range source.CatalogEntries {
		identityKey := courseIdentityKey(source.Source.ChannelID, entry)
		if owner, exists := identityOwners[identityKey]; exists {
			clusters.union(index, owner)
		} else {
			identityOwners[identityKey] = index
		}

		titleKey := normalizeSearchText(cleanCourseTitle(entry.Title))
		for _, link := range entry.Links {
			if options.LinkTombstones.ContainsURL(link.URL) {
				continue
			}
			if options.LinkSuppressions.ContainsURL(entry.EntryID, link.URL) {
				continue
			}
			canonicalLinkKey, err := linkKey(link.URL)
			if err != nil {
				continue
			}
			titleLinkKey := titleKey + "\x1f" + canonicalLinkKey
			if owner, exists := titleLinkOwners[titleLinkKey]; exists {
				clusters.union(index, owner)
			} else {
				titleLinkOwners[titleLinkKey] = index
			}
		}
	}
	for index, entry := range source.CatalogEntries {
		for _, identityKey := range repairedCourseIdentityKeys(source.Source.ChannelID, entry) {
			if owner, exists := identityOwners[identityKey]; exists {
				clusters.union(index, owner)
			}
		}
	}
	unionSharedURLTitleVariants(source.CatalogEntries, clusters, options.LinkTombstones, options.LinkSuppressions)
	unionPostNormalizationTitleVariants(
		source.Source.ChannelID,
		source.CatalogEntries,
		clusters,
		options.TitleRules,
	)

	canonicalIndexes := make(map[int]int, len(source.CatalogEntries))
	for index, entry := range source.CatalogEntries {
		root := clusters.find(index)
		canonicalIndex, exists := canonicalIndexes[root]
		if !exists ||
			timestampBefore(entry.AddedAt, source.CatalogEntries[canonicalIndex].AddedAt) ||
			(entry.AddedAt == source.CatalogEntries[canonicalIndex].AddedAt &&
				entry.EntryID < source.CatalogEntries[canonicalIndex].EntryID) {
			canonicalIndexes[root] = index
		}
	}
	canonicalIdentityKeys := make(map[int]string, len(canonicalIndexes))
	for root, index := range canonicalIndexes {
		canonicalIdentityKeys[root] = courseIdentityKey(source.Source.ChannelID, source.CatalogEntries[index])
	}

	catalog := Catalog{
		SchemaVersion: catalogSchema,
		SourceSchema:  source.SchemaVersion,
		ExportedAt:    source.ExportedAt,
		Source:        source.Source,
		Entries:       make([]CatalogEntry, 0, len(source.CatalogEntries)),
		Stats: CatalogStats{
			Messages:        len(source.Messages),
			SourceEntries:   len(source.CatalogEntries),
			SourceLinks:     source.Stats.Parsing.ParsedLinkCount,
			SourcePasswords: source.Stats.Parsing.PasswordValueCount,
		},
	}
	entryIndexes := make(map[int]int, len(source.CatalogEntries))
	entryKeysByID := make(map[string]string, len(source.CatalogEntries))
	sourceEntryIDs := make(map[string]struct{}, len(source.CatalogEntries))

	for index, entry := range source.CatalogEntries {
		if strings.TrimSpace(entry.EntryID) == "" || strings.TrimSpace(entry.MessageID) == "" {
			return CatalogStats{}, fmt.Errorf("entry %d has no stable identity", index)
		}
		if _, exists := sourceEntryIDs[entry.EntryID]; exists {
			return CatalogStats{}, fmt.Errorf("duplicate source entry %q", entry.EntryID)
		}
		sourceEntryIDs[entry.EntryID] = struct{}{}
		if strings.TrimSpace(entry.Title) == "" {
			return CatalogStats{}, fmt.Errorf("entry %q has no title", entry.EntryID)
		}
		if _, err := time.Parse(time.RFC3339Nano, entry.AddedAt); err != nil {
			return CatalogStats{}, fmt.Errorf("entry %q has invalid added_at %q: %w", entry.EntryID, entry.AddedAt, err)
		}

		message, ok := messages[entry.MessageID]
		if !ok {
			return CatalogStats{}, fmt.Errorf("entry %q references missing message %q", entry.EntryID, entry.MessageID)
		}

		links := make([]CatalogLink, 0, len(entry.Links))
		for _, link := range entry.Links {
			if _, _, rejection := inspectLink(link.URL); rejection != "" {
				catalog.Stats.StructuralLinksRemoved++
				continue
			}
			if options.LinkTombstones.ContainsURL(link.URL) {
				catalog.Stats.TombstonedLinksRemoved++
				continue
			}
			if options.LinkSuppressions.ContainsURL(entry.EntryID, link.URL) {
				catalog.Stats.SuppressedLinksRemoved++
				continue
			}
			if actionableSourceLink(entry, link) {
				catalogLink := catalogLinkFromSource(link)
				catalogLink.Content = cachedLinkContent(options.LinkEnrichment, link.URL)
				links = mergeLinks(links, []CatalogLink{catalogLink})
			}
		}
		if torrentLink, ok := torrentLinks[entry.MessageID]; ok {
			if options.LinkTombstones.ContainsURL(torrentLink.URL) {
				catalog.Stats.TombstonedLinksRemoved++
			} else if options.LinkSuppressions.ContainsURL(entry.EntryID, torrentLink.URL) {
				catalog.Stats.SuppressedLinksRemoved++
			} else {
				torrentLink.Content = cachedLinkContent(options.LinkEnrichment, torrentLink.URL)
				links = mergeLinks(links, []CatalogLink{torrentLink})
			}
		}

		clusterRoot := clusters.find(index)
		identityKey := canonicalIdentityKeys[clusterRoot]
		courseID := courseID(identityKey)
		if existingKey, exists := entryKeysByID[courseID]; exists && existingKey != identityKey {
			return CatalogStats{}, fmt.Errorf("course identity collision for %q", courseID)
		}
		entryKeysByID[courseID] = identityKey

		displayEntry, structurallyNormalized := repairLegacyDateRangeHeading(entry)
		sourceTitle := cleanCourseTitle(displayEntry.Title)
		sourceTitle, strippedTitleURL := stripExactExtractedTitleURL(sourceTitle, displayEntry.Links)
		displayTitle := sourceTitle
		normalized := false
		if options.TitleRules != nil {
			displayTitle, normalized = newTitleNormalizer(options.TitleRules).Normalize(sourceTitle)
		}
		var titleOriginal *string
		if structurallyNormalized || strippedTitleURL {
			original := cleanCourseTitle(entry.Title)
			titleOriginal = &original
		} else if normalized {
			titleOriginal = &sourceTitle
		}
		classificationEntry := displayEntry
		classificationEntry.Title = displayTitle
		categories := classify(classificationEntry)
		formats, formatSource := classifyFormats(classificationEntry, message)

		candidate := CatalogEntry{
			ID:              courseID,
			Title:           displayTitle,
			TitleOriginal:   titleOriginal,
			Author:          cleanOptionalString(displayEntry.Credit.Author),
			Year:            entry.Year,
			YearRange:       entry.YearRange,
			FirstAddedAt:    entry.AddedAt,
			LastAddedAt:     entry.AddedAt,
			Origins:         uniqueStrings([]string{entry.Origin}),
			Availability:    uniqueStrings([]string{entry.Availability}),
			Categories:      categories,
			PrimaryCategory: categories[0],
			Formats:         formats,
			PrimaryFormat:   formats[0],
			FormatSources:   []string{formatSource},
			Links:           links,
			Passwords:       uniqueStrings(entry.Passwords),
			Notes:           noteValues(entry.Notes),
			Sources: []CatalogSource{{
				EntryID:           entry.EntryID,
				MessageID:         entry.MessageID,
				TelegramMessageID: message.TelegramMessageID,
				MessageURL:        message.URL,
				SourceMessageIDs:  sourceMessageIDs(entry),
				AddedAt:           entry.AddedAt,
				Origin:            entry.Origin,
				Availability:      entry.Availability,
			}},
		}

		if existingIndex, exists := entryIndexes[clusterRoot]; exists {
			if index == canonicalIndexes[clusterRoot] {
				existing := catalog.Entries[existingIndex]
				mergeCatalogEntry(&candidate, existing)
				catalog.Entries[existingIndex] = candidate
			} else {
				mergeCatalogEntry(&catalog.Entries[existingIndex], candidate)
			}
			continue
		}
		entryIndexes[clusterRoot] = len(catalog.Entries)
		catalog.Entries = append(catalog.Entries, candidate)
	}

	catalog.Stats.EntriesWithoutLinksRemoved = removeEntriesWithoutLinks(&catalog.Entries)
	propagateAuthorTopics(catalog.Entries)
	recomputeCatalogStatsAndFacets(&catalog)

	gzipWriter, err := gzip.NewWriterLevel(output, gzip.BestCompression)
	if err != nil {
		return CatalogStats{}, fmt.Errorf("create gzip writer: %w", err)
	}
	encoder := json.NewEncoder(gzipWriter)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(catalog); err != nil {
		_ = gzipWriter.Close()
		return CatalogStats{}, fmt.Errorf("encode catalog: %w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		return CatalogStats{}, fmt.Errorf("close gzip catalog: %w", err)
	}
	return catalog.Stats, nil
}

func recomputeCatalogStatsAndFacets(catalog *Catalog) {
	catalog.Stats.Entries = len(catalog.Entries)
	catalog.Stats.Links = 0
	catalog.Stats.EnrichedLinks = 0
	catalog.Stats.Passwords = 0
	catalog.Stats.NormalizedTitles = 0
	catalog.Categories = catalog.Categories[:0]
	catalog.Formats = catalog.Formats[:0]
	categoryCounts := make(map[string]int, len(categoryDefinitions))
	formatCounts := make(map[string]int, len(formatDefinitions))
	for _, entry := range catalog.Entries {
		catalog.Stats.Links += len(entry.Links)
		for _, link := range entry.Links {
			if link.Content != nil {
				catalog.Stats.EnrichedLinks++
			}
		}
		catalog.Stats.Passwords += len(entry.Passwords)
		if entry.TitleOriginal != nil {
			catalog.Stats.NormalizedTitles++
		}
		for _, category := range entry.Categories {
			categoryCounts[category]++
		}
		for _, format := range entry.Formats {
			formatCounts[format]++
		}
	}
	for _, definition := range categoryDefinitions {
		catalog.Categories = append(catalog.Categories, CategoryMetadata{
			ID:     definition.ID,
			Label:  definition.Label,
			Count:  categoryCounts[definition.ID],
			Hidden: definition.Hidden,
		})
	}
	for _, definition := range formatDefinitions {
		catalog.Formats = append(catalog.Formats, FormatMetadata{
			ID:    definition.ID,
			Label: definition.Label,
			Count: formatCounts[definition.ID],
		})
	}
}

func removeEntriesWithoutLinks(entries *[]CatalogEntry) int {
	values := *entries
	kept := values[:0]
	removed := 0
	for _, entry := range values {
		if len(entry.Links) == 0 {
			removed++
			continue
		}
		kept = append(kept, entry)
	}
	*entries = kept
	return removed
}

func mergeSourceExports(sources []sourceExport) (sourceExport, error) {
	merged := sources[0]
	messageIndexes := make(map[string]int, len(merged.Messages))
	entryIndexes := make(map[string]int, len(merged.CatalogEntries))
	for index := range merged.Messages {
		messageIndexes[merged.Messages[index].MessageID] = index
	}
	for index := range merged.CatalogEntries {
		entryIndexes[merged.CatalogEntries[index].EntryID] = index
	}

	for _, source := range sources[1:] {
		if err := validateSameSourceIdentity(merged.Source, source.Source); err != nil {
			return sourceExport{}, err
		}
		mergeSourceMetadata(&merged, source)
		for _, message := range source.Messages {
			if existingIndex, exists := messageIndexes[message.MessageID]; exists {
				mergeSourceMessage(&merged.Messages[existingIndex], message)
				continue
			}
			messageIndexes[message.MessageID] = len(merged.Messages)
			merged.Messages = append(merged.Messages, message)
		}
		for _, entry := range source.CatalogEntries {
			if existingIndex, exists := entryIndexes[entry.EntryID]; exists {
				mergeSourceEntry(&merged.CatalogEntries[existingIndex], entry)
				continue
			}
			entryIndexes[entry.EntryID] = len(merged.CatalogEntries)
			merged.CatalogEntries = append(merged.CatalogEntries, entry)
		}
	}
	recomputeSourceStats(&merged)
	return merged, nil
}

func validateSameSourceIdentity(left, right catalogSource) error {
	if left.ChannelID != 0 && right.ChannelID != 0 && left.ChannelID != right.ChannelID {
		return fmt.Errorf("conflicting source channel IDs %d and %d", left.ChannelID, right.ChannelID)
	}
	if left.ChannelID == 0 && right.ChannelID == 0 &&
		strings.TrimSpace(left.WebURL) != "" && strings.TrimSpace(right.WebURL) != "" &&
		strings.TrimSpace(left.WebURL) != strings.TrimSpace(right.WebURL) {
		return fmt.Errorf("conflicting source web URLs %q and %q", left.WebURL, right.WebURL)
	}
	return nil
}

func mergeSourceMetadata(target *sourceExport, source sourceExport) {
	if strings.TrimSpace(source.ExportedAt) != "" {
		target.ExportedAt = source.ExportedAt
	}
	if source.Source.ChannelID != 0 {
		target.Source.ChannelID = source.Source.ChannelID
	}
	if strings.TrimSpace(source.Source.Title) != "" {
		target.Source.Title = source.Source.Title
	}
	if strings.TrimSpace(source.Source.WebURL) != "" {
		target.Source.WebURL = source.Source.WebURL
	}
	if source.Source.Participants != 0 {
		target.Source.Participants = source.Source.Participants
	}
}

func mergeSourceMessage(target *sourceMessage, source sourceMessage) {
	if source.TelegramMessageID != 0 {
		target.TelegramMessageID = source.TelegramMessageID
	}
	if strings.TrimSpace(source.URL) != "" {
		target.URL = source.URL
	}
	if strings.TrimSpace(source.Media.Type) != "" {
		target.Media.Type = source.Media.Type
	}
	if strings.TrimSpace(source.Media.Document.FileName) != "" {
		target.Media.Document.FileName = source.Media.Document.FileName
	}
	if strings.TrimSpace(source.Media.Document.MIMEType) != "" {
		target.Media.Document.MIMEType = source.Media.Document.MIMEType
	}
}

func mergeSourceEntry(target *sourceEntry, source sourceEntry) {
	if strings.TrimSpace(source.MessageID) != "" {
		target.MessageID = source.MessageID
	}
	target.SourceMessageIDs = appendUnique(target.SourceMessageIDs, source.SourceMessageIDs)
	if strings.TrimSpace(source.AddedAt) != "" {
		target.AddedAt = source.AddedAt
	}
	if strings.TrimSpace(source.Origin) != "" {
		target.Origin = source.Origin
	}
	if strings.TrimSpace(source.Title) != "" {
		target.Title = source.Title
	}
	if source.Year != nil {
		target.Year = source.Year
	}
	if source.YearRange != nil {
		target.YearRange = source.YearRange
	}
	if strings.TrimSpace(source.Availability) != "" {
		target.Availability = source.Availability
	}
	target.Passwords = appendUnique(target.Passwords, source.Passwords)
	if source.Notes != nil && strings.TrimSpace(*source.Notes) != "" {
		target.Notes = source.Notes
	}
	if strings.TrimSpace(source.RawBlock) != "" {
		target.RawBlock = source.RawBlock
	}
	if source.Credit.Author != nil && strings.TrimSpace(*source.Credit.Author) != "" {
		target.Credit.Author = source.Credit.Author
	}
	target.Links = mergeSourceLinks(target.Links, source.Links)
}

func mergeSourceLinks(target, values []sourceLink) []sourceLink {
	indexes := make(map[string]int, len(target)+len(values))
	for index, link := range target {
		indexes[sourceLinkMergeKey(link)] = index
	}
	for _, link := range values {
		key := sourceLinkMergeKey(link)
		index, exists := indexes[key]
		if !exists {
			indexes[key] = len(target)
			target = append(target, link)
			continue
		}
		if link.Primary {
			target[index].Primary = true
		}
		if target[index].Label == nil && link.Label != nil {
			target[index].Label = link.Label
		}
		if strings.TrimSpace(target[index].NormalizedFrom) == "" {
			target[index].NormalizedFrom = link.NormalizedFrom
		}
		target[index].Sources = appendUnique(target[index].Sources, link.Sources)
	}
	return target
}

func sourceLinkMergeKey(link sourceLink) string {
	if key, err := linkKey(link.URL); err == nil {
		return key
	}
	return strings.TrimSpace(link.URL)
}

func catalogLinkFromSource(link sourceLink) CatalogLink {
	return CatalogLink{
		URL:      strings.TrimSpace(link.URL),
		Host:     link.Host,
		Provider: link.Provider,
		Kind:     link.Kind,
		Role:     link.Role,
		Primary:  link.Primary,
		Label:    link.Label,
	}
}

func cachedLinkContent(cache *LinkEnrichmentCache, rawURL string) *LinkContent {
	if cache == nil {
		return nil
	}
	content, ok := cache.ContentForURL(rawURL)
	if !ok {
		return nil
	}
	return content
}

func actionableSourceLink(entry sourceEntry, link sourceLink) bool {
	if alwaysActionableSourceLink(link) {
		return true
	}
	if explicitHTTPLinkInEntryText(entry, link.URL) {
		return true
	}
	if standaloneExplicitSourceLink(link) {
		return true
	}
	return !bareDomainTitleOrCreditLink(entry, link)
}

func alwaysActionableSourceLink(link sourceLink) bool {
	if link.Primary || strings.EqualFold(link.Role, "primary") || strings.EqualFold(link.Kind, "file_host") {
		return true
	}
	return strings.EqualFold(link.Provider, "magnet") || strings.HasPrefix(strings.ToLower(strings.TrimSpace(link.URL)), "magnet:")
}

func explicitHTTPLinkInEntryText(entry sourceEntry, rawURL string) bool {
	for _, text := range entryLinkTexts(entry) {
		for _, match := range httpURLPattern.FindAllString(text, -1) {
			if sameHTTPLink(match, rawURL) {
				return true
			}
		}
	}
	return false
}

func entryLinkTexts(entry sourceEntry) []string {
	texts := []string{entry.Title, entry.RawBlock}
	if entry.Credit.Author != nil {
		texts = append(texts, *entry.Credit.Author)
	}
	for _, link := range entry.Links {
		if link.Label != nil {
			texts = append(texts, *link.Label)
		}
	}
	return texts
}

func sameHTTPLink(left, right string) bool {
	leftURL, err := url.Parse(strings.TrimRight(strings.TrimSpace(left), ".,;:)>]}'\""))
	if err != nil {
		return false
	}
	rightURL, err := url.Parse(strings.TrimRight(strings.TrimSpace(right), ".,;:)>]}'\""))
	if err != nil {
		return false
	}
	if leftURL.Scheme == "" || rightURL.Scheme == "" ||
		!strings.EqualFold(leftURL.Host, rightURL.Host) {
		return false
	}
	if !strings.EqualFold(leftURL.Scheme, "http") && !strings.EqualFold(leftURL.Scheme, "https") {
		return false
	}
	if !strings.EqualFold(rightURL.Scheme, "http") && !strings.EqualFold(rightURL.Scheme, "https") {
		return false
	}
	return strings.TrimRight(leftURL.EscapedPath(), "/") == strings.TrimRight(rightURL.EscapedPath(), "/") &&
		leftURL.RawQuery == rightURL.RawQuery
}

func standaloneExplicitSourceLink(link sourceLink) bool {
	for _, source := range link.Sources {
		source = strings.ToLower(strings.TrimSpace(source))
		if strings.Contains(source, "standalone") &&
			(strings.Contains(source, "link") || strings.Contains(source, "url") || strings.Contains(source, "domain")) {
			return true
		}
	}
	return false
}

func bareDomainTitleOrCreditLink(entry sourceEntry, link sourceLink) bool {
	if !bareDomainPattern.MatchString(strings.TrimSpace(link.NormalizedFrom)) {
		return false
	}
	if sourceFromTitleOrCredit(link) || labelMatchesTitleOrCredit(entry, link) {
		return true
	}
	host := sourceLinkHost(link)
	if host == "" {
		return false
	}
	if textMentionsHost(entry.Title, host) {
		return true
	}
	return entry.Credit.Author != nil && textMentionsHost(*entry.Credit.Author, host)
}

func sourceFromTitleOrCredit(link sourceLink) bool {
	for _, source := range link.Sources {
		source = strings.ToLower(strings.TrimSpace(source))
		if strings.Contains(source, "title") || strings.Contains(source, "credit") || strings.Contains(source, "author") {
			return true
		}
	}
	return false
}

func labelMatchesTitleOrCredit(entry sourceEntry, link sourceLink) bool {
	if link.Label == nil {
		return false
	}
	label := strings.TrimSpace(*link.Label)
	if label == "" {
		return false
	}
	if strings.EqualFold(label, strings.TrimSpace(entry.Title)) {
		return true
	}
	return entry.Credit.Author != nil && strings.EqualFold(label, strings.TrimSpace(*entry.Credit.Author))
}

func sourceLinkHost(link sourceLink) string {
	if host := strings.TrimSpace(link.Host); host != "" {
		return strings.TrimPrefix(strings.ToLower(host), "www.")
	}
	parsed, err := url.Parse(strings.TrimSpace(link.URL))
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(parsed.Host), "www.")
}

func textMentionsHost(text, host string) bool {
	return strings.Contains(strings.ToLower(text), host)
}

func recomputeSourceStats(source *sourceExport) {
	source.Stats.Retrieval.ExportedMessageCount = len(source.Messages)
	source.Stats.Parsing.CatalogEntryCount = len(source.CatalogEntries)
	source.Stats.Parsing.ParsedLinkCount = 0
	source.Stats.Parsing.PasswordValueCount = 0
	for _, entry := range source.CatalogEntries {
		source.Stats.Parsing.ParsedLinkCount += len(entry.Links)
		source.Stats.Parsing.PasswordValueCount += len(entry.Passwords)
	}
}

func cleanCourseTitle(value string) string {
	value = strings.TrimSpace(value)
	if trimmed := strings.TrimLeft(value, "'’"); trimmed != value && strings.HasPrefix(trimmed, "[") {
		value = trimmed
	}
	for {
		trimmed := value
		for _, prefix := range []string{"- ", "— ", "– ", "• "} {
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
			if trimmed != value {
				break
			}
		}
		if trimmed == value {
			return value
		}
		value = trimmed
	}
}

func stripExactExtractedTitleURL(title string, links []sourceLink) (string, bool) {
	extractedURLs := make(map[string]struct{}, len(links))
	for _, link := range links {
		key, err := linkKey(link.URL)
		if err != nil || !strings.HasPrefix(strings.ToLower(key), "http") {
			continue
		}
		extractedURLs[key] = struct{}{}
	}
	if len(extractedURLs) == 0 {
		return title, false
	}

	changed := false
	for {
		removed := false
		for _, match := range httpURLPattern.FindAllStringIndex(title, -1) {
			if !hasWhitespaceBoundaries(title, match[0], match[1]) {
				continue
			}
			key, err := linkKey(title[match[0]:match[1]])
			if err != nil {
				continue
			}
			if _, exists := extractedURLs[key]; !exists {
				continue
			}

			left := strings.TrimRightFunc(title[:match[0]], unicode.IsSpace)
			right := strings.TrimLeftFunc(title[match[1]:], unicode.IsSpace)
			if left == "" && right == "" {
				continue
			}
			separator := ""
			if left != "" && right != "" {
				separator = " "
			}
			title = left + separator + right
			changed = true
			removed = true
			break
		}
		if !removed {
			return title, changed
		}
	}
}

func hasWhitespaceBoundaries(value string, start, end int) bool {
	if start > 0 {
		previous, _ := utf8.DecodeLastRuneInString(value[:start])
		if !unicode.IsSpace(previous) {
			return false
		}
	}
	if end < len(value) {
		next, _ := utf8.DecodeRuneInString(value[end:])
		if !unicode.IsSpace(next) {
			return false
		}
	}
	return true
}

func repairLegacyDateRangeHeading(entry sourceEntry) (sourceEntry, bool) {
	parsedTitle := collapseCourseHeadingWhitespace(entry.Title)
	if !shortDatePattern.MatchString(parsedTitle) || entry.Credit.Author == nil {
		return entry, false
	}
	parsedAuthor := collapseCourseHeadingWhitespace(*entry.Credit.Author)
	authorParts := strings.Fields(parsedAuthor)
	if len(authorParts) == 0 ||
		!shortDatePattern.MatchString(authorParts[len(authorParts)-1]) {
		return entry, false
	}

	heading, _, _ := strings.Cut(entry.RawBlock, "\n")
	heading = collapseCourseHeadingWhitespace(heading)
	if strings.HasPrefix(heading, "[[") {
		heading = heading[1:]
	}
	provider := ""
	for strings.HasPrefix(heading, "[") {
		end := strings.IndexByte(heading, ']')
		if end < 2 {
			return entry, false
		}
		label := collapseCourseHeadingWhitespace(heading[1:end])
		if provider == "" {
			provider = label
		}
		heading = collapseCourseHeadingWhitespace(heading[end+1:])
	}
	heading = collapseCourseHeadingWhitespace(strings.TrimPrefix(heading, "]"))
	if provider == "" {
		return entry, false
	}
	if entry.Year != nil {
		heading = collapseCourseHeadingWhitespace(
			strings.TrimSuffix(heading, fmt.Sprintf(" (%d)", *entry.Year)),
		)
	}
	left, right, ok := strings.Cut(heading, "―")
	left = collapseCourseHeadingWhitespace(left)
	right = collapseCourseHeadingWhitespace(right)
	if !ok ||
		left != parsedAuthor ||
		right != parsedTitle {
		return entry, false
	}

	repaired := entry
	repaired.Title = left + " ― " + right
	repairedAuthor := provider
	repaired.Credit.Author = &repairedAuthor
	return repaired, true
}

func collapseCourseHeadingWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

type disjointSet struct {
	parent []int
	rank   []uint8
}

func newDisjointSet(size int) *disjointSet {
	parent := make([]int, size)
	for index := range parent {
		parent[index] = index
	}
	return &disjointSet{
		parent: parent,
		rank:   make([]uint8, size),
	}
}

func (set *disjointSet) find(index int) int {
	if set.parent[index] != index {
		set.parent[index] = set.find(set.parent[index])
	}
	return set.parent[index]
}

func (set *disjointSet) union(left, right int) {
	leftRoot := set.find(left)
	rightRoot := set.find(right)
	if leftRoot == rightRoot {
		return
	}
	if set.rank[leftRoot] < set.rank[rightRoot] {
		leftRoot, rightRoot = rightRoot, leftRoot
	}
	set.parent[rightRoot] = leftRoot
	if set.rank[leftRoot] == set.rank[rightRoot] {
		set.rank[leftRoot]++
	}
}

func cleanOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	cleaned := strings.TrimSpace(*value)
	if cleaned == "" {
		return nil
	}
	return &cleaned
}

func courseIdentityKey(channelID int64, entry sourceEntry) string {
	author := ""
	if entry.Credit.Author != nil {
		author = normalizeSearchText(*entry.Credit.Author)
	}
	return courseIdentityKeyFromParts(channelID, identityCourseTitle(entry), author, entry.Year, entry.YearRange)
}

func courseIdentityKeyFromParts(channelID int64, title, author string, entryYear *int, yearRangeValue *yearRange) string {
	yearValue := ""
	if entryYear != nil {
		yearValue = fmt.Sprintf("year:%d", *entryYear)
	}
	yearRange := ""
	if yearRangeValue != nil {
		yearRange = fmt.Sprintf("range:%d-%d", yearRangeValue.From, yearRangeValue.To)
	}
	return strings.Join([]string{
		fmt.Sprintf("channel:%d", channelID),
		normalizeSearchText(title),
		author,
		yearValue,
		yearRange,
	}, "\x1f")
}

func identityCourseTitle(entry sourceEntry) string {
	title := cleanCourseTitle(entry.Title)
	if entry.Year == nil {
		return title
	}
	matches := danglingYearPattern.FindStringSubmatch(title)
	if len(matches) != 2 || matches[1] != fmt.Sprintf("%d", *entry.Year) {
		return title
	}
	return strings.TrimSpace(title[:len(title)-len(matches[0])])
}

func repairedCourseIdentityKeys(channelID int64, entry sourceEntry) []string {
	author, title, ok := malformedAuthorTitle(cleanCourseTitle(entry.Title))
	if !ok {
		return nil
	}
	return []string{
		courseIdentityKeyFromParts(channelID, title, normalizeSearchText(author), entry.Year, entry.YearRange),
	}
}

func malformedAuthorTitle(title string) (string, string, bool) {
	if !strings.HasPrefix(title, "[") {
		before, after, ok := strings.Cut(title, "]")
		before = strings.TrimSpace(before)
		after = strings.TrimSpace(after)
		if ok && before != "" && after != "" && !strings.Contains(before, "[") {
			return before, after, true
		}
		return "", "", false
	}
	body := strings.TrimPrefix(title, "[")
	before, after, ok := strings.Cut(body, "|")
	before = strings.TrimSpace(before)
	after = strings.TrimSpace(strings.TrimPrefix(after, "]"))
	if ok && before != "" && after != "" {
		return before, after, true
	}
	return "", "", false
}

func unionSharedURLTitleVariants(
	entries []sourceEntry,
	clusters *disjointSet,
	tombstones *LinkTombstones,
	suppressions *LinkSuppressions,
) {
	ownersByURL := make(map[string][]int)
	for index, entry := range entries {
		for _, link := range entry.Links {
			if tombstones.ContainsURL(link.URL) {
				continue
			}
			if suppressions.ContainsURL(entry.EntryID, link.URL) {
				continue
			}
			key, err := linkKey(link.URL)
			if err != nil {
				continue
			}
			ownersByURL[key] = append(ownersByURL[key], index)
		}
	}
	for _, owners := range ownersByURL {
		for leftIndex := 0; leftIndex < len(owners); leftIndex++ {
			for rightIndex := leftIndex + 1; rightIndex < len(owners); rightIndex++ {
				left := entries[owners[leftIndex]]
				right := entries[owners[rightIndex]]
				if compatibleYears(left, right) && wholeTokenTitleContains(left.Title, right.Title) {
					clusters.union(owners[leftIndex], owners[rightIndex])
				}
			}
		}
	}
}

func unionPostNormalizationTitleVariants(
	channelID int64,
	entries []sourceEntry,
	clusters *disjointSet,
	rules *TitleRules,
) {
	if rules == nil {
		return
	}

	normalizer := newTitleNormalizer(rules)
	ownersByIdentity := make(map[string][]int, len(entries))
	changedIdentities := make(map[string]bool)
	for index, entry := range entries {
		displayEntry, _ := repairLegacyDateRangeHeading(entry)
		sourceTitle := cleanCourseTitle(displayEntry.Title)
		sourceTitle, _ = stripExactExtractedTitleURL(sourceTitle, displayEntry.Links)
		displayTitle, changed := normalizer.Normalize(sourceTitle)
		author := ""
		if displayEntry.Credit.Author != nil {
			author = normalizeSearchText(*displayEntry.Credit.Author)
		}
		identityKey := courseIdentityKeyFromParts(
			channelID,
			displayTitle,
			author,
			entry.Year,
			entry.YearRange,
		)
		ownersByIdentity[identityKey] = append(ownersByIdentity[identityKey], index)
		changedIdentities[identityKey] = changedIdentities[identityKey] || changed
	}

	for identityKey, owners := range ownersByIdentity {
		if !changedIdentities[identityKey] {
			continue
		}
		for _, owner := range owners[1:] {
			clusters.union(owners[0], owner)
		}
	}
}

func compatibleYears(left, right sourceEntry) bool {
	if left.Year == nil || right.Year == nil {
		return true
	}
	return *left.Year == *right.Year
}

func wholeTokenTitleContains(left, right string) bool {
	leftTokens := strings.Fields(normalizeSearchText(cleanCourseTitle(left)))
	rightTokens := strings.Fields(normalizeSearchText(cleanCourseTitle(right)))
	if len(leftTokens) == 0 || len(rightTokens) == 0 || len(leftTokens) == len(rightTokens) {
		return false
	}
	if len(leftTokens) > len(rightTokens) {
		leftTokens, rightTokens = rightTokens, leftTokens
	}
	return slices.Equal(leftTokens, rightTokens[:len(leftTokens)]) ||
		slices.Equal(leftTokens, rightTokens[len(rightTokens)-len(leftTokens):])
}

func courseID(identityKey string) string {
	digest := sha256.Sum256([]byte(identityKey))
	return "course:" + hex.EncodeToString(digest[:16])
}

func sourceMessageIDs(entry sourceEntry) []string {
	values := uniqueStrings(entry.SourceMessageIDs)
	if len(values) == 0 {
		return []string{entry.MessageID}
	}
	return values
}

func noteValues(note *string) []string {
	if note == nil {
		return []string{}
	}
	return uniqueStrings([]string{*note})
}

func uniqueStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func mergeCatalogEntry(target *CatalogEntry, source CatalogEntry) {
	if timestampBefore(source.FirstAddedAt, target.FirstAddedAt) {
		target.FirstAddedAt = source.FirstAddedAt
	}
	if timestampBefore(target.LastAddedAt, source.LastAddedAt) {
		target.LastAddedAt = source.LastAddedAt
	}
	target.Origins = appendUnique(target.Origins, source.Origins)
	target.Availability = appendUnique(target.Availability, source.Availability)
	target.Categories = orderedUnion(target.Categories, source.Categories, categoryIDs())
	target.PrimaryCategory = target.Categories[0]
	target.Formats = orderedUnion(target.Formats, source.Formats, formatIDs())
	target.PrimaryFormat = target.Formats[0]
	target.FormatSources = appendUnique(target.FormatSources, source.FormatSources)
	target.Links = mergeLinks(target.Links, source.Links)
	target.Passwords = appendUnique(target.Passwords, source.Passwords)
	target.Notes = appendUnique(target.Notes, source.Notes)
	target.Sources = append(target.Sources, source.Sources...)
}

func timestampBefore(left, right string) bool {
	leftTime, _ := time.Parse(time.RFC3339Nano, left)
	rightTime, _ := time.Parse(time.RFC3339Nano, right)
	return leftTime.Before(rightTime)
}

func appendUnique(target, values []string) []string {
	seen := make(map[string]struct{}, len(target)+len(values))
	for _, value := range target {
		seen[value] = struct{}{}
	}
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		target = append(target, value)
	}
	return target
}

func orderedUnion(left, right, order []string) []string {
	values := appendUnique(append([]string(nil), left...), right)
	present := make(map[string]struct{}, len(values))
	for _, value := range values {
		present[value] = struct{}{}
	}
	result := make([]string, 0, len(values))
	for _, value := range order {
		if _, exists := present[value]; exists {
			result = append(result, value)
			delete(present, value)
		}
	}
	for _, value := range values {
		if _, exists := present[value]; exists {
			result = append(result, value)
			delete(present, value)
		}
	}
	return result
}

func categoryIDs() []string {
	ids := make([]string, 0, len(categoryDefinitions))
	for _, definition := range categoryDefinitions {
		ids = append(ids, definition.ID)
	}
	return ids
}

func formatIDs() []string {
	ids := make([]string, 0, len(formatDefinitions))
	for _, definition := range formatDefinitions {
		ids = append(ids, definition.ID)
	}
	return ids
}

func mergeLinks(target, values []CatalogLink) []CatalogLink {
	indexes := make(map[string]int, len(target)+len(values))
	for index, link := range target {
		indexes[catalogLinkMergeKey(link)] = index
	}
	for _, link := range values {
		key := catalogLinkMergeKey(link)
		index, exists := indexes[key]
		if !exists {
			link.Content = cloneLinkContentPointer(link.Content)
			indexes[key] = len(target)
			target = append(target, link)
			continue
		}
		if link.Primary {
			target[index].Primary = true
		}
		if target[index].Label == nil && link.Label != nil {
			target[index].Label = link.Label
		}
		target[index].Content = preferredLinkContent(target[index].Content, link.Content)
	}
	return target
}

// preferredLinkContent counts populated fields and item metadata, then uses the
// lexicographically smaller JSON value as a stable tie-break independent of input order.
func preferredLinkContent(left, right *LinkContent) *LinkContent {
	if left == nil {
		return cloneLinkContentPointer(right)
	}
	if right == nil {
		return cloneLinkContentPointer(left)
	}
	leftScore := linkContentInformationScore(*left)
	rightScore := linkContentInformationScore(*right)
	preferred := left
	if rightScore > leftScore {
		preferred = right
	} else if rightScore == leftScore {
		leftJSON, _ := json.Marshal(left)
		rightJSON, _ := json.Marshal(right)
		if string(rightJSON) < string(leftJSON) {
			preferred = right
		}
	}
	return cloneLinkContentPointer(preferred)
}

func linkContentInformationScore(content LinkContent) int {
	score := len(content.MaterialTypes)
	if content.Name != "" {
		score++
	}
	if content.Kind != "" {
		score++
	}
	if content.SizeBytes != 0 {
		score++
	}
	if content.FileCount != 0 {
		score++
	}
	if content.FolderCount != 0 {
		score++
	}
	for _, item := range content.Items {
		score++
		if item.Name != "" {
			score++
		}
		if item.Kind != "" {
			score++
		}
		if item.SizeBytes != 0 {
			score++
		}
	}
	return score
}

func cloneLinkContentPointer(content *LinkContent) *LinkContent {
	if content == nil {
		return nil
	}
	clone := cloneLinkContent(*content)
	return &clone
}

func catalogLinkMergeKey(link CatalogLink) string {
	key, err := linkKey(link.URL)
	if err != nil {
		return strings.TrimSpace(link.URL)
	}
	return key
}

func propagateAuthorTopics(entries []CatalogEntry) {
	type authorStats struct {
		otherIndexes []int
		classified   int
		topics       map[string]int
	}
	statsByAuthor := make(map[string]*authorStats)
	for index, entry := range entries {
		author := normalizedEntryAuthor(entry)
		if author == "" {
			continue
		}
		stats := statsByAuthor[author]
		if stats == nil {
			stats = &authorStats{topics: make(map[string]int)}
			statsByAuthor[author] = stats
		}
		if slices.Equal(entry.Categories, []string{"other"}) {
			stats.otherIndexes = append(stats.otherIndexes, index)
			continue
		}
		topic := firstNonOtherCategory(entry.Categories)
		if topic == "" {
			continue
		}
		stats.classified++
		stats.topics[topic]++
	}

	order := categoryIDs()
	for _, stats := range statsByAuthor {
		if len(stats.otherIndexes) == 0 || stats.classified < 2 {
			continue
		}
		topic, count := dominantAuthorTopic(stats.topics, order)
		if topic == "" || count*100 < stats.classified*70 {
			continue
		}
		for _, index := range stats.otherIndexes {
			entries[index].Categories = []string{topic}
			entries[index].PrimaryCategory = topic
		}
	}
}

func normalizedEntryAuthor(entry CatalogEntry) string {
	if entry.Author == nil {
		return ""
	}
	return normalizeSearchText(*entry.Author)
}

func firstNonOtherCategory(categories []string) string {
	for _, category := range categories {
		if category != "other" && category != "service" {
			return category
		}
	}
	return ""
}

func dominantAuthorTopic(counts map[string]int, order []string) (string, int) {
	bestTopic := ""
	bestCount := 0
	for _, topic := range order {
		count := counts[topic]
		if count > bestCount {
			bestTopic = topic
			bestCount = count
		}
	}
	return bestTopic, bestCount
}

func classify(entry sourceEntry) []string {
	author := ""
	if entry.Credit.Author != nil {
		author = *entry.Credit.Author
	}
	text := normalizeSearchText(strings.Join([]string{entry.Title, author, entry.RawBlock}, " "))
	tokens := strings.Fields(text)
	if matchesCategory(text, categoryDefinitions[len(categoryDefinitions)-1].Keywords) {
		return []string{"service"}
	}

	var categories []string
	for _, definition := range categoryDefinitions {
		if definition.ID == "other" || definition.ID == "service" {
			continue
		}
		if matchesCategory(text, definition.Keywords) && !blockedCategoryMatch(definition.ID, text, tokens) {
			categories = append(categories, definition.ID)
		}
	}
	if len(categories) == 0 {
		return []string{"other"}
	}
	return categories
}

func blockedCategoryMatch(categoryID, text string, tokens []string) bool {
	switch categoryID {
	case "photo_video":
		if containsTokenSequence(tokens, "монтаж", "электропроводки") ||
			containsTokenSequence(tokens, "монтаж", "электрики") {
			return true
		}
		return containsAnyToken(tokens, "видеокурс", "видеокурса", "видеокурсы", "видеокурсов") &&
			!matchesCategory(text, []string{"фотограф*", "фотосъемк*", "видеосъемк*", "монтаж ролик*", "видеомонтаж", "ретуш*", "postproduction"})
	case "education_languages_school":
		return containsTokenSequence(tokens, "язык", "тела")
	case "music_audio_voice":
		return containsTokenSequence(tokens, "тон", "голоса")
	case "three_d_motion_vfx":
		return containsTokenSequence(tokens, "3d", "secure")
	case "relationships_family":
		return containsTokenSequence(tokens, "семейный", "бюджет") || containsTokenSequence(tokens, "семейные", "финансы")
	case "esoteric_astrology":
		return containsTokenSequence(tokens, "матрица", "управления", "проектом") ||
			containsTokenSequence(tokens, "матрица", "управления", "проектами")
	default:
		return false
	}
}

func classifyFormats(entry sourceEntry, message sourceMessage) ([]string, string) {
	text := normalizeSearchText(entry.Title)
	tokens := strings.Fields(text)
	formats := make([]string, 0, 2)

	if matchesCourseFormat(tokens) {
		formats = append(formats, "course")
	}
	if matchesWorkshopFormat(tokens) {
		formats = append(formats, "workshop")
	}
	if matchesLiveRecordingFormat(tokens) {
		formats = append(formats, "live_recording")
	}
	if matchesMarathonFormat(tokens) {
		formats = append(formats, "marathon")
	}
	if matchesBookFormat(tokens) {
		formats = append(formats, "book_guide")
	}
	if matchesTemplatesAssetsFormat(tokens) {
		formats = append(formats, "templates_assets")
	}
	if matchesBundleLibraryFormat(tokens) {
		formats = append(formats, "bundle_library")
	}
	if matchesClubMembershipFormat(tokens) {
		formats = append(formats, "club_membership")
	}
	if matchesAudioFormat(tokens) {
		formats = append(formats, "audio")
	}
	if message.Media.Type == "messageMediaDocument" &&
		strings.EqualFold(message.Media.Document.MIMEType, "application/x-bittorrent") {
		formats = append(formats, "torrent_attachment")
	}

	if len(formats) == 0 {
		return []string{"unspecified"}, "fallback"
	}
	if formats[0] == "torrent_attachment" {
		return formats, "availability"
	}
	return formats, "title"
}

func matchesAudioFormat(tokens []string) bool {
	if containsAnyToken(tokens,
		"аудиокурс", "аудиокурсы", "аудиокурса", "аудиокурсов",
		"аудиокнига", "аудиокниги", "аудиокнигу", "аудиокнигой", "аудиокниг",
		"аудиоурок", "аудиоуроки", "аудиоуроков",
		"аудиолекция", "аудиолекции", "аудиолекций",
		"аудиотренинг", "аудиотренинги", "аудиотренингов",
		"аудиопрограмма", "аудиопрограммы", "аудиопрограмм",
		"audiocourse", "audiocourses", "audiobook", "audiobooks",
	) {
		return true
	}
	for index, token := range tokens {
		if token != "аудио" && token != "audio" {
			continue
		}
		if index+1 < len(tokens) && containsAnyToken(tokens[index+1:index+2],
			"курс", "курсы", "книга", "книги", "урок", "уроки", "лекция", "лекции",
			"тренинг", "программа", "course", "courses", "book", "books", "lesson",
			"lessons", "lecture", "lectures", "training", "assembly",
		) {
			return true
		}
	}
	return false
}

func matchesBookFormat(tokens []string) bool {
	if containsAnyToken(tokens,
		"книга", "книги", "книгу", "книгой", "книге",
		"book", "books", "ebook", "ebooks", "workbook", "workbooks",
		"гайд", "гайда", "гайды", "гайдов", "guide", "guides",
		"чеклист", "чеклиста", "чеклисты", "чеклистов",
		"шпаргалка", "шпаргалки", "шпаргалку", "шпаргалок",
		"методичка", "методички", "методичку", "методичек",
		"пособие", "пособия", "пособию", "пособий",
		"учебник", "учебника", "учебники", "учебников",
		"самоучитель", "самоучителя", "самоучители", "самоучителей",
		"мануал", "мануала", "мануалы", "мануалов", "manual", "manuals",
	) {
		return true
	}
	if containsTokenSequence(tokens, "чек", "лист") || containsTokenSequence(tokens, "work", "book") {
		return true
	}
	if containsAnyToken(tokens, "книг") && containsAnyToken(tokens,
		"все", "сборник", "сборники", "библиотека", "библиотеки", "коллекция",
		"коллекции", "комплект", "комплекты", "список", "списки",
	) {
		return true
	}
	return false
}

func matchesLiveRecordingFormat(tokens []string) bool {
	return containsAnyToken(tokens,
		"вебинар", "вебинара", "вебинары", "вебинаров", "вебинаре",
		"webinar", "webinars", "мастеркласс", "мастерклассы",
		"masterclass", "masterclasses",
		"семинар", "семинара", "семинары", "семинаров", "семинаре",
		"seminar", "seminars",
		"лекция", "лекции", "лекцию", "лекций", "lecture", "lectures",
	) || containsTokenSequence(tokens, "мастер", "класс") ||
		containsTokenSequence(tokens, "master", "class")
}

func matchesWorkshopFormat(tokens []string) bool {
	hasWorkshop := containsAnyToken(tokens,
		"интенсив", "интенсива", "интенсивы", "интенсивов", "интенсиве",
		"интенсивный", "интенсивная", "интенсивное", "интенсивные",
		"intensive", "intensives",
		"воркшоп", "воркшопа", "воркшопы", "воркшопов", "workshop", "workshops",
		"буткемп", "буткемпа", "буткемпы", "буткемпов", "bootcamp", "bootcamps",
	)
	if hasWorkshop {
		return true
	}
	hasPracticum := containsAnyToken(tokens,
		"практикум", "практикума", "практикумы", "практикумов", "практикуме",
	)
	return hasPracticum && !containsTokenSequence(tokens, "яндекс", "практикум")
}

func matchesMarathonFormat(tokens []string) bool {
	return containsAnyToken(tokens,
		"марафон", "марафона", "марафоны", "марафонов", "марафоне",
		"marathon", "marathons",
		"челлендж", "челленджа", "челленджи", "челленджей",
		"challenge", "challenges",
	)
}

func matchesCourseFormat(tokens []string) bool {
	return containsAnyToken(tokens,
		"курс", "курса", "курсы", "курсу", "курсом", "курсе", "курсов",
		"курсами", "курсах", "course", "courses",
		"видеокурс", "видеокурса", "видеокурсы", "видеокурсов",
		"онлайнкурс", "онлайнкурса", "онлайнкурсы", "онлайнкурсов",
		"профессия", "профессии", "профессию", "профессией", "профессий",
		"обучение", "обучения", "обучению", "обучением", "обучении",
		"тренинг", "тренинга", "тренинги", "тренингов", "тренинге",
		"training", "trainings",
	)
}

func matchesTemplatesAssetsFormat(tokens []string) bool {
	return containsAnyToken(tokens,
		"материал", "материала", "материалы", "материалу", "материалом",
		"материале", "материалов", "материалами", "материалах",
		"шаблон", "шаблона", "шаблоны", "шаблону", "шаблоном", "шаблоне",
		"шаблонов", "шаблонами", "шаблонах", "template", "templates",
		"пресет", "пресета", "пресеты", "пресетов", "пресетами", "preset", "presets",
		"исходник", "исходника", "исходники", "исходников", "исходниками",
		"asset", "assets",
		"плагин", "плагина", "плагины", "плагинов", "плагинами", "plugin", "plugins",
	)
}

func matchesBundleLibraryFormat(tokens []string) bool {
	return containsAnyToken(tokens,
		"бандл", "бандла", "бандлы", "бандлов", "bundle", "bundles",
		"библиотека", "библиотеки", "library", "libraries",
		"коллекция", "коллекции", "collection", "collections",
		"пакет", "пакеты", "pack", "packs",
	)
}

func matchesClubMembershipFormat(tokens []string) bool {
	return containsAnyToken(tokens,
		"клуб", "клуба", "клубы", "club", "membership", "подписка", "подписки",
		"сообщество", "сообщества", "community",
	)
}

func containsAnyToken(tokens []string, values ...string) bool {
	return slices.ContainsFunc(tokens, func(token string) bool {
		return slices.Contains(values, token)
	})
}

func containsTokenSequence(tokens []string, sequence ...string) bool {
	if len(sequence) == 0 || len(sequence) > len(tokens) {
		return false
	}
	for index := 0; index <= len(tokens)-len(sequence); index++ {
		if slices.Equal(tokens[index:index+len(sequence)], sequence) {
			return true
		}
	}
	return false
}

func normalizeSearchText(value string) string {
	value = strings.ToLower(strings.ReplaceAll(value, "ё", "е"))
	value = urlPattern.ReplaceAllString(value, " ")
	value = nonSearchChars.ReplaceAllString(value, " ")
	return strings.TrimSpace(spacePattern.ReplaceAllString(value, " "))
}

func matchesCategory(text string, keywords []string) bool {
	tokens := strings.Fields(text)
	for _, keyword := range keywords {
		prefix := strings.HasSuffix(keyword, "*")
		keyword = strings.TrimSuffix(keyword, "*")
		keyword = normalizeSearchText(keyword)
		if keyword == "" {
			continue
		}
		if prefix {
			if slices.ContainsFunc(tokens, func(token string) bool {
				return strings.HasPrefix(token, keyword)
			}) {
				return true
			}
			continue
		}
		if len([]rune(keyword)) <= 4 && !strings.Contains(keyword, " ") {
			if slices.Contains(tokens, keyword) {
				return true
			}
			continue
		}
		if !strings.Contains(keyword, " ") {
			if slices.ContainsFunc(tokens, func(token string) bool {
				return strings.HasPrefix(token, keyword)
			}) {
				return true
			}
			continue
		}
		if strings.Contains(" "+text+" ", " "+keyword+" ") {
			return true
		}
	}
	return false
}

func validateSourceCounts(source sourceExport) error {
	expectedMessages := source.Stats.Retrieval.ExportedMessageCount
	expectedEntries := source.Stats.Parsing.CatalogEntryCount
	expectedLinks := source.Stats.Parsing.ParsedLinkCount
	expectedPasswords := source.Stats.Parsing.PasswordValueCount

	actualLinks := 0
	actualPasswords := 0
	for _, entry := range source.CatalogEntries {
		actualLinks += len(entry.Links)
		actualPasswords += len(entry.Passwords)
	}
	if expectedMessages != len(source.Messages) ||
		expectedEntries != len(source.CatalogEntries) ||
		expectedLinks != actualLinks ||
		expectedPasswords != actualPasswords {
		return fmt.Errorf(
			"source counts messages=%d entries=%d links=%d passwords=%d do not match payload messages=%d entries=%d links=%d passwords=%d",
			expectedMessages,
			expectedEntries,
			expectedLinks,
			expectedPasswords,
			len(source.Messages),
			len(source.CatalogEntries),
			actualLinks,
			actualPasswords,
		)
	}
	return nil
}
