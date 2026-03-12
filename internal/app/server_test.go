package app

import (
	"testing"

	"course-assistant/internal/domain"
)

func TestShuffledQuestionsWithSampling(t *testing.T) {
	quiz := &domain.Quiz{
		QuizID: "w2",
		Sampling: &domain.Sampling{
			Groups: []domain.SamplingGroup{
				{Tag: "A", Pick: 2},
				{Tag: "B", Pick: 2},
			},
		},
		Questions: []domain.Question{
			{ID: "a1", Type: domain.QuestionSingleChoice, Stem: "a1", CorrectAnswer: "A", PoolTag: "A", Options: []domain.Option{{Key: "A", Text: "1"}, {Key: "B", Text: "2"}}},
			{ID: "a2", Type: domain.QuestionSingleChoice, Stem: "a2", CorrectAnswer: "A", PoolTag: "A", Options: []domain.Option{{Key: "A", Text: "1"}, {Key: "B", Text: "2"}}},
			{ID: "a3", Type: domain.QuestionSingleChoice, Stem: "a3", CorrectAnswer: "A", PoolTag: "A", Options: []domain.Option{{Key: "A", Text: "1"}, {Key: "B", Text: "2"}}},
			{ID: "b1", Type: domain.QuestionSingleChoice, Stem: "b1", CorrectAnswer: "A", PoolTag: "B", Options: []domain.Option{{Key: "A", Text: "1"}, {Key: "B", Text: "2"}}},
			{ID: "b2", Type: domain.QuestionSingleChoice, Stem: "b2", CorrectAnswer: "A", PoolTag: "B", Options: []domain.Option{{Key: "A", Text: "1"}, {Key: "B", Text: "2"}}},
			{ID: "b3", Type: domain.QuestionSingleChoice, Stem: "b3", CorrectAnswer: "A", PoolTag: "B", Options: []domain.Option{{Key: "A", Text: "1"}, {Key: "B", Text: "2"}}},
			{ID: "f1", Type: domain.QuestionSurvey, Stem: "f1", Options: []domain.Option{{Key: "A", Text: "1"}, {Key: "B", Text: "2"}}},
			{ID: "f2", Type: domain.QuestionSurvey, Stem: "f2", Options: []domain.Option{{Key: "A", Text: "1"}, {Key: "B", Text: "2"}}},
		},
	}

	got := shuffledQuestions(quiz, "attempt-1")
	if len(got) != 6 {
		t.Fatalf("unexpected question count: %d", len(got))
	}

	countA := 0
	countB := 0
	fixed := map[string]bool{"f1": false, "f2": false}
	for _, q := range got {
		if q.PoolTag == "A" {
			countA++
		}
		if q.PoolTag == "B" {
			countB++
		}
		if _, ok := fixed[q.ID]; ok {
			fixed[q.ID] = true
		}
	}
	if countA != 2 || countB != 2 {
		t.Fatalf("unexpected sampled count: A=%d B=%d", countA, countB)
	}
	if !fixed["f1"] || !fixed["f2"] {
		t.Fatalf("fixed questions should always appear")
	}

	gotAgain := shuffledQuestions(quiz, "attempt-1")
	for i := range got {
		if got[i].ID != gotAgain[i].ID {
			t.Fatalf("same attempt should have stable order")
		}
	}
}

func TestNormalizeAnswerMultiChoice(t *testing.T) {
	q := domain.Question{
		ID:   "m1",
		Type: domain.QuestionMultiChoice,
		Options: []domain.Option{
			{Key: "A", Text: "1"},
			{Key: "B", Text: "2"},
			{Key: "C", Text: "3"},
		},
	}
	got, err := normalizeAnswer(q, " C, A ,C ")
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if got != "A,C" {
		t.Fatalf("unexpected normalized answer: %s", got)
	}
	if _, err := normalizeAnswer(q, "D"); err == nil {
		t.Fatalf("invalid option should fail")
	}
}

func TestIsCorrectAnswerMultiChoice(t *testing.T) {
	q := domain.Question{
		ID:            "m2",
		Type:          domain.QuestionMultiChoice,
		CorrectAnswer: "A,C",
		Options: []domain.Option{
			{Key: "A", Text: "1"},
			{Key: "B", Text: "2"},
			{Key: "C", Text: "3"},
		},
	}
	if !isCorrectAnswer(q, "C,A") {
		t.Fatalf("same selected set should be correct")
	}
	if isCorrectAnswer(q, "A") {
		t.Fatalf("incomplete selected set should be incorrect")
	}
}

func TestShuffledQuestionsShuffleOptionsStable(t *testing.T) {
	quiz := &domain.Quiz{
		QuizID: "w3",
		Questions: []domain.Question{
			{
				ID:            "q1",
				Type:          domain.QuestionSingleChoice,
				Stem:          "s",
				CorrectAnswer: "A",
				Options: []domain.Option{
					{Key: "A", Text: "1"},
					{Key: "B", Text: "2"},
					{Key: "C", Text: "3"},
					{Key: "D", Text: "4"},
				},
			},
		},
	}
	got1 := shuffledQuestions(quiz, "attempt-x")
	got2 := shuffledQuestions(quiz, "attempt-x")
	if len(got1) != 1 || len(got2) != 1 {
		t.Fatalf("unexpected question count")
	}
	for i := range got1[0].Options {
		if got1[0].Options[i].Key != got2[0].Options[i].Key {
			t.Fatalf("same attempt should keep option order stable")
		}
	}
}

func TestFormatQuestionCorrectForCSV(t *testing.T) {
	single := domain.Question{
		Type:          domain.QuestionSingleChoice,
		CorrectAnswer: "B",
		Options:       []domain.Option{{Key: "A", Text: "甲"}, {Key: "B", Text: "乙"}},
	}
	if got := formatQuestionCorrectForCSV(single); got != "B:乙" {
		t.Fatalf("single correct format mismatch: %s", got)
	}

	multi := domain.Question{
		Type:          domain.QuestionMultiChoice,
		CorrectAnswer: "A,C",
		Options:       []domain.Option{{Key: "A", Text: "一"}, {Key: "B", Text: "二"}, {Key: "C", Text: "三"}},
	}
	if got := formatQuestionCorrectForCSV(multi); got != "A:一；C:三" {
		t.Fatalf("multi correct format mismatch: %s", got)
	}

	short := domain.Question{
		Type:            domain.QuestionShortAnswer,
		ReferenceAnswer: "可行解",
	}
	if got := formatQuestionCorrectForCSV(short); got != "可行解" {
		t.Fatalf("short answer format mismatch: %s", got)
	}

	survey := domain.Question{
		Type: domain.QuestionSurvey,
	}
	if got := formatQuestionCorrectForCSV(survey); got != "" {
		t.Fatalf("survey should have empty correct answer: %s", got)
	}
}
