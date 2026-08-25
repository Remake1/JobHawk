package telegram

import (
	"strings"
	"testing"

	"jobhawk/internal/ashby"
	"jobhawk/internal/greenhouse"
	"jobhawk/internal/jobs"
	"jobhawk/internal/searchqueries"
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

func TestMainMenuScreenHasButtons(t *testing.T) {
	menu := mainMenuScreen()
	if len(menu.entities) == 0 || menu.keyboard == nil || len(menu.keyboard.InlineKeyboard) != 2 {
		t.Fatalf("mainMenuScreen() = %+v", menu)
	}
	if got := menu.keyboard.InlineKeyboard[0][0].CallbackData; got != callbackList {
		t.Fatalf("first callback = %q", got)
	}
}

func TestCreationSourceButtonsIncludeProviderEmojis(t *testing.T) {
	prompt := creationPromptScreen(creationSession{step: creationSource}, "")
	want := []string{"🐸 Greenhouse", "🦋 Workday", "🔮 AshbyHQ"}
	for i, label := range want {
		if got := prompt.keyboard.InlineKeyboard[i][0].Text; got != label {
			t.Errorf("provider button %d = %q, want %q", i, got, label)
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
	})
	buttons := detail.keyboard.InlineKeyboard[0]
	if buttons[0].CallbackData != "q:run:42" || buttons[1].CallbackData != "q:edit:42" {
		t.Fatalf("action callbacks = %q, %q", buttons[0].CallbackData, buttons[1].CallbackData)
	}
	if got := detail.keyboard.InlineKeyboard[1][0].CallbackData; got != "q:delete:42" {
		t.Fatalf("delete callback = %q", got)
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
