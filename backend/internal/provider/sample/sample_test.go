package sample

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/seesee010/webuntis-frontend/backend/internal/domain"
	"github.com/seesee010/webuntis-frontend/backend/internal/provider"
)

// A Wednesday in an even ISO week.
var fixedNow = time.Date(2026, 10, 7, 10, 15, 0, 0, time.Local)

func newProvider() *Provider {
	return New(WithClock(func() time.Time { return fixedNow }))
}

func snapshot(t *testing.T, p *Provider, from, to time.Time) *domain.Snapshot {
	t.Helper()
	s, err := p.Snapshot(context.Background(), provider.Query{From: from, To: to})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestEveryStatusWithinTwoWeeks(t *testing.T) {
	mon := monday(fixedNow)
	s := snapshot(t, newProvider(), mon, mon.AddDate(0, 0, 13))

	seen := map[domain.LessonStatus]bool{}
	for _, l := range s.Lessons {
		seen[l.Status] = true
	}
	for _, want := range []domain.LessonStatus{
		domain.StatusRegular, domain.StatusCancelled, domain.StatusRemoved, domain.StatusExam,
		domain.StatusEvent, domain.StatusSubstitution, domain.StatusAdditional, domain.StatusNoTeacher,
	} {
		if !seen[want] {
			t.Errorf("status %q never occurs", want)
		}
	}
}

func TestDeterministic(t *testing.T) {
	mon := monday(fixedNow)
	a := snapshot(t, newProvider(), mon, mon.AddDate(0, 0, 6))
	b := snapshot(t, newProvider(), mon, mon.AddDate(0, 0, 6))
	if !reflect.DeepEqual(a, b) {
		t.Error("two snapshots of the same window differ")
	}
}

func TestNoLessonsOnWeekendsOrHolidays(t *testing.T) {
	p := newProvider()
	mon := monday(fixedNow)
	s := snapshot(t, p, mon, mon.AddDate(0, 0, 6))
	for _, l := range s.Lessons {
		d, _ := time.ParseInLocation(dateLayout, l.Date, time.Local)
		if isWeekend(d) {
			t.Errorf("lesson on a weekend: %+v", l)
		}
	}
	if len(s.Holidays) == 0 {
		t.Fatal("no holidays")
	}
	h := s.Holidays[0]
	start, _ := time.ParseInLocation(dateLayout, h.Start, time.Local)
	end, _ := time.ParseInLocation(dateLayout, h.End, time.Local)
	if in := snapshot(t, p, start, end); len(in.Lessons) != 0 {
		t.Errorf("%d lessons during %s", len(in.Lessons), h.Name)
	}
}

func TestLessonsAreInsideTheTimeGrid(t *testing.T) {
	mon := monday(fixedNow)
	s := snapshot(t, newProvider(), mon, mon.AddDate(0, 0, 13))
	first, last := s.TimeGrid[0].Start, s.TimeGrid[len(s.TimeGrid)-1].End
	ids := map[string]bool{}
	for _, l := range s.Lessons {
		if l.Start < first || l.End > last || l.Start >= l.End {
			t.Errorf("lesson outside the grid: %s %s-%s", l.Date, l.Start, l.End)
		}
		if ids[l.ID] {
			t.Errorf("duplicate lesson id %s", l.ID)
		}
		ids[l.ID] = true
	}
}

func TestExamsMatchTheirLessons(t *testing.T) {
	mon := monday(fixedNow)
	s := snapshot(t, newProvider(), mon.AddDate(0, 0, -21), mon.AddDate(0, 0, 20))
	if len(s.Exams) != len(examPlan) {
		t.Fatalf("exams = %d, want %d", len(s.Exams), len(examPlan))
	}
	for _, e := range s.Exams {
		found := false
		for _, l := range s.Lessons {
			if l.Date == e.Date && l.Start == e.Start && l.Exam {
				found = true
			}
		}
		if !found {
			t.Errorf("exam %q on %s %s has no exam lesson", e.Name, e.Date, e.Start)
		}
	}
}

func TestWindowFiltersSections(t *testing.T) {
	today := day(fixedNow)
	s := snapshot(t, newProvider(), today, today)
	// Homework is selected by the day it was given, like the CLI, so an item
	// due after the window still shows up.
	if len(s.Homework) == 0 {
		t.Fatal("no homework given inside the window")
	}
	for _, h := range s.Homework {
		if h.Date != today.Format(dateLayout) {
			t.Errorf("homework given %s outside window", h.Date)
		}
	}
	if len(s.Messages) != len(messagePlan) {
		t.Errorf("messages are not windowed: got %d", len(s.Messages))
	}
}

func TestWho(t *testing.T) {
	p := newProvider()
	got, err := p.Who(context.Background(), "math")
	if err != nil || len(got) != 1 || got[0].Name != "Mathematics" || got[0].Kind != "subject" {
		t.Errorf("Who(math) = %+v, %v", got, err)
	}
	got, err = p.Who(context.Background(), "lab")
	if err != nil || len(got) != 2 {
		t.Errorf("Who(lab) = %+v, %v", got, err)
	}
	if _, err := p.Who(context.Background(), "XYZ"); provider.KindOf(err) != provider.KindNotFound {
		t.Errorf("Who(XYZ) error kind = %v", provider.KindOf(err))
	}
}

func TestLatencyRespectsContext(t *testing.T) {
	p := New(WithLatency(time.Hour))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.Snapshot(ctx, provider.Query{From: fixedNow, To: fixedNow})
	if provider.KindOf(err) != provider.KindCancelled {
		t.Errorf("err = %v, want cancelled", err)
	}
}
