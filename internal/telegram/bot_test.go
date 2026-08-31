package telegram

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"jobhawk/internal/ashby"
	"jobhawk/internal/daily"
	"jobhawk/internal/greenhouse"
	"jobhawk/internal/hourly"
	"jobhawk/internal/jobs"
	"jobhawk/internal/searchqueries"
	"jobhawk/internal/textsearch"
	"jobhawk/internal/workday"
)

func TestFormatJob(t *testing.T) {
	got := formatJob(jobs.Job{
		Title: "Go Engineer", Company: "Acme", Location: "Remote", URL: "https://example.com/jobs/1",
	})
	want := "New job opening\nGo Engineer at Acme\nRemote\nhttps://example.com/jobs/1"
	if got != want {
		t.Fatalf("formatJob() = %q, want %q", got, want)
	}
}

func TestAccessRestrictedMessageIncludesChatIDConfiguration(t *testing.T) {
	got := accessRestrictedMessage(-1001234567890)
	want := "Access is restricted to the configured Telegram chat.\n\nTo allow this chat, set:\nTELEGRAM_CHAT_ID=-1001234567890"
	if got != want {
		t.Fatalf("accessRestrictedMessage() = %q, want %q", got, want)
	}
}

func TestFormatDailyDigestWithNoNewJobs(t *testing.T) {
	got := formatDailyDigest(daily.Report{QueryCount: 3})
	if got != "Daily job report\n\nNo new jobs." {
		t.Fatalf("formatDailyDigest() = %q", got)
	}
}

func TestFormatDailyDigestListsNewJobsOnce(t *testing.T) {
	got := formatDailyDigest(daily.Report{
		QueryCount: 2,
		NewJobs: []jobs.Job{
			{Title: "Go Engineer", Company: "Acme", Location: "Remote", URL: "https://example.com/1"},
			{Title: "Platform Engineer", Company: "Beta"},
		},
	})
	want := "Daily job report\n\n2 new jobs found:\n\n1. Go Engineer at Acme\nRemote\nhttps://example.com/1\n2. Platform Engineer at Beta"
	if got != want {
		t.Fatalf("formatDailyDigest() = %q, want %q", got, want)
	}
}

func TestFormatDailyDigestMentionsFailures(t *testing.T) {
	got := formatDailyDigest(daily.Report{
		QueryCount: 2,
		Failures:   []daily.QueryFailure{{QueryName: "Broken"}},
	})
	if !strings.Contains(got, "No new jobs.") || !strings.Contains(got, "1 of 2 searches failed") {
		t.Fatalf("formatDailyDigest() = %q", got)
	}
}

func TestCommandFromText(t *testing.T) {
	tests := []struct {
		text string
		want string
		ok   bool
	}{
		{text: "", ok: false},
		{text: "   ", ok: false},
		{text: "/STATUS", want: "/status", ok: true},
		{text: "/start@JobHawkBot payload", want: "/start", ok: true},
	}

	for _, tt := range tests {
		got, ok := commandFromText(tt.text)
		if got != tt.want || ok != tt.ok {
			t.Errorf("commandFromText(%q) = (%q, %v), want (%q, %v)", tt.text, got, ok, tt.want, tt.ok)
		}
	}
}

func TestParseCommandPreservesArguments(t *testing.T) {
	command, args, ok := parseCommand(" /SEARCH@JobHawkBot Point72 SWE Internship 2027 ")
	if !ok || command != "/search" || args != "Point72 SWE Internship 2027" {
		t.Fatalf("parseCommand() = (%q, %q, %v)", command, args, ok)
	}
}

func TestParseGreenhouseArgs(t *testing.T) {
	name, filters, err := parseGreenhouseArgs("Point72 SWE Internship 2027 | point72 | Warsaw, Poland | 2027, Internship, Software")
	if err != nil {
		t.Fatalf("parseGreenhouseArgs() error = %v", err)
	}
	if name != "Point72 SWE Internship 2027" || filters.BoardToken != "point72" || filters.Location != "Warsaw, Poland" {
		t.Fatalf("parseGreenhouseArgs() = %q, %+v", name, filters)
	}
	if len(filters.TitleWords) != 3 || filters.TitleWords[1] != "Internship" {
		t.Fatalf("TitleWords = %v", filters.TitleWords)
	}
}

