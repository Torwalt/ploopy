package harness

import (
	"testing"
	"time"
)

func TestTheLastMarkerNearTheEndWins(t *testing.T) {
	text := "working\nDONE\nmore thinking\nBLOCKED the tests will not build\n"
	marker, reason := ParseMarker(text)
	if marker != Blocked || reason != "the tests will not build" {
		t.Fatalf("got %q %q", marker, reason)
	}
}

func TestAMarkerSurvivesTheDecorationAHarnessAddsToIt(t *testing.T) {
	for _, line := range []string{"DONE", "**DONE**", "- DONE", "> `DONE`", "#### DONE"} {
		if marker, _ := ParseMarker(line); marker != Done {
			t.Fatalf("%q parsed as %q", line, marker)
		}
	}
}

func TestProseThatMentionsAMarkerIsNotOne(t *testing.T) {
	text := "I would report DONE but the build fails.\nStill working.\n"
	if marker, _ := ParseMarker(text); marker != None {
		t.Fatalf("prose parsed as %q", marker)
	}
}

func TestAMarkerFarAboveTheEndIsNotRead(t *testing.T) {
	text := "DONE\n"
	for i := 0; i < 40; i++ {
		text += "noise\n"
	}
	if marker, _ := ParseMarker(text); marker != None {
		t.Fatalf("a marker 40 lines up was read as %q", marker)
	}
}

func TestAnsiDecorationDoesNotHideAMarker(t *testing.T) {
	if marker, _ := ParseMarker("\x1b[1mDONE\x1b[0m\n"); marker != Done {
		t.Fatal("ANSI escapes hid the marker")
	}
}

func TestAResetTimeIsWaitedFor(t *testing.T) {
	now := time.Date(2026, 10, 2, 14, 0, 0, 0, time.UTC)
	limit := RateLimitFromText("You have reached your usage limit. It resets at 4pm.", now)
	if limit == nil {
		t.Fatal("a usage limit was not recognised")
	}
	if wait := limit.ResetAt.Sub(now); wait < 2*time.Hour || wait > 2*time.Hour+5*time.Minute {
		t.Fatalf("wait %s", wait)
	}
}

func TestAResetEarlierInTheDayMeansTomorrow(t *testing.T) {
	now := time.Date(2026, 10, 2, 22, 0, 0, 0, time.UTC)
	limit := RateLimitFromText("hit the limit; resets at 9am", now)
	if limit == nil {
		t.Fatal("a usage limit was not recognised")
	}
	if wait := limit.ResetAt.Sub(now); wait < 10*time.Hour || wait > 12*time.Hour {
		t.Fatalf("wait %s", wait)
	}
}

func TestALimitWithoutATimeHasNoReset(t *testing.T) {
	limit := RateLimitFromText("you have exceeded the rate limit", time.Now())
	if limit == nil {
		t.Fatal("a usage limit was not recognised")
	}
	if !limit.ResetAt.IsZero() {
		t.Fatalf("reset %s, want none", limit.ResetAt)
	}
}

func TestOrdinaryOutputIsNotALimit(t *testing.T) {
	if limit := RateLimitFromText("the test suite passed\nDONE\n", time.Now()); limit != nil {
		t.Fatalf("ordinary output read as a limit: %+v", limit)
	}
}

// DeepSeek bills double on weekday mornings, UTC.
var deepseek = []Window{
	{Days: []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday},
		Start: 60, End: 240},
	{Days: []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday},
		Start: 360, End: 600},
}

func TestPeakIsRecognisedAndItsEndReported(t *testing.T) {
	// Friday 2026-10-02, 02:30 UTC is inside the first window.
	inPeak, until := Peak(deepseek, time.Date(2026, 10, 2, 2, 30, 0, 0, time.UTC))
	if !inPeak {
		t.Fatal("02:30 on a Friday should be peak")
	}
	if want := time.Date(2026, 10, 2, 4, 0, 0, 0, time.UTC); !until.Equal(want) {
		t.Fatalf("peak ends %s, want %s", until, want)
	}
}

func TestTheGapBetweenWindowsIsOffPeak(t *testing.T) {
	if inPeak, _ := Peak(deepseek, time.Date(2026, 10, 2, 5, 0, 0, 0, time.UTC)); inPeak {
		t.Fatal("05:00 sits between the windows and is off-peak")
	}
}

func TestAWindowEdgeIsOffPeakAtItsEnd(t *testing.T) {
	if inPeak, _ := Peak(deepseek, time.Date(2026, 10, 2, 4, 0, 0, 0, time.UTC)); inPeak {
		t.Fatal("the window ends at 04:00 and does not include it")
	}
	if inPeak, _ := Peak(deepseek, time.Date(2026, 10, 2, 1, 0, 0, 0, time.UTC)); !inPeak {
		t.Fatal("the window starts at 01:00 and includes it")
	}
}

func TestTheWeekendIsAlwaysOffPeak(t *testing.T) {
	// Saturday 2026-10-03, 02:30 UTC.
	if inPeak, _ := Peak(deepseek, time.Date(2026, 10, 3, 2, 30, 0, 0, time.UTC)); inPeak {
		t.Fatal("a Saturday morning should be off-peak")
	}
}

func TestPeakIsJudgedInUTCWhateverTheLocalZone(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no timezone database")
	}
	// 04:30 CEST on Friday is 02:30 UTC, which is peak.
	local := time.Date(2026, 10, 2, 4, 30, 0, 0, berlin)
	if inPeak, _ := Peak(deepseek, local); !inPeak {
		t.Fatal("peak must be judged in UTC, not in the local zone")
	}
}

func TestAHarnessWithoutWindowsIsNeverPeak(t *testing.T) {
	if inPeak, _ := Peak(nil, time.Date(2026, 10, 2, 2, 30, 0, 0, time.UTC)); inPeak {
		t.Fatal("a harness that declares no windows is never in peak")
	}
}
