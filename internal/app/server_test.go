package app

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"course-assistant/internal/domain"
)

type memStore struct {
	attempts            []domain.Attempt
	answers             map[string]map[string]string
	settings            map[string]string
	homeworkSubmissions []domain.HomeworkSubmission
}

func (m *memStore) Init(context.Context) error { return nil }
func (m *memStore) GetSetting(_ context.Context, key string) (string, error) {
	if m.settings == nil {
		return "", errors.New("not implemented")
	}
	if v, ok := m.settings[key]; ok {
		return v, nil
	}
	return "", errors.New("not implemented")
}
func (m *memStore) SetSetting(_ context.Context, key, value string) error {
	if m.settings == nil {
		m.settings = map[string]string{}
	}
	m.settings[key] = value
	return nil
}
func (m *memStore) CreateAttempt(context.Context, *domain.Attempt) error {
	return errors.New("not implemented")
}
func (m *memStore) ListAttempts(context.Context) ([]domain.Attempt, error) { return m.attempts, nil }
func (m *memStore) GetAttemptByID(context.Context, string) (*domain.Attempt, error) {
	return nil, errors.New("not implemented")
}
func (m *memStore) GetAttemptByToken(context.Context, string) (*domain.Attempt, error) {
	return nil, errors.New("not implemented")
}
func (m *memStore) UpdateAttemptStatus(context.Context, string, domain.AttemptStatus) error {
	return errors.New("not implemented")
}
func (m *memStore) SubmitAttempt(context.Context, string) (int, error) {
	return 0, errors.New("not implemented")
}
func (m *memStore) SaveAnswer(context.Context, domain.Answer) error {
	return errors.New("not implemented")
}
func (m *memStore) GetAnswers(ctx context.Context, attemptID string) (map[string]string, error) {
	if m.answers == nil {
		return map[string]string{}, nil
	}
	if got, ok := m.answers[attemptID]; ok {
		return got, nil
	}
	return map[string]string{}, nil
}
func (m *memStore) UpsertSummary(context.Context, string, string) error {
	return errors.New("not implemented")
}
func (m *memStore) GetSummary(context.Context, string) (string, error) {
	return "", errors.New("not implemented")
}
func (m *memStore) GetLiveStats(context.Context) (int, int, error) {
	return 0, 0, errors.New("not implemented")
}
func (m *memStore) GetInProgressAttempt(context.Context, string, string) (*domain.Attempt, error) {
	return nil, errors.New("not implemented")
}
func (m *memStore) UpdateAttemptSession(context.Context, string, string, string, string) error {
	return errors.New("not implemented")
}
func (m *memStore) UpsertAdminSummary(context.Context, string, string) error {
	return errors.New("not implemented")
}
func (m *memStore) GetAdminSummary(context.Context, string) (string, error) {
	return "", errors.New("not implemented")
}
func (m *memStore) DeleteAdminSummary(context.Context, string) error {
	return errors.New("not implemented")
}
func (m *memStore) ClearAttempts(context.Context, string) error { return errors.New("not implemented") }
func (m *memStore) CreateHomeworkSubmission(_ context.Context, submission *domain.HomeworkSubmission) error {
	m.homeworkSubmissions = append(m.homeworkSubmissions, *submission)
	return nil
}
func (m *memStore) GetHomeworkSubmissionByID(_ context.Context, submissionID string) (*domain.HomeworkSubmission, error) {
	for i := range m.homeworkSubmissions {
		if m.homeworkSubmissions[i].ID == submissionID {
			item := m.homeworkSubmissions[i]
			return &item, nil
		}
	}
	return nil, sql.ErrNoRows
}
func (m *memStore) GetHomeworkSubmissionByToken(_ context.Context, token string) (*domain.HomeworkSubmission, error) {
	for i := range m.homeworkSubmissions {
		if m.homeworkSubmissions[i].SessionToken == token {
			item := m.homeworkSubmissions[i]
			return &item, nil
		}
	}
	return nil, sql.ErrNoRows
}
func (m *memStore) GetHomeworkSubmissionByScope(_ context.Context, course, assignmentID, studentNo string) (*domain.HomeworkSubmission, error) {
	for i := range m.homeworkSubmissions {
		item := m.homeworkSubmissions[i]
		if item.Course == course && item.AssignmentID == assignmentID && item.StudentNo == studentNo {
			return &item, nil
		}
	}
	return nil, sql.ErrNoRows
}
func (m *memStore) UpdateHomeworkSubmissionSession(_ context.Context, submissionID, token, name, className string) error {
	for i := range m.homeworkSubmissions {
		if m.homeworkSubmissions[i].ID == submissionID {
			m.homeworkSubmissions[i].SessionToken = token
			m.homeworkSubmissions[i].Name = name
			m.homeworkSubmissions[i].ClassName = className
			m.homeworkSubmissions[i].UpdatedAt = time.Now()
			return nil
		}
	}
	return errors.New("not implemented")
}
func (m *memStore) ListHomeworkSubmissions(_ context.Context, course, assignmentID string) ([]domain.HomeworkSubmission, error) {
	items := make([]domain.HomeworkSubmission, 0)
	for _, item := range m.homeworkSubmissions {
		if course != "" && item.Course != course {
			continue
		}
		if assignmentID != "" && item.AssignmentID != assignmentID {
			continue
		}
		items = append(items, item)
	}
	return items, nil
}
func (m *memStore) SaveHomeworkFileMetadata(_ context.Context, submissionID string, slot domain.HomeworkFileSlot, originalName string) error {
	for i := range m.homeworkSubmissions {
		if m.homeworkSubmissions[i].ID != submissionID {
			continue
		}
		now := time.Now()
		m.homeworkSubmissions[i].UpdatedAt = now
		switch slot {
		case domain.HomeworkSlotReport:
			m.homeworkSubmissions[i].ReportOriginalName = originalName
			m.homeworkSubmissions[i].ReportUploadedAt = &now
		case domain.HomeworkSlotCode:
			m.homeworkSubmissions[i].CodeOriginalName = originalName
			m.homeworkSubmissions[i].CodeUploadedAt = &now
		default:
			return errors.New("not implemented")
		}
		return nil
	}
	return errors.New("not implemented")
}
func (m *memStore) DeleteHomeworkFileMetadata(_ context.Context, submissionID string, slot domain.HomeworkFileSlot) error {
	for i := range m.homeworkSubmissions {
		if m.homeworkSubmissions[i].ID != submissionID {
			continue
		}
		m.homeworkSubmissions[i].UpdatedAt = time.Now()
		switch slot {
		case domain.HomeworkSlotReport:
			m.homeworkSubmissions[i].ReportOriginalName = ""
			m.homeworkSubmissions[i].ReportUploadedAt = nil
		case domain.HomeworkSlotCode:
			m.homeworkSubmissions[i].CodeOriginalName = ""
			m.homeworkSubmissions[i].CodeUploadedAt = nil
		default:
			return errors.New("not implemented")
		}
		return nil
	}
	return errors.New("not implemented")
}

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

