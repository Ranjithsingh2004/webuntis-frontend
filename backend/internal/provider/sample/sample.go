// Package sample implements a provider that fabricates a fictional but
// believable school week relative to the current date.
//
// The data is fully deterministic for a given day, so screenshots and
// tests are reproducible, and every week contains each lesson state
// (cancellation, substitution, room change, exam, event, ...) so all UI
// states can be demoed without a real account.
package sample

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/seesee010/webuntis-frontend/backend/internal/domain"
	"github.com/seesee010/webuntis-frontend/backend/internal/provider"
)

const dateLayout = "2006-01-02"

// Provider serves sample data.
type Provider struct {
	now     func() time.Time
	latency time.Duration
}

// Option configures a Provider.
type Option func(*Provider)

// WithClock overrides the current time (for tests).
func WithClock(now func() time.Time) Option { return func(p *Provider) { p.now = now } }

// WithLatency delays every answer, to exercise loading states in the UI.
func WithLatency(d time.Duration) Option { return func(p *Provider) { p.latency = d } }

// New creates a sample provider.
func New(opts ...Option) *Provider {
	p := &Provider{now: time.Now}
	for _, o := range opts {
		o(p)
	}
	return p
}

func (p *Provider) Name() string { return "sample" }

func (p *Provider) wait(ctx context.Context) error {
	if p.latency <= 0 {
		return nil
	}
	select {
	case <-time.After(p.latency):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Snapshot builds the data for the requested window.
func (p *Provider) Snapshot(ctx context.Context, q provider.Query) (*domain.Snapshot, error) {
	if err := p.wait(ctx); err != nil {
		return nil, err
	}
	now := p.now()
	today := day(now)
	from, to := day(q.From), day(q.To)
	if to.Before(from) {
		return nil, provider.Errorf(provider.KindInternal, "window ends before it starts")
	}
	anchor := monday(today)
	holidays := buildHolidays(anchor)
	exams := buildExams(anchor)

	s := &domain.Snapshot{
		Meta: domain.Meta{
			Provider: p.Name(), School: schoolName, Server: serverName, User: studentName,
			From: from.Format(dateLayout), To: to.Format(dateLayout),
			OwnClasses: []string{ownClass}, FetchedAt: now, TimetableBy: "sample",
		},
		TimeGrid: append([]domain.Period(nil), timeGrid...),
	}

	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		if isWeekend(d) || inHolidays(d, holidays) {
			continue
		}
		s.Lessons = append(s.Lessons, lessonsFor(d, exams)...)
	}

	for _, e := range exams {
		if inWindow(e.Date, from, to) {
			s.Exams = append(s.Exams, e)
		}
	}
	// Selected by the day it was given, like the CLI: the window is a lesson
	// window, and the due date can be past its end.
	for _, h := range buildHomework(today) {
		if inWindow(h.Date, from, to) {
			s.Homework = append(s.Homework, h)
		}
	}
	for _, a := range buildAbsences(today) {
		if a.EndDate >= from.Format(dateLayout) && a.StartDate <= to.Format(dateLayout) {
			s.Absences = append(s.Absences, a)
		}
	}
	s.Messages = buildMessages(today)
	for _, h := range holidays {
		if h.End >= today.Format(dateLayout) {
			s.Holidays = append(s.Holidays, h)
		}
	}
	return s.Ensure(), nil
}

// Who looks a code or (part of a) name up in the sample tables.
func (p *Provider) Who(ctx context.Context, code string) ([]domain.Lookup, error) {
	if err := p.wait(ctx); err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(code))
	if q == "" {
		return nil, provider.Errorf(provider.KindNotFound, "empty code")
	}
	var out []domain.Lookup
	for _, t := range []struct {
		kind  string
		table map[string]string
	}{{"subject", subjects}, {"teacher", teachers}, {"room", rooms}, {"class", classes}} {
		for short, long := range t.table {
			ls, ll := strings.ToLower(short), strings.ToLower(long)
			if ls == q || ll == q || (len(q) >= 3 && strings.Contains(ll, q)) {
				out = append(out, domain.Lookup{Code: short, Kind: t.kind, Name: long})
			}
		}
	}
	if len(out) == 0 {
		return nil, provider.Errorf(provider.KindNotFound, "no subject, teacher, room or class matches %q", code)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind+out[i].Code < out[j].Kind+out[j].Code })
	return out, nil
}

// --- timetable ----------------------------------------------------------