func TestParseGreenhouseArgsRejectsMissingFields(t *testing.T) {
	if _, _, err := parseGreenhouseArgs("name point72 Warsaw"); err == nil {
		t.Fatal("parseGreenhouseArgs() expected an error")
	}
}

func TestParseAshbyArgs(t *testing.T) {
	name, filters, err := parseAshbyArgs("Snowflake Software | https://jobs.ashbyhq.com/snowflake/fc1923c1-b151-4458-a792-40d58331a5be | Warsaw, Poland | Software, Engineer")
	if err != nil {
		t.Fatalf("parseAshbyArgs() error = %v", err)
	}
	if name != "Snowflake Software" || filters.JobBoard != "snowflake" || filters.Location != "Warsaw, Poland" {
		t.Fatalf("parseAshbyArgs() = %q, %+v", name, filters)
	}
	if len(filters.TitleWords) != 2 || filters.TitleWords[1] != "Engineer" {
		t.Fatalf("TitleWords = %v", filters.TitleWords)
	}
}

func TestParseWorkdayArgs(t *testing.T) {
	name, filters, err := parseWorkdayArgs("State Street Poland | https://statestreet.wd1.myworkdayjobs.com/Global/job/Munich-Germany/Working-Student_R-1/apply | Poland | Working, Student")
	if err != nil {
		t.Fatalf("parseWorkdayArgs() error = %v", err)
	}
	if name != "State Street Poland" || filters.Tenant != "statestreet" || filters.Site != "Global" || filters.Location != "Poland" {
		t.Fatalf("parseWorkdayArgs() = %q, %+v", name, filters)
	}
	if len(filters.TitleWords) != 2 || filters.TitleWords[1] != "Student" {
		t.Fatalf("TitleWords = %v", filters.TitleWords)
	}
}

func TestParseTextArgsPreservesFilteredURLAndFragment(t *testing.T) {
	name, filters, err := parseTextArgs("Google Poland | https://example.com/jobs?location=Poland&level=intern | Search again or try updating your filters")
	if err != nil {
		t.Fatalf("parseTextArgs() error = %v", err)
	}
	if name != "Google Poland" || filters.URL != "https://example.com/jobs?location=Poland&level=intern" || filters.NoJobsText != "Search again or try updating your filters" {
		t.Fatalf("parseTextArgs() = %q, %+v", name, filters)
	}
}

func TestParseTextArgsAcceptsOptionalCSRMode(t *testing.T) {
	name, filters, err := parseTextArgs("Dynamic board | https://example.com/jobs | No matching jobs | csr")
	if err != nil {
		t.Fatalf("parseTextArgs() error = %v", err)
	}
	if name != "Dynamic board" || !filters.ClientSideRender {
		t.Fatalf("parseTextArgs() = %q, %+v", name, filters)
	}
}

func TestParseTextArgsPreservesPipesInEmptyText(t *testing.T) {
	_, filters, err := parseTextArgs("Board | https://example.com/jobs | No jobs | change filters")
	if err != nil {
		t.Fatalf("parseTextArgs() error = %v", err)
	}
	if filters.NoJobsText != "No jobs | change filters" || filters.ClientSideRender {
		t.Fatalf("parseTextArgs() filters = %+v", filters)
	}
}

func TestMainMenuScreenHasButtons(t *testing.T) {
	menu := mainMenuScreen()
	if len(menu.entities) == 0 || menu.keyboard == nil || len(menu.keyboard.InlineKeyboard) != 3 {
		t.Fatalf("mainMenuScreen() = %+v", menu)
	}
	if got := menu.keyboard.InlineKeyboard[0][0].CallbackData; got != callbackList {
		t.Fatalf("first callback = %q", got)
	}
	if got := menu.keyboard.InlineKeyboard[2][0].CallbackData; got != callbackRunDaily {
		t.Fatalf("daily callback = %q", got)
	}
}

func TestCreationSourceButtonsIncludeProviderEmojis(t *testing.T) {
	prompt := creationPromptScreen(creationSession{step: creationSource}, "")
	want := []string{"🐸 Greenhouse", "🦋 Workday", "🔮 AshbyHQ", "📝 Text search"}
	for i, label := range want {
		if got := prompt.keyboard.InlineKeyboard[i][0].Text; got != label {
			t.Errorf("provider button %d = %q, want %q", i, got, label)
		}
	}
}

