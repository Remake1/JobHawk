package telegram

import (
	"strings"
	"testing"

	"jobhawk/internal/greenhouse"
	"jobhawk/internal/jobs"
	"jobhawk/internal/searchqueries"
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

func TestMainMenuScreenHasButtons(t *testing.T) {
	menu := mainMenuScreen()
	if len(menu.entities) == 0 || menu.keyboard == nil || len(menu.keyboard.InlineKeyboard) != 2 {
		t.Fatalf("mainMenuScreen() = %+v", menu)
	}
	if got := menu.keyboard.InlineKeyboard[0][0].CallbackData; got != callbackList {
		t.Fatalf("first callback = %q", got)
	}
}

func TestQueryDetailScreenActionsUseQueryID(t *testing.T) {
	detail := queryDetailScreen(searchqueries.GreenhouseQuery{
		ID:   42,
		Name: "Point72",
		Filters: greenhouse.Filters{
			BoardToken: "point72",
			Location:   "Warsaw, Poland",
			TitleWords: []string{"Software"},
		},
	})
	buttons := detail.keyboard.InlineKeyboard[0]
	if buttons[0].CallbackData != "q:run:42" || buttons[1].CallbackData != "q:delete:42" {
		t.Fatalf("action callbacks = %q, %q", buttons[0].CallbackData, buttons[1].CallbackData)
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
}

func TestCreationPromptOffersOnlyValidSkips(t *testing.T) {
	location := creationPromptScreen(creationSession{step: creationLocation}, "")
	if location.keyboard.InlineKeyboard[0][0].CallbackData != callbackSkipLoc {
		t.Fatal("location step did not offer skip")
	}

	titleWithoutLocation := creationPromptScreen(creationSession{step: creationTitleWords}, "")
	if len(titleWithoutLocation.keyboard.InlineKeyboard) != 1 || titleWithoutLocation.keyboard.InlineKeyboard[0][0].CallbackData != callbackCancel {
		t.Fatal("title step allowed an unfiltered search")
	}

	titleWithLocation := creationPromptScreen(creationSession{
		step:  creationTitleWords,
		draft: creationDraft{Filters: greenhouse.Filters{Location: "Warsaw, Poland"}},
	}, "")
	if titleWithLocation.keyboard.InlineKeyboard[0][0].CallbackData != callbackSkipTitle {
		t.Fatal("title step did not allow skip when location is set")
	}
}

func TestTruncateButtonTextHandlesWhitespaceAndUnicode(t *testing.T) {
	got := truncateButtonText(strings.Repeat(" ", 50) + strings.Repeat("Ż", 50))
	if len([]rune(got)) != 42 || !strings.HasSuffix(got, "…") {
		t.Fatalf("truncateButtonText() = %q", got)
	}
}
