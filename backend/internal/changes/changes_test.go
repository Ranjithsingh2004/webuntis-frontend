package changes

import (
	"testing"

	"github.com/seesee010/webuntis-frontend/backend/internal/domain"
)

func lesson(date, start, subj, teacher, room string) domain.Lesson {
	return domain.Lesson{
		ID: date + start + subj, Date: date, Start: start,
		Subjects: []domain.Element{{Short: subj}},
		Teachers: []domain.Element{{Short: teacher}},
		Rooms:    []domain.Element{{Short: room}},
	}
}

func snap(from, to string, ls ...domain.Lesson) *domain.Snapshot {
	return &domain.Snapshot{Meta: domain.Meta{From: from, To: to}, Lessons: ls}
}

func kinds(cs []Change) map[string]Change {
	m := map[string]Change{}
	for _, c := range cs {
		m[c.Kind] = c
	}
	return m
}

func TestLessonChanges(t *testing.T) {
	old := snap("2026-10-05", "2026-10-09",
		lesson("2026-10-05", "08:00", "MATH", "AMB", "R101"),
		lesson("2026-10-05", "09:00", "ENG", "BRO", "R101"),
		lesson("2026-10-06", "08:00", "GER", "CAS", "R101"),
		lesson("2026-10-06", "09:00", "HIST", "DUN", "R101"),
		lesson("2026-10-07", "08:00", "ART", "HOL", "ARTS"),
	)
	cancelled := lesson("2026-10-05", "08:00", "MATH", "AMB", "R101")
	cancelled.Cancelled = true
	cur := snap("2026-10-05", "2026-10-09",
		cancelled,
		lesson("2026-10-05", "09:00", "ENG", "IVE", "R101"),
		lesson("2026-10-06", "08:00", "GER", "CAS", "AULA"),
		lesson("2026-10-07", "08:00", "ART", "HOL", "ARTS"),
		lesson("2026-10-08", "14:00", "CS", "GRE", "CS2"),
	)
	got := kinds(Diff(old, cur))
	if len(got) != 5 {
		t.Fatalf("changes = %+v", Diff(old, cur))
	}
	if got[Substitution].Detail != "BRO → IVE" {
		t.Errorf("substitution = %+v", got[Substitution])
	}
	if got[RoomChange].Detail != "R101 → AULA" {
		t.Errorf("room change = %+v", got[RoomChange])
	}
	if got[Cancelled].Subject != "MATH" || got[LessonDropped].Subject != "HIST" || got[LessonAdded].Subject != "CS" {
		t.Errorf("changes = %+v", got)
	}
}

func TestOnlyOverlappingDaysCompare(t *testing.T) {
	old := snap("2026-10-05", "2026-10-06", lesson("2026-10-05", "08:00", "MATH", "AMB", "R101"))
	cur := snap("2026-10-06", "2026-10-07", lesson("2026-10-07", "08:00", "ENG", "BRO", "R101"))
	if cs := Diff(old, cur); len(cs) != 0 {
		t.Errorf("moving the window reported changes: %+v", cs)
	}
}

func TestSlidingWindowDoesNotAddExamsOrHomework(t *testing.T) {
	// the window moves forward one day; the exam at the new far edge and the
	// homework due there were in WebUntis all along
	old := &domain.Snapshot{Meta: domain.Meta{From: "2026-10-01", To: "2026-10-31"},
		Exams:    []domain.Exam{{ID: "1", Date: "2026-10-31", Name: "Test", Subjects: []domain.Element{{Short: "MATH"}}}},
		Homework: []domain.Homework{{ID: "h1", DueDate: "2026-10-31", Text: "Read"}},
	}
	cur := &domain.Snapshot{Meta: domain.Meta{From: "2026-10-02", To: "2026-11-01"},
		Exams:    []domain.Exam{{ID: "1", Date: "2026-10-31", Name: "Test", Subjects: []domain.Element{{Short: "MATH"}}}, {ID: "2", Date: "2026-11-01", Name: "Quiz", Subjects: []domain.Element{{Short: "ENG"}}}},
		Homework: []domain.Homework{{ID: "h1", DueDate: "2026-10-31", Text: "Read"}, {ID: "h2", DueDate: "2026-11-01", Text: "Write"}},
	}
	if cs := Diff(old, cur); len(cs) != 0 {
		t.Errorf("sliding window reported changes: %+v", cs)
	}

	// but a real new exam inside the shared days is still reported
	cur.Exams = append(cur.Exams, domain.Exam{ID: "3", Date: "2026-10-20", Name: "Real", Subjects: []domain.Element{{Short: "CS"}}})
	got := kinds(Diff(old, cur))
	if got[ExamAdded].Detail != "Real" {
		t.Errorf("missing the new exam: %+v", Diff(old, cur))
	}
}

func TestExamsHomeworkMessages(t *testing.T) {
	old := &domain.Snapshot{Meta: domain.Meta{From: "2026-10-01", To: "2026-10-31"},
		Exams:    []domain.Exam{{ID: "1", Date: "2026-10-09", Name: "Test", Subjects: []domain.Element{{Short: "MATH"}}}},
		Homework: []domain.Homework{{ID: "h1"}},
		Messages: []domain.Message{{ID: "m1"}},
	}
	cur := &domain.Snapshot{Meta: old.Meta,
		Exams:    []domain.Exam{{ID: "2", Date: "2026-10-12", Name: "Quiz", Subjects: []domain.Element{{Short: "ENG"}}}},
		Homework: []domain.Homework{{ID: "h1"}, {ID: "h2", Text: "Read"}},
		Messages: []domain.Message{{ID: "m1"}, {ID: "m2", Subject: "Hi"}},
	}
	got := kinds(Diff(old, cur))
	for _, k := range []string{ExamAdded, ExamRemoved, HomeworkAdded, MessageNew} {
		if _, ok := got[k]; !ok {
			t.Errorf("missing %s in %+v", k, got)
		}
	}
	if Diff(nil, cur) != nil {
		t.Error("diff against nothing must be empty")
	}
}