func TestTextQueryDetailOffersRenderingAction(t *testing.T) {
	filters := textsearch.Filters{URL: "https://example.com/jobs?location=Poland", NoJobsText: "No matching jobs"}
	detail := queryDetailScreen(searchqueries.Query{
		ID: 44, Name: "Google Poland", SourceType: searchqueries.SourceText, Text: &filters,
	}, nil)
	if len(detail.keyboard.InlineKeyboard[0]) != 2 || detail.keyboard.InlineKeyboard[0][0].CallbackData != "q:run:44" || detail.keyboard.InlineKeyboard[0][1].CallbackData != "q:edit_rendering:44" {
		t.Fatalf("text query actions = %+v", detail.keyboard.InlineKeyboard[0])
	}
	if !strings.Contains(detail.text, filters.URL) || !strings.Contains(detail.text, filters.NoJobsText) {
		t.Fatalf("text query detail = %q", detail.text)
	}
}

func TestTextRenderingScreenAllowsSwitchingBothWays(t *testing.T) {
	for _, clientSideRender := range []bool{false, true} {
		filters := textsearch.Filters{
			URL: "https://example.com/jobs", NoJobsText: "No matching jobs", ClientSideRender: clientSideRender,
		}
		rendering := textRenderingScreen(searchqueries.Query{
			ID: 44, Name: "Dynamic board", SourceType: searchqueries.SourceText, Text: &filters,
		})
		if got := rendering.keyboard.InlineKeyboard[0][0].CallbackData; got != "q:render_ssr:44" {
			t.Fatalf("SSR callback = %q", got)
		}
		if got := rendering.keyboard.InlineKeyboard[1][0].CallbackData; got != "q:render_csr:44" {
			t.Fatalf("CSR callback = %q", got)
		}
		selectedRow := 0
		if clientSideRender {
			selectedRow = 1
		}
		if !strings.HasPrefix(rendering.keyboard.InlineKeyboard[selectedRow][0].Text, "✓ ") {
			t.Fatalf("selected rendering button = %q", rendering.keyboard.InlineKeyboard[selectedRow][0].Text)
		}
	}
}

func TestQueryDetailScreenActionsUseQueryID(t *testing.T) {
	filters := greenhouse.Filters{
		BoardToken: "point72",
		Location:   "Warsaw, Poland",
		TitleWords: []string{"Software"},
	}
	detail := queryDetailScreen(searchqueries.Query{
		ID: 42, Name: "Point72", SourceType: searchqueries.SourceGreenhouse, Greenhouse: &filters,
	}, nil)
	buttons := detail.keyboard.InlineKeyboard[0]
	if buttons[0].CallbackData != "q:run:42" || buttons[1].CallbackData != "q:edit:42" {
		t.Fatalf("action callbacks = %q, %q", buttons[0].CallbackData, buttons[1].CallbackData)
	}
	if got := detail.keyboard.InlineKeyboard[1][0].CallbackData; got != "q:hourly_create:42" {
		t.Fatalf("hourly callback = %q", got)
	}
	if got := detail.keyboard.InlineKeyboard[2][0].CallbackData; got != "q:delete:42" {
		t.Fatalf("delete callback = %q", got)
	}
}

func TestQueryDetailScreenShowsExistingHourlyAlert(t *testing.T) {
	filters := greenhouse.Filters{BoardToken: "point72", Location: "Warsaw"}
	detail := queryDetailScreen(searchqueries.Query{
		ID: 42, Name: "Point72", SourceType: searchqueries.SourceGreenhouse, Greenhouse: &filters,
	}, &hourly.Subscription{
		SearchDate: time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC), IntervalMinutes: 30,
	})
	if got := detail.keyboard.InlineKeyboard[1][0].CallbackData; got != "q:hourly_delete:42" {
		t.Fatalf("hourly callback = %q", got)
	}
	if !strings.Contains(detail.text, "2026-08-25 every 30 minutes") {
		t.Fatalf("detail text = %q", detail.text)
	}
}