// lessonsFor builds one day from the regular week and applies the
// deterministic variations of that weekday.
func lessonsFor(d time.Time, exams []domain.Exam) []domain.Lesson {
	wd := int(d.Weekday()+6) % 7 // 0 = Monday
	_, isoWeek := d.ISOWeek()
	date := d.Format(dateLayout)

	var out []domain.Lesson
	for _, sl := range week[wd] {
		out = append(out, lessonFromSlot(date, sl))
	}

	switch wd {
	case 0: // Monday: a substitute teacher, and every other week the class is taken out of a lesson
		sub := &out[isoWeek%3]
		replaceTeacher(sub, "IVE")
		if isoWeek%2 == 0 {
			rm := &out[3]
			rm.Removed = true
			rm.Classes = []domain.Element{{Short: ownClass, Long: classes[ownClass], Status: "REMOVED"}, el("3A", classes)}
		}
	case 1: // Tuesday: a cancellation
		out[2].Cancelled = true
		out[2].Info = "Teacher on a training course"
	case 2: // Wednesday: a room change
		rc := &out[isoWeek%2+1]
		old := rc.Rooms[0].Short
		rc.Rooms = []domain.Element{{Short: "AULA", Long: rooms["AULA"], Status: "CHANGED", Replaces: old, ReplacesLong: rooms[old]}}
		rc.Substitution = true
		rc.SubstitutionText = "Room change"
	case 3: // Thursday: an excursion in odd weeks, an extra lesson in even weeks
		if isoWeek%2 == 1 {
			out = out[:5]
			ev := domain.Lesson{
				Date: date, Start: timeGrid[5].Start, End: timeGrid[6].End, Event: true,
				Info: "Museum excursion", Text: "Meet at the main entrance.",
				Teachers: []domain.Element{el("DUN", teachers), el("EVE", teachers)},
				Classes:  []domain.Element{el(ownClass, classes)},
			}
			out = append(out, ev)
		} else {
			extra := lessonFromSlot(date, slot{7, 1, "CS", "GRE", "CS2", ""})
			extra.Text = "Extra lesson for the project"
			extra.DeriveStatus(true)
			out = append(out, extra)
		}
	case 4: // Friday: last lesson cancelled in even weeks, a missing teacher in odd weeks
		if isoWeek%2 == 0 {
			out[len(out)-1].Cancelled = true
		} else {
			nt := &out[1]
			nt.NoTeacher = true
			nt.Teachers[0].Status = "REMOVED"
		}
	}

	for _, e := range exams {
		if e.Date != date {
			continue
		}
		for i := range out {
			if out[i].Start == e.Start && len(out[i].Subjects) > 0 && out[i].Subjects[0].Short == e.Subjects[0].Short {
				out[i].Exam = true
				out[i].Text = e.Name
			}
		}
	}

	for i := range out {
		l := &out[i]
		if l.Status != domain.StatusAdditional {
			l.DeriveStatus(false)
		}
		l.ID = fmt.Sprintf("s-%s-%s-%d", date, strings.ReplaceAll(l.Start, ":", ""), i)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

func lessonFromSlot(date string, sl slot) domain.Lesson {
	end := timeGrid[sl.Period+sl.Length-2].End
	l := domain.Lesson{
		Date: date, Start: timeGrid[sl.Period-1].Start, End: end,
		Subjects: []domain.Element{el(sl.Subject, subjects)},
		Teachers: []domain.Element{el(sl.Teacher, teachers)},
		Rooms:    []domain.Element{el(sl.Room, rooms)},
		Classes:  []domain.Element{el(ownClass, classes)},
		Text:     sl.Group,
	}
	l.DeriveStatus(false)
	return l
}

func replaceTeacher(l *domain.Lesson, code string) {
	old := l.Teachers[0]
	l.Teachers = []domain.Element{{
		Short: code, Long: teachers[code], Status: "CHANGED",
		Replaces: old.Short, ReplacesLong: old.Long,
	}}
	l.Substitution = true
	l.SubstitutionText = "Substitution"
}

func el(code string, table map[string]string) domain.Element {
	return domain.Element{Short: code, Long: table[code]}
}

// --- other sections -------------------------------------------------------

func buildExams(anchor time.Time) []domain.Exam {
	out := make([]domain.Exam, 0, len(examPlan))
	for i, pe := range examPlan {
		d := anchor.AddDate(0, 0, pe.WeekOffset*7+pe.Weekday)
		sl := week[pe.Weekday][pe.Slot]
		out = append(out, domain.Exam{
			ID:   fmt.Sprintf("s-exam-%d", i+1),
			Date: d.Format(dateLayout), Start: timeGrid[sl.Period-1].Start,
			End: timeGrid[sl.Period+sl.Length-2].End, Name: pe.Name, Type: pe.Type, Grade: pe.Grade,
			Subjects: []domain.Element{el(sl.Subject, subjects)},
			Teachers: []domain.Element{el(sl.Teacher, teachers)},
			Rooms:    []domain.Element{el(sl.Room, rooms)},
		})
	}
	return out
}

func buildHomework(today time.Time) []domain.Homework {
	out := make([]domain.Homework, 0, len(homeworkPlan))
	for i, ph := range homeworkPlan {
		h := domain.Homework{
			ID:      fmt.Sprintf("s-hw-%d", i+1),
			Date:    weekday(today.AddDate(0, 0, ph.GivenOffset), -1).Format(dateLayout),
			DueDate: weekday(today.AddDate(0, 0, ph.DueOffset), +1).Format(dateLayout),
			Text:    ph.Text, Remark: ph.Remark, Completed: ph.Completed,
			Subjects:    []domain.Element{el(ph.Subject, subjects)},
			Teachers:    []domain.Element{el(ph.Teacher, teachers)},
			Attachments: []domain.Attachment{},
		}
		for j, name := range ph.Attachments {
			h.Attachments = append(h.Attachments, domain.Attachment{ID: fmt.Sprintf("%d-%d", i+1, j+1), Name: name})
		}
		out = append(out, h)
	}
	return out
}

func buildAbsences(today time.Time) []domain.Absence {
	out := make([]domain.Absence, 0, len(absencePlan))
	for i, pa := range absencePlan {
		start := weekday(today.AddDate(0, 0, pa.DayOffset), -1)
		end := weekday(start.AddDate(0, 0, pa.Days-1), +1)
		out = append(out, domain.Absence{
			ID:        fmt.Sprintf("s-abs-%d", i+1),
			StartDate: start.Format(dateLayout), EndDate: end.Format(dateLayout),
			StartTime: pa.StartTime, EndTime: pa.EndTime, Reason: pa.Reason, Text: pa.Text,
			Excused: pa.Excused, ExcuseStatus: pa.Status, ExcuseText: pa.ExcuseText,
		})
	}
	return out
}

func buildMessages(today time.Time) []domain.Message {
	out := make([]domain.Message, 0, len(messagePlan))
	for i, pm := range messagePlan {
		at := today.AddDate(0, 0, pm.DayOffset).Add(7*time.Hour + time.Duration(i*17)*time.Minute)
		out = append(out, domain.Message{
			ID: fmt.Sprintf("s-msg-%d", i+1), Subject: pm.Subject, Preview: pm.Preview,
			From: pm.From, Date: at.Format("2006-01-02T15:04:05"), Read: pm.Read,
			HasAttachments: pm.Attachments,
		})
	}
	return out
}

// buildHolidays places a short break, a public holiday and a long break
// in the coming weeks, so the countdowns always have something to show.
func buildHolidays(anchor time.Time) []domain.Holiday {
	mk := func(id, name string, start time.Time, days int) domain.Holiday {
		return domain.Holiday{
			ID: id, Name: name, Start: start.Format(dateLayout),
			End: start.AddDate(0, 0, days-1).Format(dateLayout),
		}
	}
	return []domain.Holiday{
		mk("s-hol-1", "Autumn break", anchor.AddDate(0, 0, 3*7), 9),
		mk("s-hol-2", "Public holiday", anchor.AddDate(0, 0, 6*7+2), 1),
		mk("s-hol-3", "Winter break", anchor.AddDate(0, 0, 9*7-2), 16),
	}
}

// --- date helpers ---------------------------------------------------------

func day(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}

func monday(t time.Time) time.Time {
	return day(t).AddDate(0, 0, -(int(t.Weekday()+6) % 7))
}

func isWeekend(t time.Time) bool {
	return t.Weekday() == time.Saturday || t.Weekday() == time.Sunday
}

// weekday moves a weekend date to the nearest weekday in direction dir.
func weekday(t time.Time, dir int) time.Time {
	for isWeekend(t) {
		t = t.AddDate(0, 0, dir)
	}
	return t
}

func inHolidays(t time.Time, hs []domain.Holiday) bool {
	s := t.Format(dateLayout)
	for _, h := range hs {
		if h.Start <= s && s <= h.End {
			return true
		}
	}
	return false
}

func inWindow(date string, from, to time.Time) bool {
	return date >= from.Format(dateLayout) && date <= to.Format(dateLayout)
}
