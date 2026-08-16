package telegram

import (
	"testing"

	"jobhawk/internal/jobs"
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
