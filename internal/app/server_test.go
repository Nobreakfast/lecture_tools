package app

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"course-assistant/internal/domain"
)

type memStore struct {
	attempts            []domain.Attempt
	answers             map[string]map[string]string
	settings            map[string]string
	homeworkSubmissions []domain.HomeworkSubmission
	teachers            []domain.Teacher
	courses             []domain.Course
	nextCourseID        int
}

func (m *memStore) Init(context.Context) error { return nil }
func (m *memStore) Close() error               { return nil }

// Teacher stubs
func (m *memStore) CreateTeacher(_ context.Context, t *domain.Teacher) error {
	m.teachers = append(m.teachers, *t)
	return nil
}
func (m *memStore) GetTeacher(_ context.Context, id string) (*domain.Teacher, error) {
	for i := range m.teachers {
		if m.teachers[i].ID == id {
			item := m.teachers[i]
			return &item, nil
		}
	}
	return nil, errors.New("not found")
}
func (m *memStore) ListTeachers(context.Context) ([]domain.Teacher, error) { return m.teachers, nil }
func (m *memStore) UpdateTeacherPassword(_ context.Context, id, passwordHash string) error {
	for i := range m.teachers {
		if m.teachers[i].ID == id {
			m.teachers[i].PasswordHash = passwordHash
			m.teachers[i].UpdatedAt = time.Now()
			return nil
		}
	}
	return errors.New("not found")
}
func (m *memStore) UpdateTeacherRole(_ context.Context, id string, role domain.UserRole) error {
	for i := range m.teachers {
		if m.teachers[i].ID == id {
			m.teachers[i].Role = role
			m.teachers[i].UpdatedAt = time.Now()
			return nil
		}
	}
	return errors.New("not found")
}
func (m *memStore) DeleteTeacher(_ context.Context, id string) error {
	for i := range m.teachers {
		if m.teachers[i].ID == id {
			m.teachers = append(m.teachers[:i], m.teachers[i+1:]...)
			return nil
		}
	}
	return errors.New("not found")
}

// Course stubs
func (m *memStore) CreateCourse(_ context.Context, c *domain.Course) error {
	if m.nextCourseID == 0 {
		m.nextCourseID = 1
	}
	c.ID = m.nextCourseID
	m.nextCourseID++
	m.courses = append(m.courses, *c)
	return nil
}
func (m *memStore) GetCourse(_ context.Context, id int) (*domain.Course, error) {
	for i := range m.courses {
		if m.courses[i].ID == id {
			item := m.courses[i]
			return &item, nil
		}
	}
	return nil, errors.New("not found")
}
func (m *memStore) GetCourseByInviteCode(_ context.Context, code string) (*domain.Course, error) {
	for i := range m.courses {
		if m.courses[i].InviteCode == code {
			item := m.courses[i]
			return &item, nil
		}
	}
	return nil, errors.New("not found")
}
func (m *memStore) ListCoursesByTeacher(_ context.Context, teacherID string) ([]domain.Course, error) {
	items := make([]domain.Course, 0)
	for _, item := range m.courses {
		if item.TeacherID == teacherID {
			items = append(items, item)
		}
	}
	return items, nil
}
func (m *memStore) ListAllCourses(context.Context) ([]domain.Course, error) { return m.courses, nil }
func (m *memStore) UpdateCourse(_ context.Context, c *domain.Course) error {
	for i := range m.courses {
		if m.courses[i].ID == c.ID {
			m.courses[i] = *c
			return nil
		}
	}
	return errors.New("not found")
}
func (m *memStore) DeleteCourse(_ context.Context, id int) error {
	for i := range m.courses {
		if m.courses[i].ID == id {
			m.courses = append(m.courses[:i], m.courses[i+1:]...)
			return nil
		}
	}
	return errors.New("not found")
}

// Course state stubs
func (m *memStore) GetCourseState(context.Context, int) (*domain.CourseState, error) {
	return nil, errors.New("not found")
}
func (m *memStore) SetCourseState(context.Context, *domain.CourseState) error { return nil }

