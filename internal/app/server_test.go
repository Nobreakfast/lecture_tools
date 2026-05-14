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
	shortAnswerGrades   map[string]map[string]domain.ShortAnswerGrade
	settings            map[string]string
	homeworkSubmissions []domain.HomeworkSubmission
	homeworkQA          []domain.HomeworkQA
	qaIssues            []domain.QAIssue
	qaMessages          []domain.QAMessage
	nextQAIssueID       int
	nextQAMessageID     int
	teachers            []domain.Teacher
	courses             []domain.Course
	courseTeachers      []domain.CourseTeacher
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
	seen := map[int]bool{}
	for _, item := range m.courses {
		if item.TeacherID == teacherID {
			items = append(items, item)
			seen[item.ID] = true
		}
	}
	for _, ct := range m.courseTeachers {
		if ct.TeacherID != teacherID || seen[ct.CourseID] {
			continue
		}
		for _, item := range m.courses {
			if item.ID == ct.CourseID {
				items = append(items, item)
				seen[item.ID] = true
			}
		}
	}
	return items, nil
}
func (m *memStore) ListAllCourses(context.Context) ([]domain.Course, error) { return m.courses, nil }
func (m *memStore) AddCourseTeacher(_ context.Context, ct *domain.CourseTeacher) error {
	for i := range m.courseTeachers {
		if m.courseTeachers[i].CourseID == ct.CourseID && m.courseTeachers[i].TeacherID == ct.TeacherID {
			m.courseTeachers[i].Permission = ct.Permission
			m.courseTeachers[i].UpdatedAt = ct.UpdatedAt
			return nil
		}
	}
	m.courseTeachers = append(m.courseTeachers, *ct)
	return nil
}
func (m *memStore) GetCourseTeacher(_ context.Context, courseID int, teacherID string) (*domain.CourseTeacher, error) {
	for i := range m.courseTeachers {
		if m.courseTeachers[i].CourseID == courseID && m.courseTeachers[i].TeacherID == teacherID {
			item := m.courseTeachers[i]
			return &item, nil
		}
	}
	return nil, errors.New("not found")
}
func (m *memStore) ListCourseTeachers(_ context.Context, courseID int) ([]domain.CourseTeacher, error) {
	items := make([]domain.CourseTeacher, 0)
	for _, item := range m.courseTeachers {
		if item.CourseID == courseID {
			items = append(items, item)
		}
	}
	return items, nil
}
func (m *memStore) UpdateCourseTeacherPermission(_ context.Context, courseID int, teacherID string, permission domain.CoursePermission) error {
	for i := range m.courseTeachers {
		if m.courseTeachers[i].CourseID == courseID && m.courseTeachers[i].TeacherID == teacherID {
			m.courseTeachers[i].Permission = permission
			return nil
		}
	}
	return errors.New("not found")
}
func (m *memStore) RemoveCourseTeacher(_ context.Context, courseID int, teacherID string) error {
	for i := range m.courseTeachers {
		if m.courseTeachers[i].CourseID == courseID && m.courseTeachers[i].TeacherID == teacherID {
			m.courseTeachers = append(m.courseTeachers[:i], m.courseTeachers[i+1:]...)
			return nil
		}
	}
	return errors.New("not found")
}
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
func (m *memStore) GetAttemptByToken(_ context.Context, token string) (*domain.Attempt, error) {
	for i := range m.attempts {
		if m.attempts[i].SessionToken == "" {
			continue
		}
		if m.attempts[i].SessionToken == token {
			item := m.attempts[i]
			return &item, nil
		}
	}
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
func (m *memStore) UpsertShortAnswerGrade(_ context.Context, grade domain.ShortAnswerGrade) error {
	if m.shortAnswerGrades == nil {
		m.shortAnswerGrades = map[string]map[string]domain.ShortAnswerGrade{}
	}
	if m.shortAnswerGrades[grade.AttemptID] == nil {
		m.shortAnswerGrades[grade.AttemptID] = map[string]domain.ShortAnswerGrade{}
	}
	m.shortAnswerGrades[grade.AttemptID][grade.QuestionID] = grade
	return nil
}
func (m *memStore) GetShortAnswerGrades(_ context.Context, attemptID string) (map[string]domain.ShortAnswerGrade, error) {
	if m.shortAnswerGrades == nil {
		return map[string]domain.ShortAnswerGrade{}, nil
	}
	if got, ok := m.shortAnswerGrades[attemptID]; ok {
		return got, nil
	}
	return map[string]domain.ShortAnswerGrade{}, nil
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
func (m *memStore) SaveHomeworkGrade(_ context.Context, submissionID string, score *float64, feedback string) error {
	for i := range m.homeworkSubmissions {
		if m.homeworkSubmissions[i].ID != submissionID {
			continue
		}
		now := time.Now()
		if m.homeworkSubmissions[i].GradedAt == nil {
			m.homeworkSubmissions[i].GradedAt = &now
		}
		m.homeworkSubmissions[i].Score = score
		m.homeworkSubmissions[i].Feedback = feedback
		m.homeworkSubmissions[i].GradeUpdatedAt = &now
		m.homeworkSubmissions[i].UpdatedAt = now
		return nil
	}
	return errors.New("not found")
}
func (m *memStore) SaveHomeworkAIPregrade(_ context.Context, submissionID string, score *float64, feedback, prompt, errorMessage string) error {
	for i := range m.homeworkSubmissions {
		if m.homeworkSubmissions[i].ID != submissionID {
			continue
		}
		now := time.Now()
		m.homeworkSubmissions[i].AIPregradeScore = score
		m.homeworkSubmissions[i].AIPregradeFeedback = feedback
		m.homeworkSubmissions[i].AIPregradePrompt = prompt
		m.homeworkSubmissions[i].AIPregradedAt = &now
		m.homeworkSubmissions[i].AIPregradeError = errorMessage
		m.homeworkSubmissions[i].UpdatedAt = now
		return nil
	}
	return errors.New("not found")
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
func (m *memStore) CreateHomeworkQuestion(_ context.Context, qa *domain.HomeworkQA) error {
	m.homeworkQA = append(m.homeworkQA, *qa)
	return nil
}
func (m *memStore) ListHomeworkQA(_ context.Context, courseID int, course, assignmentID string, includeUnanswered, includeHidden bool) ([]domain.HomeworkQA, error) {
	items := make([]domain.HomeworkQA, 0)
	for _, item := range m.homeworkQA {
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
		if !includeUnanswered && strings.TrimSpace(item.Answer) == "" {
			continue
		}
		if !includeHidden && item.Hidden {
			continue
		}
		items = append(items, item)
	}
	return items, nil
}
func (m *memStore) GetHomeworkQAByID(_ context.Context, id string) (*domain.HomeworkQA, error) {
	for i := range m.homeworkQA {
		if m.homeworkQA[i].ID == id {
			item := m.homeworkQA[i]
			return &item, nil
		}
	}
	return nil, sql.ErrNoRows
}
func (m *memStore) AnswerHomeworkQuestion(_ context.Context, id, answer string, answerImages []string) error {
	for i := range m.homeworkQA {
		if m.homeworkQA[i].ID == id {
			now := time.Now()
			m.homeworkQA[i].Answer = answer
			m.homeworkQA[i].AnswerImages = answerImages
			m.homeworkQA[i].AnsweredAt = &now
			m.homeworkQA[i].UpdatedAt = now
			return nil
		}
	}
	return sql.ErrNoRows
}
func (m *memStore) SetHomeworkQuestionPinned(_ context.Context, id string, pinned bool) error {
	for i := range m.homeworkQA {
		if m.homeworkQA[i].ID == id {
			m.homeworkQA[i].Pinned = pinned
			m.homeworkQA[i].UpdatedAt = time.Now()
			return nil
		}
	}
	return sql.ErrNoRows
}
func (m *memStore) SetHomeworkQuestionHidden(_ context.Context, id string, hidden bool) error {
	for i := range m.homeworkQA {
		if m.homeworkQA[i].ID == id {
			m.homeworkQA[i].Hidden = hidden
			m.homeworkQA[i].UpdatedAt = time.Now()
			return nil
		}
	}
	return sql.ErrNoRows
}

// QAIssueStore stubs
func (m *memStore) CreateQAIssue(_ context.Context, issue *domain.QAIssue) (int64, error) {
	if m.nextQAIssueID == 0 {
		m.nextQAIssueID = 1
	}
	issue.ID = m.nextQAIssueID
	m.nextQAIssueID++
	m.qaIssues = append(m.qaIssues, *issue)
	return int64(issue.ID), nil
}
func (m *memStore) GetQAIssueByID(_ context.Context, id int) (*domain.QAIssue, error) {
	for i := range m.qaIssues {
		if m.qaIssues[i].ID == id {
			item := m.qaIssues[i]
			return &item, nil
		}
	}
	return nil, sql.ErrNoRows
}
func (m *memStore) ListQAIssues(_ context.Context, courseID int, assignmentID string, includeHidden bool) ([]domain.QAIssue, error) {
	items := make([]domain.QAIssue, 0)
	for _, item := range m.qaIssues {
		if courseID > 0 && item.CourseID != courseID {
			continue
		}
		if strings.TrimSpace(assignmentID) != "" && item.AssignmentID != assignmentID {
			continue
		}
		if item.Hidden && !includeHidden {
			continue
		}
		items = append(items, item)
	}
	return items, nil
}
func (m *memStore) ListQAIssuesByCourse(ctx context.Context, courseID int, includeHidden bool) ([]domain.QAIssue, error) {
	return m.ListQAIssues(ctx, courseID, "", includeHidden)
}
func (m *memStore) UpdateQAIssueStatus(_ context.Context, id int, status string) error {
	for i := range m.qaIssues {
		if m.qaIssues[i].ID == id {
			m.qaIssues[i].Status = status
			m.qaIssues[i].UpdatedAt = time.Now()
			return nil
		}
	}
	return sql.ErrNoRows
}
func (m *memStore) UpdateQAIssueQuestion(_ context.Context, id int, title, question string) error {
	for i := range m.qaIssues {
		if m.qaIssues[i].ID == id {
			m.qaIssues[i].Title = title
			m.qaIssues[i].UpdatedAt = time.Now()
			for j := range m.qaMessages {
				if m.qaMessages[j].IssueID == id && m.qaMessages[j].Sender == "student" {
					m.qaMessages[j].Content = question
					return nil
				}
			}
			return sql.ErrNoRows
		}
	}
	return sql.ErrNoRows
}
func (m *memStore) SetQAIssuePinned(_ context.Context, id int, pinned bool) error {
	for i := range m.qaIssues {
		if m.qaIssues[i].ID == id {
			m.qaIssues[i].Pinned = pinned
			m.qaIssues[i].UpdatedAt = time.Now()
			return nil
		}
	}
	return sql.ErrNoRows
}
func (m *memStore) SetQAIssueHidden(_ context.Context, id int, hidden bool) error {
	for i := range m.qaIssues {
		if m.qaIssues[i].ID == id {
			m.qaIssues[i].Hidden = hidden
			m.qaIssues[i].UpdatedAt = time.Now()
			return nil
		}
	}
	return sql.ErrNoRows
}
func (m *memStore) IncrementQAIssueMessageCount(_ context.Context, id int) error {
	for i := range m.qaIssues {
		if m.qaIssues[i].ID == id {
			m.qaIssues[i].MessageCount++
			m.qaIssues[i].UpdatedAt = time.Now()
			return nil
		}
	}
	return sql.ErrNoRows
}
func (m *memStore) CreateQAMessage(_ context.Context, msg *domain.QAMessage) (int64, error) {
	if m.nextQAMessageID == 0 {
		m.nextQAMessageID = 1
	}
	msg.ID = m.nextQAMessageID
	m.nextQAMessageID++
	m.qaMessages = append(m.qaMessages, *msg)
	return int64(msg.ID), nil
}
func (m *memStore) ListQAMessages(_ context.Context, issueID int) ([]domain.QAMessage, error) {
	items := make([]domain.QAMessage, 0)
	for _, item := range m.qaMessages {
		if item.IssueID == issueID {
			items = append(items, item)
		}
	}
	return items, nil
}
func (m *memStore) UpdateAttemptStudentInfo(_ context.Context, _, _, _, _ string) error {
	return errors.New("not implemented")
}
func (m *memStore) MergeAttemptStudent(_ context.Context, _, _, _, _, _, _ string, _ int) (int64, error) {
	return 0, errors.New("not implemented")
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

func TestBuildAdminSummaryInputNormalizesLabShortAnswerSamples(t *testing.T) {
	quiz := &domain.Quiz{
		QuizID: "lab-1",
		Title:  "实验课",
		Questions: []domain.Question{
			{ID: "q1", Type: domain.QuestionShortAnswer, Stem: "请粘贴实验代码"},
		},
	}
	rawCode := "```go\nfunc main() {\n\tfmt.Println(\"实验完成\", `特殊符号`)\n}\n```\x00"
	st := &memStore{
		attempts: []domain.Attempt{
			{ID: "a1", QuizID: quiz.QuizID, Name: "张三", StudentNo: "s1", Status: domain.StatusSubmitted, AttemptNo: 1},
		},
		answers: map[string]map[string]string{
			"a1": {"q1": rawCode},
		},
	}
	s := &Server{store: st}

	in, err := s.buildAdminSummaryInput(context.Background(), quiz, 0)
	if err != nil {
		t.Fatalf("buildAdminSummaryInput failed: %v", err)
	}
	if len(in.FeedbackItems) != 1 || len(in.FeedbackItems[0].TextSamples) != 1 {
		t.Fatalf("unexpected feedback items: %+v", in.FeedbackItems)
	}
	sample := in.FeedbackItems[0].TextSamples[0]
	if strings.ContainsAny(sample, "\n\r\t`\"\\") || strings.Contains(sample, "\x00") {
		t.Fatalf("sample was not normalized: %q", sample)
	}
	if !strings.Contains(sample, "func main") || !strings.Contains(sample, "特殊符号") {
		t.Fatalf("sample lost useful content: %q", sample)
	}
}

func TestBuildResultIncludesAgentScoredShortAnswer(t *testing.T) {
	score := 0.8
	gradedAt := time.Now()
	quiz := &domain.Quiz{
		QuizID: "quiz-agent-short",
		Title:  "Agent 简答评分",
		Questions: []domain.Question{
			{ID: "q1", Type: domain.QuestionSingleChoice, Stem: "1+1", CorrectAnswer: "A", Options: []domain.Option{{Key: "A", Text: "2"}, {Key: "B", Text: "3"}}},
			{ID: "q2", Type: domain.QuestionShortAnswer, Stem: "解释凸函数定义", ReferenceAnswer: "满足 Jensen 不等式", ScoreWithAgent: true},
		},
	}
	st := &memStore{
		attempts: []domain.Attempt{{ID: "a1", QuizID: quiz.QuizID, Name: "张三", StudentNo: "s1", Status: domain.StatusSubmitted, AttemptNo: 1}},
		answers: map[string]map[string]string{
			"a1": {"q1": "A", "q2": "凸函数满足 Jensen 不等式"},
		},
		shortAnswerGrades: map[string]map[string]domain.ShortAnswerGrade{
			"a1": {
				"q2": {
					AttemptID:  "a1",
					QuestionID: "q2",
					Status:     domain.ShortAnswerGradeGraded,
					Score:      &score,
					Feedback:   "核心概念正确。",
					GradedAt:   &gradedAt,
					UpdatedAt:  gradedAt,
				},
			},
		},
	}
	s := &Server{store: st, currentQuiz: quiz}

	res, err := s.buildResult(context.Background(), &st.attempts[0])
	if err != nil {
		t.Fatalf("buildResult failed: %v", err)
	}
	scorePayload, _ := res["score"].(map[string]any)
	if scorePayload["correct"] != 1.8 || scorePayload["total"] != 2 {
		t.Fatalf("unexpected score payload: %+v", scorePayload)
	}
	questions, _ := res["questions"].([]map[string]any)
	shortQ := questions[1]
	if shortQ["score_with_agent"] != true {
		t.Fatalf("short answer should expose score_with_agent: %+v", shortQ)
	}
	grade, _ := shortQ["short_answer_grade"].(map[string]any)
	if grade["status"] != string(domain.ShortAnswerGradeGraded) || grade["score"] != score {
		t.Fatalf("unexpected short answer grade payload: %+v", grade)
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

func TestAPIStudentMCPRouteIsNotExposed(t *testing.T) {
	st := &memStore{
		settings: map[string]string{},
		homeworkSubmissions: []domain.HomeworkSubmission{{
			ID:           "sub-1",
			SessionToken: "homework-token",
			CourseID:     1,
			Course:       "course-one",
			AssignmentID: "hw1",
			Name:         "学生一",
			StudentNo:    "S01",
			ClassName:    "一班",
		}},
	}
	s := New(Config{}, st)

	req := httptest.NewRequest(http.MethodPost, "/api/student/mcp", strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: homeworkCookieName, Value: "homework-token"})
	rr := httptest.NewRecorder()
	s.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("student MCP route status = %d, want 404: %s", rr.Code, rr.Body.String())
	}
}

func TestStudentAgentPromptUsesInternalContextWithoutMCPConfig(t *testing.T) {
	st := &memStore{
		courses: []domain.Course{{ID: 1, TeacherID: "T01", Name: "机器学习", Slug: "ml", DisplayName: "机器学习"}},
		homeworkSubmissions: []domain.HomeworkSubmission{{
			ID:                 "sub-1",
			SessionToken:       "homework-token",
			CourseID:           1,
			Course:             "ml",
			AssignmentID:       "hw1",
			Name:               "学生一",
			StudentNo:          "S01",
			ClassName:          "一班",
			ReportOriginalName: "report.pdf",
		}},
		attempts: []domain.Attempt{{ID: "a1", CourseID: 1, QuizID: "quiz-1", Name: "学生一", StudentNo: "S01", ClassName: "一班", AttemptNo: 1, Status: domain.StatusSubmitted, UpdatedAt: time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)}},
	}
	s := New(Config{}, st)
	req := httptest.NewRequest(http.MethodPost, "/api/student/agent/chat", nil)
	prompt := s.studentAgentPrompt(req, &st.homeworkSubmissions[0], "我前几次小测表现如何？", nil)
	if !strings.Contains(prompt, "历史小测内部结果") || !strings.Contains(prompt, "quiz-1") || !strings.Contains(prompt, "当前作业内部结果") || !strings.Contains(prompt, "report.pdf") {
		t.Fatalf("student agent prompt missing internal context: %s", prompt)
	}
	if strings.Contains(strings.ToLower(prompt), "token") || strings.Contains(prompt, "/mcp/sse") {
		t.Fatalf("student agent prompt should not expose MCP config or token details: %s", prompt)
	}
}

func TestStudentAgentPromptUsesVisibleQAMaterialsAndAssignmentContext(t *testing.T) {
	st := &memStore{
		courses: []domain.Course{{ID: 1, TeacherID: "T01", Name: "机器人", Slug: "robot", DisplayName: "机器人"}},
		homeworkSubmissions: []domain.HomeworkSubmission{{
			ID:           "sub-1",
			SessionToken: "homework-token",
			CourseID:     1,
			Course:       "robot",
			AssignmentID: "hw1",
			Name:         "学生一",
			StudentNo:    "S01",
			ClassName:    "一班",
		}},
		qaIssues: []domain.QAIssue{{
			ID:           7,
			CourseID:     1,
			Course:       "robot",
			AssignmentID: "hw1",
			StudentNo:    "S01",
			Title:        "提交格式",
			Status:       "resolved",
			MessageCount: 2,
			UpdatedAt:    time.Now(),
		}},
		qaMessages: []domain.QAMessage{
			{IssueID: 7, Sender: "student", Content: "报告提交格式是什么？"},
			{IssueID: 7, Sender: "teacher", Content: "报告请提交 PDF，代码请提交 zip。"},
		},
	}
	s := New(Config{MetadataDir: t.TempDir()}, st)
	materialDir := s.metadataMaterialsDir("T01", "robot")
	if err := os.MkdirAll(materialDir, 0o755); err != nil {
		t.Fatalf("mkdir material dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(materialDir, "visible.md"), []byte("可见资料内容：传感器复习重点。"), 0o644); err != nil {
		t.Fatalf("write visible material: %v", err)
	}
	assignmentDir := s.metadataHomeworkAssignmentDir("T01", "robot", "hw1")
	if err := os.MkdirAll(assignmentDir, 0o755); err != nil {
		t.Fatalf("mkdir assignment dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(assignmentDir, "guide.md"), []byte("作业说明：提交 PDF 报告。"), 0o644); err != nil {
		t.Fatalf("write assignment guide: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/student/agent/chat", nil)
	prompt := s.studentAgentPrompt(req, &st.homeworkSubmissions[0], "报告提交格式是什么？请结合资料", nil)
	for _, want := range []string{"已有 Q&A 检索结果", "报告请提交 PDF", "学生可见课程资料", "visible.md", "可见资料内容", "当前作业可见资料", "作业说明"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("student agent prompt missing %q: %s", want, prompt)
		}
	}
}

func TestQAIssuePayloadOmitsStudentIdentity(t *testing.T) {
	payload := qaIssuePayload(domain.QAIssue{
		ID:           1,
		CourseID:     7,
		AssignmentID: "hw1",
		StudentNo:    "S001",
		Title:        "问题",
		Status:       "open",
	})
	if _, ok := payload["student_no"]; ok {
		t.Fatalf("Q&A payload should not expose student_no: %+v", payload)
	}
}

func TestStudentMCPCreateQAIssuePersistsStudentIdentityButRedactsQuestion(t *testing.T) {
	st := &memStore{
		courses: []domain.Course{{ID: 1, TeacherID: "T01", Name: "机器人", Slug: "robot", DisplayName: "机器人"}},
	}
	s := New(Config{}, st)
	submission := &domain.HomeworkSubmission{
		CourseID:     1,
		Course:       "robot",
		AssignmentID: "hw1",
		Name:         "学生一",
		StudentNo:    "S01",
		ClassName:    "一班",
	}
	if _, err := s.studentMCPCreateQAIssue(context.Background(), submission, "学生一同学询问提交格式", "学生一同学询问：S01 报告提交格式是什么？"); err != nil {
		t.Fatalf("studentMCPCreateQAIssue failed: %v", err)
	}
	if len(st.qaIssues) != 1 {
		t.Fatalf("expected one Q&A issue, got %+v", st.qaIssues)
	}
	if st.qaIssues[0].StudentNo != "S01" {
		t.Fatalf("Q&A issue should store student_no internally, got %+v", st.qaIssues[0])
	}
	if strings.Contains(st.qaIssues[0].Title, "学生一") || strings.Contains(st.qaIssues[0].Title, "S01") {
		t.Fatalf("Q&A title should redact student identity, got %+v", st.qaIssues[0])
	}
	if len(st.qaMessages) != 1 || strings.Contains(st.qaMessages[0].Content, "学生一") || strings.Contains(st.qaMessages[0].Content, "S01") {
		t.Fatalf("Q&A message should redact student identity, got %+v", st.qaMessages)
	}
}

func TestStudentAgentHiddenMaterialsStayOutOfPrompt(t *testing.T) {
	st := &memStore{
		courses: []domain.Course{{ID: 1, TeacherID: "T01", Name: "机器人", Slug: "robot", DisplayName: "机器人"}},
		homeworkSubmissions: []domain.HomeworkSubmission{{
			ID:           "sub-1",
			SessionToken: "homework-token",
			CourseID:     1,
			Course:       "robot",
			AssignmentID: "hw1",
			Name:         "学生一",
			StudentNo:    "S01",
			ClassName:    "一班",
		}},
	}
	s := New(Config{MetadataDir: t.TempDir()}, st)
	materialDir := s.metadataMaterialsDir("T01", "robot")
	if err := os.MkdirAll(materialDir, 0o755); err != nil {
		t.Fatalf("mkdir material dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(materialDir, "visible.md"), []byte("公开复习资料"), 0o644); err != nil {
		t.Fatalf("write visible material: %v", err)
	}
	if err := os.WriteFile(filepath.Join(materialDir, "hidden.md"), []byte("隐藏答案资料"), 0o644); err != nil {
		t.Fatalf("write hidden material: %v", err)
	}
	if err := s.setMaterialVisibility(context.Background(), "robot", "hidden.md", false); err != nil {
		t.Fatalf("set hidden material visibility: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/student/agent/chat", nil)
	prompt := s.studentAgentPrompt(req, &st.homeworkSubmissions[0], "请读取隐藏资料 hidden.md", nil)
	if strings.Contains(prompt, "资料 hidden.md 的可读取内容") || strings.Contains(prompt, "隐藏答案资料") {
		t.Fatalf("student agent prompt exposed hidden material: %s", prompt)
	}
	if !strings.Contains(prompt, "visible.md") {
		t.Fatalf("student agent prompt should still include visible material listing: %s", prompt)
	}
	if _, err := s.callAgentTool(context.Background(), "read_visible_material_text", agentToolContext{Student: &st.homeworkSubmissions[0]}, map[string]any{"material_file": "hidden.md"}); err == nil {
		t.Fatalf("hidden material read should fail")
	}
}

func TestParseStudentAgentDecisionAcceptsFencedJSON(t *testing.T) {
	raw := "```json\n{\"action\":\"create_qa\",\"answer\":\"已整理\",\"qa_title\":\"作业要求\",\"qa_summary\":\"学生想确认提交格式\"}\n```"
	decision, ok := parseStudentAgentDecision(raw)
	if !ok {
		t.Fatalf("expected fenced JSON to parse")
	}
	if decision.Action != "create_qa" || decision.QATitle != "作业要求" || decision.QASummary != "学生想确认提交格式" {
		t.Fatalf("unexpected decision: %+v", decision)
	}
}

func TestTruncateAgentTextCapsLongInput(t *testing.T) {
	long := strings.Repeat("问", agentMaxMessageRunes+10)
	got := truncateAgentText(long)
	if len([]rune(got)) != agentMaxMessageRunes+3 || !strings.HasSuffix(got, "...") {
		t.Fatalf("truncateAgentText length/suffix mismatch: len=%d suffix=%q", len([]rune(got)), got[len(got)-3:])
	}
}

func TestAgentToolRegistryEnforcesTeacherWritePermission(t *testing.T) {
	st := &memStore{
		teachers:       []domain.Teacher{{ID: "owner", Name: "Owner", Role: domain.RoleTeacher}, {ID: "assistant", Name: "Assistant", Role: domain.RoleTeacher}},
		courses:        []domain.Course{{ID: 1, TeacherID: "owner", Name: "AI", Slug: "ai", InternalName: "ai", DisplayName: "AI"}},
		courseTeachers: []domain.CourseTeacher{{CourseID: 1, TeacherID: "assistant", Permission: domain.CoursePermissionView}},
	}
	s := New(Config{}, st)
	_, err := s.callAgentTool(context.Background(), "set_quiz_entry_open",
		agentToolContext{Session: &authSession{TeacherID: "assistant", Role: domain.RoleTeacher}, Platform: true, Confirmed: true},
		map[string]any{"course_id": 1, "open": true},
	)
	if err == nil || !strings.Contains(err.Error(), "无权限修改") {
		t.Fatalf("read-only collaborator write err = %v, want permission error", err)
	}
}

func TestAgentToolRegistryRequiresPlatformConfirmationForWrites(t *testing.T) {
	st := &memStore{
		teachers: []domain.Teacher{{ID: "owner", Name: "Owner", Role: domain.RoleTeacher}},
		courses:  []domain.Course{{ID: 1, TeacherID: "owner", Name: "AI", Slug: "ai", InternalName: "ai", DisplayName: "AI"}},
	}
	s := New(Config{}, st)
	_, err := s.callAgentTool(context.Background(), "set_quiz_entry_open",
		agentToolContext{Session: &authSession{TeacherID: "owner", Role: domain.RoleTeacher}, Platform: true, Confirmed: false},
		map[string]any{"course_id": 1, "open": true},
	)
	if err == nil || !strings.Contains(err.Error(), "二次确认") {
		t.Fatalf("unconfirmed platform write err = %v, want confirmation error", err)
	}
}

func TestAgentToolRegistryKeepsStudentToolsStudentScoped(t *testing.T) {
	s := New(Config{}, &memStore{})
	_, err := s.callAgentTool(context.Background(), "get_my_quiz_history", agentToolContext{}, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("student tool without student err = %v, want unauthorized", err)
	}
}

func TestAgentMentionSearchReturnsScopedStudentsAndAssignments(t *testing.T) {
	st := &memStore{
		teachers: []domain.Teacher{{ID: "owner", Name: "Owner", Role: domain.RoleTeacher}},
		courses:  []domain.Course{{ID: 1, TeacherID: "owner", Name: "AI", Slug: "ai", InternalName: "ai", DisplayName: "AI"}},
		attempts: []domain.Attempt{{ID: "a1", CourseID: 1, QuizID: "q1", Name: "张三", StudentNo: "S001", ClassName: "一班", Status: domain.StatusSubmitted, UpdatedAt: time.Now()}},
		homeworkSubmissions: []domain.HomeworkSubmission{{
			ID: "h1", CourseID: 1, Course: "ai", AssignmentID: "hw1", Name: "张三", StudentNo: "S001", ClassName: "一班",
		}},
	}
	s := New(Config{}, st)
	items, err := s.agentMentionCandidates(context.Background(), &authSession{TeacherID: "owner", Role: domain.RoleTeacher}, 1, "张三", 20)
	if err != nil {
		t.Fatalf("agentMentionCandidates returned error: %v", err)
	}
	foundStudent := false
	for _, item := range items {
		if item.Type == "student" && item.Label == "张三" && item.Meta["student_no"] == "S001" {
			foundStudent = true
		}
	}
	if !foundStudent {
		t.Fatalf("mention candidates missing student: %+v", items)
	}
}

func TestTeacherAgentMentionPlanningPassesQuizID(t *testing.T) {
	s := New(Config{}, &memStore{
		teachers: []domain.Teacher{{ID: "owner", Name: "Owner", Role: domain.RoleTeacher}},
		courses:  []domain.Course{{ID: 1, TeacherID: "owner", Name: "AI", Slug: "ai", InternalName: "ai", DisplayName: "AI"}},
	})
	calls, events := s.planTeacherAgentMentionTools(context.Background(), &authSession{TeacherID: "owner", Role: domain.RoleTeacher}, 1, []teacherAgentMention{{Type: "quiz", ID: "week7_l1", Label: "Week 7", CourseID: 1}})
	if len(events) == 0 {
		t.Fatalf("expected mention events")
	}
	found := false
	for _, call := range calls {
		if call.Name == "get_quiz_question_stats" && call.Args["quiz_id"] == "week7_l1" && call.Args["course_id"] == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("planned calls missing quiz stats with quiz_id: %+v", calls)
	}
}

func TestTeacherTaskAgentDetectsExplicitQuizReference(t *testing.T) {
	st := &memStore{
		teachers: []domain.Teacher{{ID: "owner", Name: "Owner", Role: domain.RoleTeacher}},
		courses:  []domain.Course{{ID: 1, TeacherID: "owner", Name: "AI", Slug: "ai", InternalName: "ai", DisplayName: "AI"}},
	}
	s := New(Config{MetadataDir: t.TempDir()}, st)
	dir := s.metadataQuizDir("owner", "ai", "week10_l1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir quiz dir: %v", err)
	}
	yamlText := "quiz_id: week10_l1\ntitle: 上一周实验小测\nquestions:\n  - id: q1\n    type: single_choice\n    stem: 参考题\n"
	if err := os.WriteFile(filepath.Join(dir, "week10_l1.yaml"), []byte(yamlText), 0o644); err != nil {
		t.Fatalf("write quiz yaml: %v", err)
	}
	req := teacherTaskAgentRequest{
		TaskType: "quiz_generate",
		Session:  &authSession{TeacherID: "owner", Role: domain.RoleTeacher},
		CourseID: 1,
		Prompt:   "参考上一周的实验小测题目和 week10_l1，给这周生成一份小测",
	}
	refs := s.detectQuizRefs(context.Background(), req)
	if len(refs) == 0 || refs[0] != "week10_l1" {
		t.Fatalf("detectQuizRefs = %+v, want week10_l1", refs)
	}
	ctxText, events := s.teacherTaskAgentContext(context.Background(), req)
	if !strings.Contains(ctxText, "week10_l1") || !strings.Contains(ctxText, "参考题") {
		t.Fatalf("task context missing referenced quiz yaml: %s", ctxText)
	}
	foundTool := false
	for _, evt := range events {
		if evt.Tool == "read_quiz_bank_yaml" {
			foundTool = true
		}
	}
	if !foundTool {
		t.Fatalf("expected read_quiz_bank_yaml event, got %+v", events)
	}
}

func TestAgentStudentProfileDoesNotMixSameNameDifferentStudentNo(t *testing.T) {
	st := &memStore{
		teachers: []domain.Teacher{{ID: "owner", Name: "Owner", Role: domain.RoleTeacher}},
		courses:  []domain.Course{{ID: 1, TeacherID: "owner", Name: "AI", Slug: "ai", InternalName: "ai", DisplayName: "AI"}},
		attempts: []domain.Attempt{
			{ID: "a1", CourseID: 1, QuizID: "q1", Name: "张三", StudentNo: "S001", ClassName: "一班", AttemptNo: 1, Status: domain.StatusSubmitted, UpdatedAt: time.Now()},
			{ID: "a2", CourseID: 1, QuizID: "q2", Name: "张三", StudentNo: "S002", ClassName: "二班", AttemptNo: 1, Status: domain.StatusSubmitted, UpdatedAt: time.Now()},
		},
	}
	s := New(Config{}, st)
	text, err := s.callAgentTool(context.Background(), "get_student_profile",
		agentToolContext{Session: &authSession{TeacherID: "owner", Role: domain.RoleTeacher}},
		map[string]any{"course_id": 1, "student_no": "S001", "name": "张三", "class_name": "一班"},
	)
	if err != nil {
		t.Fatalf("get_student_profile returned error: %v", err)
	}
	if !strings.Contains(text, "S001") || strings.Contains(text, "S002") || strings.Contains(text, "q2") {
		t.Fatalf("student profile mixed other student: %s", text)
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

func TestJoinRedirectUsesTemporaryRedirect(t *testing.T) {
	s := New(Config{}, &memStore{})

	req := httptest.NewRequest(http.MethodGet, "/join?code=ABC123", nil)
	rr := httptest.NewRecorder()
	s.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("expected temporary redirect 302, got %d", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if loc != "/?code=ABC123" {
		t.Fatalf("unexpected redirect target: %q", loc)
	}
}

func TestAPIMeIncludesCourseID(t *testing.T) {
	st := &memStore{
		attempts: []domain.Attempt{{
			ID:           "attempt-1",
			SessionToken: "student-token",
			QuizID:       "quiz-a",
			CourseID:     7,
			Name:         "张三",
			StudentNo:    "2024001",
			ClassName:    "一班",
			Status:       domain.StatusInProgress,
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}},
	}
	s := New(Config{}, st)
	s.courseQuizzes[7] = &domain.Quiz{QuizID: "quiz-a", Title: "课堂小测", Questions: []domain.Question{}}

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(&http.Cookie{Name: "student_token", Value: "student-token"})
	rr := httptest.NewRecorder()
	s.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Attempt struct {
			CourseID int `json:"course_id"`
		} `json:"attempt"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if resp.Attempt.CourseID != 7 {
		t.Fatalf("course_id = %d, want 7", resp.Attempt.CourseID)
	}
}

func TestAPIStudentQuizRecordsScopesToCourseAndIdentity(t *testing.T) {
	submittedAt := time.Date(2026, 5, 9, 10, 30, 0, 0, time.Local)
	st := &memStore{
		courses: []domain.Course{
			{ID: 1, TeacherID: "T01", Name: "机器人控制技术", Slug: "robotics"},
			{ID: 2, TeacherID: "T01", Name: "最优化方法", Slug: "optimization"},
		},
		attempts: []domain.Attempt{
			{
				ID: "a1", QuizID: "quiz-a", CourseID: 1, Name: "张三", StudentNo: "2024001", ClassName: "一班",
				AttemptNo: 1, Status: domain.StatusSubmitted, CreatedAt: submittedAt.Add(-time.Hour), UpdatedAt: submittedAt, SubmittedAt: &submittedAt,
			},
			{
				ID: "a2", QuizID: "quiz-b", CourseID: 1, Name: "张三", StudentNo: "2024001", ClassName: "一班",
				Status: domain.StatusInProgress, CreatedAt: submittedAt.Add(time.Hour), UpdatedAt: submittedAt.Add(time.Hour),
			},
			{
				ID: "other-course", QuizID: "quiz-a", CourseID: 2, Name: "张三", StudentNo: "2024001", ClassName: "一班",
				AttemptNo: 1, Status: domain.StatusSubmitted, CreatedAt: submittedAt, UpdatedAt: submittedAt, SubmittedAt: &submittedAt,
			},
			{
				ID: "wrong-student", QuizID: "quiz-a", CourseID: 1, Name: "张三", StudentNo: "2024999", ClassName: "一班",
				AttemptNo: 1, Status: domain.StatusSubmitted, CreatedAt: submittedAt, UpdatedAt: submittedAt, SubmittedAt: &submittedAt,
			},
		},
	}
	s := New(Config{}, st)
	s.courseQuizzes[1] = &domain.Quiz{QuizID: "quiz-a", Title: "本次课堂小测", Questions: []domain.Question{}}

	body := strings.NewReader(`{"course_id":1,"name":"张三","student_no":"2024001","class_name":"一班"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/student/quiz-records", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.Routes().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "correct") || strings.Contains(rr.Body.String(), "answer") {
		t.Fatalf("student records response should not expose score/answers: %s", rr.Body.String())
	}
	var resp struct {
		CurrentQuizID      string `json:"current_quiz_id"`
		MatchedRecordCount int    `json:"matched_record_count"`
		CurrentRecord      struct {
			QuizID      string `json:"quiz_id"`
			Status      string `json:"status"`
			SubmittedAt string `json:"submitted_at"`
		} `json:"current_record"`
		Records []struct {
			QuizID    string `json:"quiz_id"`
			CourseID  int    `json:"course_id"`
			StudentNo string `json:"student_no"`
		} `json:"records"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if resp.CurrentQuizID != "quiz-a" {
		t.Fatalf("current_quiz_id = %q", resp.CurrentQuizID)
	}
	if resp.MatchedRecordCount != 2 || len(resp.Records) != 2 {
		t.Fatalf("expected 2 course-scoped matching records, got count=%d records=%d body=%s", resp.MatchedRecordCount, len(resp.Records), rr.Body.String())
	}
	if resp.CurrentRecord.QuizID != "quiz-a" || resp.CurrentRecord.Status != string(domain.StatusSubmitted) || resp.CurrentRecord.SubmittedAt == "" {
		t.Fatalf("unexpected current record: %+v", resp.CurrentRecord)
	}

	missReq := httptest.NewRequest(http.MethodPost, "/api/student/quiz-records", strings.NewReader(`{"course_id":1,"name":"张三","student_no":"2024001","class_name":"二班"}`))
	missReq.Header.Set("Content-Type", "application/json")
	missRR := httptest.NewRecorder()
	s.Routes().ServeHTTP(missRR, missReq)
	if missRR.Code != http.StatusOK {
		t.Fatalf("unexpected mismatch status: %d body=%s", missRR.Code, missRR.Body.String())
	}
	var missResp struct {
		MatchedRecordCount int `json:"matched_record_count"`
	}
	if err := json.Unmarshal(missRR.Body.Bytes(), &missResp); err != nil {
		t.Fatalf("unmarshal mismatch response failed: %v", err)
	}
	if missResp.MatchedRecordCount != 0 {
		t.Fatalf("class mismatch should return no records, got %d", missResp.MatchedRecordCount)
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

func TestHomeworkGradeHiddenUntilPublished(t *testing.T) {
	now := time.Now()
	score := 88.5
	st := &memStore{
		settings: map[string]string{},
		teachers: []domain.Teacher{
			{ID: "t1", Name: "教师一", Role: domain.RoleTeacher},
		},
		courses: []domain.Course{
			{ID: 1, TeacherID: "t1", Slug: "course_a", InternalName: "course_a"},
		},
		homeworkSubmissions: []domain.HomeworkSubmission{
			{
				ID:             "sub-grade-1",
				SessionToken:   "hw-token",
				CourseID:       1,
				Course:         "course_a",
				AssignmentID:   "task_1",
				Name:           "李四",
				StudentNo:      "2023002",
				ClassName:      "计科1班",
				Score:          &score,
				Feedback:       "结构完整，继续加强分析。",
				GradedAt:       &now,
				GradeUpdatedAt: &now,
				CreatedAt:      now,
				UpdatedAt:      now,
			},
		},
	}
	s := New(Config{}, st)
	s.authTokens["teacher-token"] = authSession{
		TeacherID: "t1",
		Role:      domain.RoleTeacher,
		Expiry:    now.Add(time.Hour),
	}

	studentReq := httptest.NewRequest(http.MethodGet, "/api/homework/submission", nil)
	studentReq.AddCookie(&http.Cookie{Name: homeworkCookieName, Value: "hw-token"})
	studentRR := httptest.NewRecorder()
	s.Routes().ServeHTTP(studentRR, studentReq)
	if studentRR.Code != http.StatusOK {
		t.Fatalf("student hidden status: %d body=%s", studentRR.Code, studentRR.Body.String())
	}
	var hiddenResp map[string]map[string]any
	if err := json.Unmarshal(studentRR.Body.Bytes(), &hiddenResp); err != nil {
		t.Fatalf("unmarshal hidden response failed: %v", err)
	}
	if _, ok := hiddenResp["submission"]["score"]; ok {
		t.Fatalf("score leaked before publish: %s", studentRR.Body.String())
	}
	if _, ok := hiddenResp["submission"]["feedback"]; ok {
		t.Fatalf("feedback leaked before publish: %s", studentRR.Body.String())
	}

	publishReq := httptest.NewRequest(http.MethodPost, "/api/teacher/courses/homework/grades/visibility?course_id=1", strings.NewReader(`{"assignment_id":"task_1","published":true}`))
	publishReq.Header.Set("Content-Type", "application/json")
	publishReq.AddCookie(&http.Cookie{Name: "auth_token", Value: "teacher-token"})
	publishRR := httptest.NewRecorder()
	s.Routes().ServeHTTP(publishRR, publishReq)
	if publishRR.Code != http.StatusOK {
		t.Fatalf("publish status: %d body=%s", publishRR.Code, publishRR.Body.String())
	}

	studentReq2 := httptest.NewRequest(http.MethodGet, "/api/homework/submission", nil)
	studentReq2.AddCookie(&http.Cookie{Name: homeworkCookieName, Value: "hw-token"})
	studentRR2 := httptest.NewRecorder()
	s.Routes().ServeHTTP(studentRR2, studentReq2)
	if studentRR2.Code != http.StatusOK {
		t.Fatalf("student published status: %d body=%s", studentRR2.Code, studentRR2.Body.String())
	}
	var shownResp map[string]map[string]any
	if err := json.Unmarshal(studentRR2.Body.Bytes(), &shownResp); err != nil {
		t.Fatalf("unmarshal shown response failed: %v", err)
	}
	if shownResp["submission"]["score"] != score {
		t.Fatalf("expected published score, got %v body=%s", shownResp["submission"]["score"], studentRR2.Body.String())
	}
	if shownResp["submission"]["feedback"] != "结构完整，继续加强分析。" {
		t.Fatalf("expected published feedback, got %v", shownResp["submission"]["feedback"])
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

func TestTeacherCanJoinCourseByInviteCode(t *testing.T) {
	st := &memStore{
		teachers: []domain.Teacher{{ID: "owner", Name: "Owner", Role: domain.RoleTeacher}, {ID: "assistant", Name: "Assistant", Role: domain.RoleTeacher}},
		courses:  []domain.Course{{ID: 1, TeacherID: "owner", Name: "AI", Slug: "ai", InternalName: "ai", DisplayName: "AI", InviteCode: "ABC123", CreatedAt: time.Now(), UpdatedAt: time.Now()}},
	}
	s := New(Config{}, st)
	s.authTokens["assistant-token"] = authSession{TeacherID: "assistant", Role: domain.RoleTeacher, Expiry: time.Now().Add(time.Hour)}

	body := strings.NewReader(`{"invite_code":"abc123","permission":"view"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/teacher/courses/join", body)
	req.AddCookie(&http.Cookie{Name: "auth_token", Value: "assistant-token"})
	rr := httptest.NewRecorder()
	s.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("join course got status %d: %s", rr.Code, rr.Body.String())
	}
	if _, err := st.GetCourseTeacher(context.Background(), 1, "assistant"); err != nil {
		t.Fatalf("assistant membership not stored: %v", err)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/teacher/courses", nil)
	listReq.AddCookie(&http.Cookie{Name: "auth_token", Value: "assistant-token"})
	listRR := httptest.NewRecorder()
	s.Routes().ServeHTTP(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Fatalf("list courses got status %d: %s", listRR.Code, listRR.Body.String())
	}
	var resp struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(listRR.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0]["access"] != "assistant" || resp.Items[0]["permission"] != "view" {
		t.Fatalf("unexpected assistant course payload: %#v", resp.Items)
	}
}

func TestReadOnlyAssistantCannotModifyCourse(t *testing.T) {
	st := &memStore{
		teachers:       []domain.Teacher{{ID: "owner", Name: "Owner", Role: domain.RoleTeacher}, {ID: "assistant", Name: "Assistant", Role: domain.RoleTeacher}},
		courses:        []domain.Course{{ID: 1, TeacherID: "owner", Name: "AI", Slug: "ai", InternalName: "ai", DisplayName: "AI", InviteCode: "ABC123"}},
		courseTeachers: []domain.CourseTeacher{{CourseID: 1, TeacherID: "assistant", Permission: domain.CoursePermissionView}},
	}
	s := New(Config{}, st)
	s.authTokens["assistant-token"] = authSession{TeacherID: "assistant", Role: domain.RoleTeacher, Expiry: time.Now().Add(time.Hour)}

	req := httptest.NewRequest(http.MethodPost, "/api/teacher/courses/entry?course_id=1", strings.NewReader(`{"open":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "auth_token", Value: "assistant-token"})
	rr := httptest.NewRecorder()
	s.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest && rr.Code != http.StatusForbidden {
		t.Fatalf("read-only assistant modify got status %d, want forbidden/bad request: %s", rr.Code, rr.Body.String())
	}
}

func TestTeacherAgentContextIsReadOnlyForViewCollaborator(t *testing.T) {
	st := &memStore{
		teachers:       []domain.Teacher{{ID: "owner", Name: "Owner", Role: domain.RoleTeacher}, {ID: "assistant", Name: "Assistant", Role: domain.RoleTeacher}},
		courses:        []domain.Course{{ID: 1, TeacherID: "owner", Name: "AI", Slug: "ai", InternalName: "ai", DisplayName: "AI", InviteCode: "ABC123"}},
		courseTeachers: []domain.CourseTeacher{{CourseID: 1, TeacherID: "assistant", Permission: domain.CoursePermissionView}},
		attempts:       []domain.Attempt{{ID: "a1", CourseID: 1, QuizID: "quiz-1", Name: "张三", StudentNo: "S001", ClassName: "一班", AttemptNo: 1, Status: domain.StatusSubmitted, UpdatedAt: time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)}},
	}
	s := New(Config{}, st)
	sess := &authSession{TeacherID: "assistant", Role: domain.RoleTeacher, Expiry: time.Now().Add(time.Hour)}
	req := httptest.NewRequest(http.MethodPost, "/api/teacher/agent/chat?course_id=1", nil)
	ctxText, err := s.teacherAgentContext(req, sess, "1")
	if err != nil {
		t.Fatalf("teacherAgentContext returned error: %v", err)
	}
	if !strings.Contains(ctxText, "课程：AI") || !strings.Contains(ctxText, "张三") {
		t.Fatalf("context missing expected read-only course/attempt data: %s", ctxText)
	}
}

func TestAPITeacherAgentChatRejectsEmptyQuestion(t *testing.T) {
	st := &memStore{}
	s := New(Config{}, st)
	s.authTokens["teacher-token"] = authSession{TeacherID: "T01", Role: domain.RoleTeacher, Expiry: time.Now().Add(time.Hour)}
	req := httptest.NewRequest(http.MethodPost, "/api/teacher/agent/chat", strings.NewReader(`{"message":"   "}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "auth_token", Value: "teacher-token"})
	rr := httptest.NewRecorder()
	s.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty agent question got status %d, want 400: %s", rr.Code, rr.Body.String())
	}
}