func TestFirstHourlyRun(t *testing.T) {
	location := time.FixedZone("test", -5*60*60)
	now := time.Date(2026, 8, 25, 11, 42, 0, 0, location)
	if got := firstHourlyRun(localDate(now, location), now, location); !got.Equal(now) {
		t.Fatalf("today first run = %v, want %v", got, now)
	}
	future := time.Date(2026, 8, 27, 0, 0, 0, 0, location)
	if got := firstHourlyRun(future, now, location); !got.Equal(future) {
		t.Fatalf("future first run = %v, want %v", got, future)
	}
}

func TestFormatHourlyResults(t *testing.T) {
	got := formatHourlyResults(searchqueries.Query{Name: "Warsaw Go"}, []jobs.Job{{
		Title: "Go Engineer", Company: "Acme", Location: "Warsaw", URL: "https://example.com/1",
	}})
	if !strings.Contains(got, "Hourly job alert\nWarsaw Go") || !strings.Contains(got, "1 matching job found") || !strings.Contains(got, "Go Engineer at Acme") {
		t.Fatalf("formatHourlyResults() = %q", got)
	}
}

func TestQueryListLabelsShowProviderCompanyAndName(t *testing.T) {
	ashbyFilters := ashby.Filters{JobBoard: "snowflake", Location: "Warsaw, Poland"}
	greenhouseFilters := greenhouse.Filters{BoardToken: "point72", Location: "Warsaw, Poland"}
	workdayFilters := workday.Filters{Tenant: "statestreet"}
	queries := []searchqueries.Query{
		{ID: 1, Name: "Snowflake Software", SourceType: searchqueries.SourceAshby, Ashby: &ashbyFilters},
		{ID: 2, Name: "Point72 Internship", SourceType: searchqueries.SourceGreenhouse, Greenhouse: &greenhouseFilters},
		{ID: 3, Name: "State Street Student", SourceType: searchqueries.SourceWorkday, Workday: &workdayFilters},
	}
	want := []string{
		"🔮 | snowflake | Snowflake Software",
		"🐸 | point72 | Point72 Internship",
		"🦋 | statestreet | State Street Student",
	}
	list := queryListScreen(queries)
	for i, label := range want {
		if got := list.keyboard.InlineKeyboard[i][0].Text; got != label {
			t.Errorf("query button %d = %q, want %q", i, got, label)
		}
	}
}