// Course-scoped attempt stubs
func (m *memStore) ListAttemptsByCourse(_ context.Context, courseID int) ([]domain.Attempt, error) {
	items := make([]domain.Attempt, 0)
	for _, item := range m.attempts {
		if item.CourseID == courseID {
			items = append(items, item)
		}
	}
	return items, nil
}
func (m *memStore) GetLiveStatsByCourse(context.Context, int) (int, int, error) { return 0, 0, nil }
func (m *memStore) GetLiveStatsByCourseQuiz(context.Context, int, string) (int, int, error) {
	return 0, 0, nil
}
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
func (m *memStore) GetInProgressAttempt(context.Context, string, string, int) (*domain.Attempt, error) {
	return nil, errors.New("not implemented")
}
func (m *memStore) UpdateAttemptSession(context.Context, string, string, string, string, int) error {
	return errors.New("not implemented")
}
func (m *memStore) UpsertAdminSummary(context.Context, int, string, string) error {
	return errors.New("not implemented")
}
func (m *memStore) GetAdminSummary(context.Context, int, string) (string, error) {
	return "", errors.New("not implemented")
}
func (m *memStore) DeleteAdminSummary(context.Context, int, string) error {
	return errors.New("not implemented")
}
func (m *memStore) ClearAttempts(context.Context, string) error { return errors.New("not implemented") }
func (m *memStore) ClearAttemptsByCourse(context.Context, int, string) error {
	return errors.New("not implemented")
}
func (m *memStore) FixLegacyAttemptsCourse(context.Context, string, int) (int, error) {
	return 0, errors.New("not implemented")
}
func (m *memStore) FixAllLegacyAttemptsCourse(context.Context, []string, int) (int, error) {
	return 0, errors.New("not implemented")
}
func (m *memStore) CreateQuizShare(context.Context, *domain.QuizShare) error {
	return errors.New("not implemented")
}
func (m *memStore) GetQuizShareByID(context.Context, int) (*domain.QuizShare, error) {
	return nil, errors.New("not implemented")
}
func (m *memStore) GetQuizShareByToken(context.Context, string) (*domain.QuizShare, error) {
	return nil, errors.New("not implemented")
}
func (m *memStore) ListActiveQuizShares(context.Context, int, string) ([]domain.QuizShare, error) {
	return nil, errors.New("not implemented")
}
func (m *memStore) RevokeQuizShare(context.Context, int) error {
	return errors.New("not implemented")
}
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
func (m *memStore) GetHomeworkSubmissionByScope(_ context.Context, courseID int, course, assignmentID, studentNo string) (*domain.HomeworkSubmission, error) {
	for i := range m.homeworkSubmissions {
		item := m.homeworkSubmissions[i]
		match := item.AssignmentID == assignmentID && item.StudentNo == studentNo
		if courseID > 0 {
			match = match && item.CourseID == courseID
		} else {
			match = match && item.Course == course
		}
		if match {
			return &item, nil
		}
	}
	return nil, sql.ErrNoRows
}
func (m *memStore) UpdateHomeworkSubmissionSession(_ context.Context, submissionID, token, name, className, secretKey string) error {
	for i := range m.homeworkSubmissions {
		if m.homeworkSubmissions[i].ID == submissionID {
			m.homeworkSubmissions[i].SessionToken = token
			m.homeworkSubmissions[i].Name = name
			m.homeworkSubmissions[i].ClassName = className
			m.homeworkSubmissions[i].SecretKey = secretKey
			m.homeworkSubmissions[i].UpdatedAt = time.Now()
			return nil
		}
	}
	return errors.New("not implemented")
}
func (m *memStore) ListHomeworkSubmissions(_ context.Context, courseID int, course, assignmentID string) ([]domain.HomeworkSubmission, error) {
	items := make([]domain.HomeworkSubmission, 0)
	for _, item := range m.homeworkSubmissions {
		if courseID > 0 {
			if item.CourseID != courseID {
				continue
			}
		} else if course != "" && item.Course != course {
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
		case domain.HomeworkSlotExtra:
			m.homeworkSubmissions[i].ExtraOriginalName = originalName
			m.homeworkSubmissions[i].ExtraUploadedAt = &now
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
		case domain.HomeworkSlotExtra:
			m.homeworkSubmissions[i].ExtraOriginalName = ""
			m.homeworkSubmissions[i].ExtraUploadedAt = nil
		default:
			return errors.New("not implemented")
		}
		return nil
	}
	return errors.New("not implemented")
}
func (m *memStore) DeleteHomeworkSubmission(_ context.Context, submissionID string) error {
	for i := range m.homeworkSubmissions {
		if m.homeworkSubmissions[i].ID == submissionID {
			m.homeworkSubmissions = append(m.homeworkSubmissions[:i], m.homeworkSubmissions[i+1:]...)
			return nil
		}
	}
	return errors.New("not found")
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
			{ID: "a1", QuizID: quiz.QuizID, Name: "张三", StudentNo: "s1", Status: domain.StatusSubmitted, AttemptNo: 1},
			{ID: "a2", QuizID: quiz.QuizID, Name: "李四", StudentNo: "s2", Status: domain.StatusSubmitted, AttemptNo: 1},
		},
		answers: map[string]map[string]string{
			"a1": {"q1": "A", "q2": "反馈1", "q3": "A", "q4": "A"},
			"a2": {"q1": "B", "q2": "反馈2", "q3": "B", "q4": "B"},
		},
	}
	s := &Server{store: st}

	in, err := s.buildAdminSummaryInput(context.Background(), quiz, 0)
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

