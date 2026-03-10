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