func TestNormalizeAnswerSurveyAllowMultiple(t *testing.T) {
	q := domain.Question{
		ID:            "s1",
		Type:          domain.QuestionSurvey,
		AllowMultiple: true,
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

func TestNormalizeAnswerSurveySingleChoice(t *testing.T) {
	q := domain.Question{
		ID:   "s2",
		Type: domain.QuestionSurvey,
		Options: []domain.Option{
			{Key: "A", Text: "1"},
			{Key: "B", Text: "2"},
		},
	}
	got, err := normalizeAnswer(q, "B")
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if got != "B" {
		t.Fatalf("unexpected normalized answer: %s", got)
	}
	if _, err := normalizeAnswer(q, "A,B"); err == nil {
		t.Fatalf("single survey should reject multi answer")
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

func TestBuildAdminSummaryInputAvgTotalExcludesSurveyAndShortAnswer(t *testing.T) {
	quiz := &domain.Quiz{
		QuizID: "quiz-1",
		Title:  "t",
		Questions: []domain.Question{
			{ID: "q1", Type: domain.QuestionSingleChoice, Stem: "q1", CorrectAnswer: "A", Options: []domain.Option{{Key: "A", Text: "1"}, {Key: "B", Text: "2"}}},
			{ID: "q2", Type: domain.QuestionShortAnswer, Stem: "q2"},
			{ID: "q3", Type: domain.QuestionSurvey, Stem: "q3", Options: []domain.Option{{Key: "A", Text: "好"}, {Key: "B", Text: "一般"}}},
			{ID: "q4", Type: domain.QuestionSingleChoice, Stem: "q4", CorrectAnswer: "B", Options: []domain.Option{{Key: "A", Text: "1"}, {Key: "B", Text: "2"}}},
		},
	}

	st := &memStore{
		attempts: []domain.Attempt{
			{ID: "a1", QuizID: quiz.QuizID, StudentNo: "s1", Status: domain.StatusSubmitted, AttemptNo: 1},
			{ID: "a2", QuizID: quiz.QuizID, StudentNo: "s2", Status: domain.StatusSubmitted, AttemptNo: 1},
		},
		answers: map[string]map[string]string{
			"a1": {"q1": "A", "q2": "反馈1", "q3": "A", "q4": "A"},
			"a2": {"q1": "B", "q2": "反馈2", "q3": "B", "q4": "B"},
		},
	}
	s := &Server{store: st}

	in, err := s.buildAdminSummaryInput(context.Background(), quiz)
	if err != nil {
		t.Fatalf("buildAdminSummaryInput failed: %v", err)
	}
	if in.StudentCount != 2 {
		t.Fatalf("unexpected StudentCount: %d", in.StudentCount)
	}
	if in.AvgScore != 1.0 {
		t.Fatalf("unexpected AvgScore: %v", in.AvgScore)
	}
	if in.AvgTotal != 2.0 {
		t.Fatalf("unexpected AvgTotal: %v", in.AvgTotal)
	}
	if len(in.QuestionStats) != 2 {
		t.Fatalf("unexpected QuestionStats count: %d", len(in.QuestionStats))
	}
}