func TestAPITeacherCoursesNormalizesEnglishName(t *testing.T) {
	st := &memStore{}
	s := New(Config{}, st)
	s.authTokens["teacher-token"] = authSession{
		TeacherID: "T01",
		Role:      domain.RoleTeacher,
		Expiry:    time.Now().Add(time.Hour),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/teacher/courses", strings.NewReader(`{
		"name":"机器学习导论",
		"slug":"  Machine   Learning Intro  "
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "auth_token", Value: "teacher-token"})
	rr := httptest.NewRecorder()

	s.apiTeacherCourses(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		OK     bool `json:"ok"`
		Course struct {
			DisplayName  string `json:"display_name"`
			InternalName string `json:"internal_name"`
			Slug         string `json:"slug"`
		} `json:"course"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if !resp.OK {
		t.Fatalf("expected ok response")
	}
	if resp.Course.DisplayName != "Machine Learning Intro" {
		t.Fatalf("display_name mismatch: %q", resp.Course.DisplayName)
	}
	if resp.Course.InternalName != "Machine_Learning_Intro" {
		t.Fatalf("internal_name mismatch: %q", resp.Course.InternalName)
	}
	if resp.Course.Slug != resp.Course.InternalName {
		t.Fatalf("legacy slug should mirror internal_name: %q", resp.Course.Slug)
	}
	if len(st.courses) != 1 {
		t.Fatalf("expected 1 stored course, got %d", len(st.courses))
	}
	if st.courses[0].DisplayName != "Machine Learning Intro" || st.courses[0].InternalName != "Machine_Learning_Intro" {
		t.Fatalf("stored course mismatch: %+v", st.courses[0])
	}
}

func TestAPITeacherMCPPersistentTokenToggle(t *testing.T) {
	st := &memStore{
		settings: map[string]string{},
		teachers: []domain.Teacher{
			{ID: "T01", Name: "教师一", Role: domain.RoleTeacher},
		},
	}
	s := New(Config{}, st)
	s.authTokens["teacher-token"] = authSession{
		TeacherID: "T01",
		Role:      domain.RoleTeacher,
		Expiry:    time.Now().Add(time.Hour),
	}

	enableReq := httptest.NewRequest(http.MethodPost, "/api/teacher/mcp", strings.NewReader(`{"enabled":true}`))
	enableReq.Header.Set("Content-Type", "application/json")
	enableReq.AddCookie(&http.Cookie{Name: "auth_token", Value: "teacher-token"})
	enableRR := httptest.NewRecorder()
	s.Routes().ServeHTTP(enableRR, enableReq)
	if enableRR.Code != http.StatusOK {
		t.Fatalf("enable status: %d body=%s", enableRR.Code, enableRR.Body.String())
	}
	var enabledResp struct {
		Enabled  bool   `json:"enabled"`
		HasToken bool   `json:"has_token"`
		Token    string `json:"token"`
	}
	if err := json.Unmarshal(enableRR.Body.Bytes(), &enabledResp); err != nil {
		t.Fatalf("unmarshal enable response: %v", err)
	}
	if !enabledResp.Enabled || !enabledResp.HasToken || enabledResp.Token == "" {
		t.Fatalf("unexpected enable response: %+v", enabledResp)
	}
	if sess := s.getAuthSessionByToken(enabledResp.Token); sess == nil || sess.TeacherID != "T01" {
		t.Fatalf("persistent token should authenticate teacher, got %+v", sess)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/teacher/mcp", nil)
	getReq.AddCookie(&http.Cookie{Name: "auth_token", Value: "teacher-token"})
	getRR := httptest.NewRecorder()
	s.Routes().ServeHTTP(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("get status: %d body=%s", getRR.Code, getRR.Body.String())
	}
	var getResp struct {
		Enabled bool   `json:"enabled"`
		Token   string `json:"token"`
	}
	if err := json.Unmarshal(getRR.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("unmarshal get response: %v", err)
	}
	if !getResp.Enabled || getResp.Token != enabledResp.Token {
		t.Fatalf("unexpected get response: %+v", getResp)
	}

	disableReq := httptest.NewRequest(http.MethodPost, "/api/teacher/mcp", strings.NewReader(`{"enabled":false}`))
	disableReq.Header.Set("Content-Type", "application/json")
	disableReq.AddCookie(&http.Cookie{Name: "auth_token", Value: "teacher-token"})
	disableRR := httptest.NewRecorder()
	s.Routes().ServeHTTP(disableRR, disableReq)
	if disableRR.Code != http.StatusOK {
		t.Fatalf("disable status: %d body=%s", disableRR.Code, disableRR.Body.String())
	}
	if sess := s.getAuthSessionByToken(enabledResp.Token); sess != nil {
		t.Fatalf("disabled persistent token should not authenticate, got %+v", sess)
	}
}

func TestAPICourseByInviteCodeReturnsDisplayAndInternalName(t *testing.T) {
	st := &memStore{
		courses: []domain.Course{{
			ID:           1,
			TeacherID:    "T01",
			Name:         "机器学习导论",
			DisplayName:  "Machine Learning Intro",
			InternalName: "Machine_Learning_Intro",
			Slug:         "Machine_Learning_Intro",
			InviteCode:   "ABC123",
		}},
	}
	s := New(Config{}, st)

	req := httptest.NewRequest(http.MethodGet, "/api/course?code=ABC123", nil)
	rr := httptest.NewRecorder()
	s.apiCourseByInviteCode(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if resp["display_name"] != "Machine Learning Intro" {
		t.Fatalf("unexpected display_name: %#v", resp["display_name"])
	}
	if resp["internal_name"] != "Machine_Learning_Intro" {
		t.Fatalf("unexpected internal_name: %#v", resp["internal_name"])
	}
	if resp["slug"] != "Machine_Learning_Intro" {
		t.Fatalf("unexpected legacy slug: %#v", resp["slug"])
	}
}

func TestAPIAdminOverviewIncludesOnlineCount(t *testing.T) {
	now := time.Now()
	st := &memStore{
		teachers: []domain.Teacher{
			{ID: "admin", Name: "管理员", Role: domain.RoleAdmin},
			{ID: "t1", Name: "教师一", Role: domain.RoleTeacher},
		},
		attempts: []domain.Attempt{
			{ID: "a1", StudentNo: "S001", UpdatedAt: now.Add(-5 * time.Minute)},
			{ID: "a2", StudentNo: "S001", UpdatedAt: now.Add(-3 * time.Minute)},
			{ID: "a3", StudentNo: "S002", UpdatedAt: now.Add(-20 * time.Minute)},
		},
		homeworkSubmissions: []domain.HomeworkSubmission{
			{ID: "h1", StudentNo: "S003", UpdatedAt: now.Add(-4 * time.Minute)},
			{ID: "h2", StudentNo: "S002", UpdatedAt: now.Add(-2 * time.Minute)},
			{ID: "h3", StudentNo: "S004", UpdatedAt: now.Add(-16 * time.Minute)},
		},
	}
	s := New(Config{}, st)
	s.authTokens["admin-token"] = authSession{
		TeacherID: "admin",
		Role:      domain.RoleAdmin,
		Expiry:    now.Add(time.Hour),
	}
	s.authTokens["teacher-token"] = authSession{
		TeacherID: "t1",
		Role:      domain.RoleTeacher,
		Expiry:    now.Add(time.Hour),
	}
	s.authTokens["expired-token"] = authSession{
		TeacherID: "t-expired",
		Role:      domain.RoleTeacher,
		Expiry:    now.Add(-time.Minute),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/system/overview", nil)
	req.AddCookie(&http.Cookie{Name: "auth_token", Value: "admin-token"})
	rr := httptest.NewRecorder()

	s.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if got := int(resp["online_student_count"].(float64)); got != 3 {
		t.Fatalf("unexpected online_student_count: %d", got)
	}
	if got := int(resp["online_teacher_count"].(float64)); got != 2 {
		t.Fatalf("unexpected online_teacher_count: %d", got)
	}
	if got := int(resp["online_count"].(float64)); got != 5 {
		t.Fatalf("unexpected online_count: %d", got)
	}
	if got := int(resp["online_window_minutes"].(float64)); got != 15 {
		t.Fatalf("unexpected online_window_minutes: %d", got)
	}
}

func TestTeacherHomeworkDownloadUsesStructuredFilename(t *testing.T) {
	now := time.Now()
	st := &memStore{
		teachers: []domain.Teacher{
			{ID: "t1", Name: "教师一", Role: domain.RoleTeacher},
		},
		courses: []domain.Course{
			{ID: 1, TeacherID: "t1", Slug: "course_a", InternalName: "course_a"},
		},
		homeworkSubmissions: []domain.HomeworkSubmission{
			{
				ID:                 "sub-1",
				CourseID:           1,
				Course:             "course_a",
				AssignmentID:       "task_1",
				Name:               "张三",
				StudentNo:          "2023001",
				ClassName:          "计科1班",
				ReportOriginalName: "原始文件.pdf",
				UpdatedAt:          now,
			},
		},
	}
	tmpDir := t.TempDir()
	s := New(Config{MetadataDir: tmpDir}, st)
	s.authTokens["teacher-token"] = authSession{
		TeacherID: "t1",
		Role:      domain.RoleTeacher,
		Expiry:    now.Add(time.Hour),
	}
	submission := &st.homeworkSubmissions[0]
	dir := s.homeworkSubmissionDir(submission)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "report.pdf"), []byte("%PDF-1.4 test"), 0o644); err != nil {
		t.Fatalf("write report failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/teacher/courses/homework/submissions/download?course_id=1&id=sub-1&slot=report&download=1", nil)
	req.AddCookie(&http.Cookie{Name: "auth_token", Value: "teacher-token"})
	rr := httptest.NewRecorder()
	s.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Disposition"); !strings.Contains(got, `filename="计科1班_task_1_张三_2023001.pdf"`) {
		t.Fatalf("unexpected content disposition: %s", got)
	}
}

func TestBuildHomeworkBulkArchiveUsesStructuredNames(t *testing.T) {
	st := &memStore{
		courses: []domain.Course{
			{ID: 1, TeacherID: "t1", Slug: "course_a", InternalName: "course_a"},
		},
	}
	tmpDir := t.TempDir()
	s := New(Config{MetadataDir: tmpDir}, st)
	submission := domain.HomeworkSubmission{
		ID:                 "sub-1",
		CourseID:           1,
		Course:             "course_a",
		AssignmentID:       "task_1",
		Name:               "张三",
		StudentNo:          "2023001",
		ClassName:          "计科1班",
		ReportOriginalName: "原始文件.pdf",
	}
	dir := s.homeworkSubmissionDir(&submission)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "report.pdf"), []byte("%PDF-1.4 test"), 0o644); err != nil {
		t.Fatalf("write report failed: %v", err)
	}

	data, err := s.buildHomeworkBulkArchive([]domain.HomeworkSubmission{submission})
	if err != nil {
		t.Fatalf("build archive failed: %v", err)
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open zip failed: %v", err)
	}
	if len(reader.File) != 1 {
		t.Fatalf("unexpected zip file count: %d", len(reader.File))
	}
	if got := reader.File[0].Name; got != "计科1班_task_1_张三_2023001/计科1班_task_1_张三_2023001.pdf" {
		t.Fatalf("unexpected zip entry name: %s", got)
	}
}

// TestAPIStudentSignoutRejectsGet documents the CSRF / accidental-trigger
// guard added so a stray GET (link prefetch, address-bar typo, hostile
// <img src>, etc.) cannot quietly clear an in-progress student session.
func TestAPIStudentSignoutRejectsGet(t *testing.T) {
	st := &memStore{}
	s := New(Config{}, st)

	req := httptest.NewRequest(http.MethodGet, "/api/student-signout", nil)
	rr := httptest.NewRecorder()
	s.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET, got %d body=%s", rr.Code, rr.Body.String())
	}
	for _, c := range rr.Result().Cookies() {
		if c.Name == "student_token" && c.MaxAge == -1 {
			t.Fatalf("GET should not clear student_token cookie")
		}
	}
}

func TestAPIStudentSignoutAcceptsPost(t *testing.T) {
	st := &memStore{}
	s := New(Config{}, st)

	req := httptest.NewRequest(http.MethodPost, "/api/student-signout", nil)
	rr := httptest.NewRecorder()
	s.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for POST, got %d body=%s", rr.Code, rr.Body.String())
	}
	found := false
	for _, c := range rr.Result().Cookies() {
		if c.Name == "student_token" && c.MaxAge == -1 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("POST should clear student_token cookie via Set-Cookie")
	}
}

// TestAPITeacherCoursesAcceptsDisplayNameOnly guards against a regression
// where supplying only display_name (or only internal_name) blanked the
// counterpart and produced a confusing 400.
func TestAPITeacherCoursesAcceptsDisplayNameOnly(t *testing.T) {
	st := &memStore{}
	s := New(Config{}, st)
	s.authTokens["teacher-token"] = authSession{
		TeacherID: "T01",
		Role:      domain.RoleTeacher,
		Expiry:    time.Now().Add(time.Hour),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/teacher/courses", strings.NewReader(`{
		"name":"机器学习导论",
		"display_name":"Machine Learning Intro"
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "auth_token", Value: "teacher-token"})
	rr := httptest.NewRecorder()
	s.apiTeacherCourses(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rr.Code, rr.Body.String())
	}
	if len(st.courses) != 1 {
		t.Fatalf("expected 1 stored course, got %d", len(st.courses))
	}
	if st.courses[0].DisplayName != "Machine Learning Intro" {
		t.Fatalf("unexpected display_name: %q", st.courses[0].DisplayName)
	}
	if st.courses[0].InternalName != "Machine_Learning_Intro" {
		t.Fatalf("internal_name should be derived from display_name, got %q", st.courses[0].InternalName)
	}
}

func TestAPITeacherCoursesAcceptsInternalNameOnly(t *testing.T) {
	st := &memStore{}
	s := New(Config{}, st)
	s.authTokens["teacher-token"] = authSession{
		TeacherID: "T01",
		Role:      domain.RoleTeacher,
		Expiry:    time.Now().Add(time.Hour),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/teacher/courses", strings.NewReader(`{
		"name":"机器学习导论",
		"internal_name":"Machine_Learning_Intro"
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "auth_token", Value: "teacher-token"})
	rr := httptest.NewRecorder()
	s.apiTeacherCourses(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rr.Code, rr.Body.String())
	}
	if len(st.courses) != 1 {
		t.Fatalf("expected 1 stored course, got %d", len(st.courses))
	}
	if st.courses[0].InternalName != "Machine_Learning_Intro" {
		t.Fatalf("unexpected internal_name: %q", st.courses[0].InternalName)
	}
	if st.courses[0].DisplayName == "" {
		t.Fatalf("display_name should be derived from internal_name, got empty")
	}
}