func TestTextQueryListLabelOmitsWWWHostnamePrefix(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{url: "https://google.com/jobs?location=Poland", want: "📝 | google.com | Google Poland"},
		{url: "https://www.google.com/jobs?location=Poland", want: "📝 | google.com | Google Poland"},
	}
	for _, tt := range tests {
		filters := textsearch.Filters{URL: tt.url, NoJobsText: "No jobs"}
		query := searchqueries.Query{Name: "Google Poland", SourceType: searchqueries.SourceText, Text: &filters}
		if got := queryListLabel(query); got != tt.want {
			t.Errorf("queryListLabel(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestQueryListPaginatesManyQueries(t *testing.T) {
	queries := make([]searchqueries.Query, 23)
	filters := greenhouse.Filters{BoardToken: "example"}
	for i := range queries {
		queries[i] = searchqueries.Query{
			ID: int64(i + 1), Name: "Query " + strconv.Itoa(i+1),
			SourceType: searchqueries.SourceGreenhouse, Greenhouse: &filters,
		}
	}

	first := queryListPageScreen(queries, 0)
	if got := len(first.keyboard.InlineKeyboard); got != queryListPageSize+3 {
		t.Fatalf("first page row count = %d, want %d", got, queryListPageSize+3)
	}
	if got := first.keyboard.InlineKeyboard[queryListPageSize][0].CallbackData; got != "m:list:1" {
		t.Fatalf("first page next callback = %q", got)
	}
	if !strings.Contains(first.text, "23 saved · Page 1 of 3") {
		t.Fatalf("first page text = %q", first.text)
	}

	last := queryListPageScreen(queries, 2)
	if got := len(last.keyboard.InlineKeyboard); got != 6 {
		t.Fatalf("last page row count = %d, want 6", got)
	}
	if got := last.keyboard.InlineKeyboard[3][0].CallbackData; got != "m:list:1" {
		t.Fatalf("last page previous callback = %q", got)
	}
	if got := last.keyboard.InlineKeyboard[0][0].CallbackData; got != "q:view:21" {
		t.Fatalf("last page first query callback = %q", got)
	}
}

func TestQueryListPageClampsAfterDeletion(t *testing.T) {
	queries := make([]searchqueries.Query, 20)
	page := queryListPageScreen(queries, 2)
	if !strings.Contains(page.text, "Page 2 of 2") {
		t.Fatalf("clamped page text = %q", page.text)
	}
}

func TestParseQueryListPageCallback(t *testing.T) {
	tests := []struct {
		data string
		page int
		ok   bool
	}{
		{data: callbackList, page: 0, ok: true},
		{data: "m:list:2", page: 2, ok: true},
		{data: "m:list:-1", ok: false},
		{data: "m:list:nope", ok: false},
		{data: "m:home", ok: false},
	}
	for _, tt := range tests {
		page, ok := parseQueryListPageCallback(tt.data)
		if page != tt.page || ok != tt.ok {
			t.Errorf("parseQueryListPageCallback(%q) = (%d, %v), want (%d, %v)", tt.data, page, ok, tt.page, tt.ok)
		}
	}
}

func TestParseQueryCallback(t *testing.T) {
	action, id, ok := parseQueryCallback("q:confirm_delete:73")
	if !ok || action != "confirm_delete" || id != 73 {
		t.Fatalf("parseQueryCallback() = (%q, %d, %v)", action, id, ok)
	}
	if _, _, ok := parseQueryCallback("q:unknown:73"); ok {
		t.Fatal("parseQueryCallback() accepted an unknown action")
	}
	if action, id, ok := parseQueryCallback("q:edit_tags:73"); !ok || action != "edit_tags" || id != 73 {
		t.Fatalf("parseQueryCallback(edit tags) = (%q, %d, %v)", action, id, ok)
	}
	if action, id, ok := parseQueryCallback("q:render_csr:73"); !ok || action != "render_csr" || id != 73 {
		t.Fatalf("parseQueryCallback(render CSR) = (%q, %d, %v)", action, id, ok)
	}
}

func TestQueryEditorExposesOnlyLocationAndTags(t *testing.T) {
	filters := greenhouse.Filters{
		BoardToken: "point72", Location: "Warsaw, Poland", TitleWords: []string{"Software", "Intern"},
	}
	editor := queryEditorScreen(searchqueries.Query{
		ID: 42, Name: "Point72", SourceType: searchqueries.SourceGreenhouse, Greenhouse: &filters,
	})
	buttons := editor.keyboard.InlineKeyboard[0]
	if buttons[0].CallbackData != "q:edit_location:42" || buttons[1].CallbackData != "q:edit_tags:42" {
		t.Fatalf("editor callbacks = %q, %q", buttons[0].CallbackData, buttons[1].CallbackData)
	}
	if !strings.Contains(editor.text, "query name and job board cannot be changed") || !strings.Contains(editor.text, "Software, Intern") {
		t.Fatalf("editor text = %q", editor.text)
	}
}

func TestEditPromptOffersClearOnlyWhenAnotherFilterRemains(t *testing.T) {
	locationOnly := greenhouse.Filters{BoardToken: "point72", Location: "Warsaw"}
	prompt := editPromptScreen(editSession{
		field: editLocation,
		query: searchqueries.Query{ID: 42, SourceType: searchqueries.SourceGreenhouse, Greenhouse: &locationOnly},
	}, "")
	if len(prompt.keyboard.InlineKeyboard) != 1 || prompt.keyboard.InlineKeyboard[0][0].CallbackData != "q:edit:42" {
		t.Fatalf("location-only prompt unexpectedly allows clear: %+v", prompt.keyboard.InlineKeyboard)
	}

	both := greenhouse.Filters{BoardToken: "point72", Location: "Warsaw", TitleWords: []string{"Engineer"}}
	prompt = editPromptScreen(editSession{
		field: editLocation,
		query: searchqueries.Query{ID: 42, SourceType: searchqueries.SourceGreenhouse, Greenhouse: &both},
	}, "")
	if got := prompt.keyboard.InlineKeyboard[0][0].CallbackData; got != "q:clear_location:42" {
		t.Fatalf("clear callback = %q", got)
	}
}

func TestCreationPromptOffersOnlyValidSkips(t *testing.T) {
	location := creationPromptScreen(creationSession{step: creationLocation, draft: creationDraft{SourceType: searchqueries.SourceGreenhouse}}, "")
	if location.keyboard.InlineKeyboard[0][0].CallbackData != callbackSkipLoc {
		t.Fatal("location step did not offer skip")
	}

	titleWithoutLocation := creationPromptScreen(creationSession{step: creationTitleWords}, "")
	if len(titleWithoutLocation.keyboard.InlineKeyboard) != 1 || titleWithoutLocation.keyboard.InlineKeyboard[0][0].CallbackData != callbackCancel {
		t.Fatal("title step allowed an unfiltered search")
	}

	titleWithLocation := creationPromptScreen(creationSession{
		step:  creationTitleWords,
		draft: creationDraft{SourceType: searchqueries.SourceGreenhouse, Greenhouse: greenhouse.Filters{Location: "Warsaw, Poland"}},
	}, "")
	if titleWithLocation.keyboard.InlineKeyboard[0][0].CallbackData != callbackSkipTitle {
		t.Fatal("title step did not allow skip when location is set")
	}
}

func TestTextCreationPromptAsksForRenderingModeWithServerDefault(t *testing.T) {
	prompt := creationPromptScreen(creationSession{
		step:  creationRender,
		draft: creationDraft{SourceType: searchqueries.SourceText},
	}, "")
	if !strings.Contains(prompt.text, "render its job results in JavaScript") {
		t.Fatalf("prompt text = %q", prompt.text)
	}
	if got := prompt.keyboard.InlineKeyboard[0][0].CallbackData; got != callbackSSR {
		t.Fatalf("default rendering callback = %q", got)
	}
	if got := prompt.keyboard.InlineKeyboard[1][0].CallbackData; got != callbackCSR {
		t.Fatalf("CSR rendering callback = %q", got)
	}
}

func TestWorkdayCreationPromptExplainsPartialLocation(t *testing.T) {
	prompt := creationPromptScreen(creationSession{
		step: creationLocation,
		draft: creationDraft{
			SourceType: searchqueries.SourceWorkday,
			Workday:    workday.Filters{Host: "statestreet.wd1.myworkdayjobs.com", Tenant: "statestreet", Site: "Global"},
		},
	}, "")
	if !strings.Contains(prompt.text, "Poland matches Krakow, Poland") {
		t.Fatalf("prompt text = %q", prompt.text)
	}
}

func TestAshbyCreationPromptAcceptsJobURL(t *testing.T) {
	prompt := creationPromptScreen(creationSession{
		step: creationBoard,
		draft: creationDraft{
			SourceType: searchqueries.SourceAshby,
			Ashby:      ashby.Filters{},
		},
	}, "")
	if !strings.Contains(prompt.text, "jobs.ashbyhq.com/snowflake/") {
		t.Fatalf("prompt text = %q", prompt.text)
	}
}

func TestSearchLoadingScreenShowsProviderAndHasNoButtons(t *testing.T) {
	loading := searchLoadingScreen(searchqueries.Query{
		ID: 9, Name: "State Street Software", SourceType: searchqueries.SourceWorkday,
	})
	if !strings.Contains(loading.text, "Searching 🦋 Workday") || !strings.Contains(loading.text, "update automatically") {
		t.Fatalf("loading text = %q", loading.text)
	}
	if loading.keyboard != nil {
		t.Fatalf("loading keyboard = %+v", loading.keyboard)
	}
}

func TestSearchErrorScreenAllowsRetry(t *testing.T) {
	failure := searchErrorScreen(searchqueries.Query{
		ID: 9, Name: "State Street Software", SourceType: searchqueries.SourceWorkday,
	})
	if !strings.Contains(failure.text, "🦋 Workday search failed") {
		t.Fatalf("failure text = %q", failure.text)
	}
	if got := failure.keyboard.InlineKeyboard[0][0].CallbackData; got != "q:run:9" {
		t.Fatalf("retry callback = %q", got)
	}
}

func TestTruncateButtonTextHandlesWhitespaceAndUnicode(t *testing.T) {
	got := truncateButtonText(strings.Repeat(" ", 50) + strings.Repeat("Ż", 50))
	if len([]rune(got)) != 42 || !strings.HasSuffix(got, "…") {
		t.Fatalf("truncateButtonText() = %q", got)
	}
}
