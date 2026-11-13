// Package changes compares two snapshots and lists what changed, the way
// `untis --changes` does: cancellations, substitutions, room changes,
// removed or added lessons, new or removed exams and new homework.
package changes

import (
	"sort"
	"strings"

	"github.com/seesee010/webuntis-frontend/backend/internal/domain"
)

// Kinds of changes.
const (
	Cancelled     = "cancelled"
	Substitution  = "substitution"
	RoomChange    = "room-change"
	Removed       = "removed"
	LessonAdded   = "lesson-added"
	LessonDropped = "lesson-dropped"
	ExamAdded     = "exam-added"
	ExamRemoved   = "exam-removed"
	HomeworkAdded = "homework-added"
	MessageNew    = "message-new"
)

// Change is one difference between two snapshots.
type Change struct {
	Kind    string `json:"kind"`
	Date    string `json:"date,omitempty"`
	Start   string `json:"start,omitempty"`
	Subject string `json:"subject,omitempty"`
	Detail  string `json:"detail,omitempty"`
	Ref     string `json:"ref,omitempty"` // ID of the lesson, exam, homework or message
}

// Diff lists the changes from old to cur. Lessons, exams and homework are
// only compared on days both windows cover, so moving the window is not a
// change.
func Diff(old, cur *domain.Snapshot) []Change {
	if old == nil || cur == nil {
		return nil
	}
	var out []Change
	inBoth := window(old, cur)
	out = append(out, diffLessons(inBoth, old, cur)...)
	out = append(out, diffExams(inBoth, old, cur)...)
	out = append(out, diffHomework(inBoth, old, cur)...)
	out = append(out, diffMessages(old, cur)...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Date+out[i].Start < out[j].Date+out[j].Start
	})
	return out
}

func lessonKey(l domain.Lesson) string {
	return l.Date + "|" + l.Start + "|" + shorts(l.Subjects) + "|" + l.Info
}

// window returns the days both snapshots cover. Anything outside is reported
// by the window moving, not by a change in WebUntis: the overview window slides
// forward every day, so an exam or homework entering it at the far edge is not
// new. Undated homework cannot be placed and is always considered.
func window(old, cur *domain.Snapshot) func(string) bool {
	from := max(old.Meta.From, cur.Meta.From)
	to := min(old.Meta.To, cur.Meta.To)
	return func(d string) bool { return d == "" || (d >= from && d <= to) }
}

func diffLessons(inBoth func(string) bool, old, cur *domain.Snapshot) []Change {
	before := map[string]domain.Lesson{}
	for _, l := range old.Lessons {
		if inBoth(l.Date) {
			before[lessonKey(l)] = l
		}
	}
	var out []Change
	seen := map[string]bool{}
	for _, l := range cur.Lessons {
		if !inBoth(l.Date) {
			continue
		}
		k := lessonKey(l)
		seen[k] = true
		c := Change{Date: l.Date, Start: l.Start, Subject: subject(l), Ref: l.ID}
		prev, ok := before[k]
		if !ok {
			c.Kind = LessonAdded
			out = append(out, c)
			continue
		}
		switch {
		case l.Cancelled && !prev.Cancelled:
			c.Kind = Cancelled
		case l.Removed && !prev.Removed:
			c.Kind = Removed
		case shorts(l.Teachers) != shorts(prev.Teachers) && !l.Cancelled:
			c.Kind, c.Detail = Substitution, shorts(prev.Teachers)+" → "+shorts(l.Teachers)
		case shorts(l.Rooms) != shorts(prev.Rooms) && !l.Cancelled:
			c.Kind, c.Detail = RoomChange, shorts(prev.Rooms)+" → "+shorts(l.Rooms)
		default:
			continue
		}
		out = append(out, c)
	}
	for k, l := range before {
		if !seen[k] {
			out = append(out, Change{Kind: LessonDropped, Date: l.Date, Start: l.Start, Subject: subject(l), Ref: l.ID})
		}
	}
	return out
}

func diffExams(inBoth func(string) bool, old, cur *domain.Snapshot) []Change {
	key := func(e domain.Exam) string { return e.Date + "|" + shorts(e.Subjects) + "|" + e.Name }
	before := map[string]bool{}
	for _, e := range old.Exams {
		before[key(e)] = true
	}
	now := map[string]bool{}
	var out []Change
	for _, e := range cur.Exams {
		now[key(e)] = true
		if !before[key(e)] && inBoth(e.Date) {
			out = append(out, Change{Kind: ExamAdded, Date: e.Date, Start: e.Start, Subject: shorts(e.Subjects), Detail: e.Name, Ref: e.ID})
		}
	}
	for _, e := range old.Exams {
		if !now[key(e)] && inBoth(e.Date) {
			out = append(out, Change{Kind: ExamRemoved, Date: e.Date, Start: e.Start, Subject: shorts(e.Subjects), Detail: e.Name, Ref: e.ID})
		}
	}
	return out
}

// diffHomework compares homework by the lesson it was given in, which is the
// day the CLI selects it by (the window is passed to /homeworks/lessons, not
// used as a due-date filter). Filtering on the due date instead would miss
// homework given today and due after the window ends.
func diffHomework(inBoth func(string) bool, old, cur *domain.Snapshot) []Change {
	before := map[string]bool{}
	for _, h := range old.Homework {
		before[h.ID] = true
	}
	var out []Change
	for _, h := range cur.Homework {
		if !before[h.ID] && inBoth(h.Date) {
			out = append(out, Change{Kind: HomeworkAdded, Date: h.DueDate, Subject: shorts(h.Subjects), Detail: h.Text, Ref: h.ID})
		}
	}
	return out
}

func diffMessages(old, cur *domain.Snapshot) []Change {
	before := map[string]bool{}
	for _, m := range old.Messages {
		before[m.ID] = true
	}
	var out []Change
	for _, m := range cur.Messages {
		if !before[m.ID] {
			out = append(out, Change{Kind: MessageNew, Detail: m.Subject, Ref: m.ID})
		}
	}
	return out
}

func subject(l domain.Lesson) string {
	if s := shorts(l.Subjects); s != "" {
		return s
	}
	return l.Info
}

func shorts(es []domain.Element) string {
	var parts []string
	for _, e := range es {
		if e.Status == "REMOVED" {
			continue
		}
		parts = append(parts, e.Short)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}
