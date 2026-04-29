// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package domain

import "time"

type UserRole string

const (
	RoleAdmin   UserRole = "admin"
	RoleTeacher UserRole = "teacher"
)

type Teacher struct {
	ID           string
	Name         string
	PasswordHash string
	Role         UserRole
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Course struct {
	ID           int
	TeacherID    string
	Name         string
	DisplayName  string
	InternalName string
	Slug         string
	InviteCode   string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type CourseState struct {
	CourseID       int
	EntryOpen      bool
	QuizYAML       string
	QuizSourcePath string
}

type AttemptStatus string

const (
	StatusInProgress AttemptStatus = "in_progress"
	StatusSubmitted  AttemptStatus = "submitted"
)

type QuestionType string

const (
	QuestionSingleChoice QuestionType = "single_choice"
	QuestionMultiChoice  QuestionType = "multi_choice"
	QuestionYesNo        QuestionType = "yes_no"
	QuestionSurvey       QuestionType = "survey"
	QuestionShortAnswer  QuestionType = "short_answer"
)

type Option struct {
	Key   string `json:"key" yaml:"key"`
	Text  string `json:"text" yaml:"text"`
	Image string `json:"image,omitempty" yaml:"image,omitempty"`
}

type Question struct {
	ID              string       `json:"id" yaml:"id"`
	Type            QuestionType `json:"type" yaml:"type"`
	Stem            string       `json:"stem" yaml:"stem"`
	Options         []Option     `json:"options" yaml:"options"`
	AllowMultiple   bool         `json:"allow_multiple,omitempty" yaml:"allow_multiple,omitempty"`
	ShortAnswerMode string       `json:"short_answer_mode,omitempty" yaml:"short_answer_mode,omitempty"`
	CorrectAnswer   string       `json:"correct_answer,omitempty" yaml:"correct_answer,omitempty"`
	ReferenceAnswer string       `json:"reference_answer,omitempty" yaml:"reference_answer,omitempty"`
	Explanation     string       `json:"explanation,omitempty" yaml:"explanation,omitempty"`
	KnowledgeTag    string       `json:"knowledge_tag,omitempty" yaml:"knowledge_tag,omitempty"`
	PoolTag         string       `json:"pool_tag,omitempty" yaml:"pool_tag,omitempty"`
	Image           string       `json:"image,omitempty" yaml:"image,omitempty"`
	FixedPosition   bool         `json:"fixed_position,omitempty" yaml:"fixed_position,omitempty"`
	ShuffleOptions  *bool        `json:"shuffle_options,omitempty" yaml:"shuffle_options,omitempty"`
}

type SamplingGroup struct {
	Tag  string `json:"tag" yaml:"tag"`
	Pick int    `json:"pick" yaml:"pick"`
}

type Sampling struct {
	Groups []SamplingGroup `json:"groups" yaml:"groups"`
}

type Quiz struct {
	QuizID    string     `json:"quiz_id" yaml:"quiz_id"`
	Title     string     `json:"title" yaml:"title"`
	Sampling  *Sampling  `json:"sampling,omitempty" yaml:"sampling,omitempty"`
	Questions []Question `json:"questions" yaml:"questions"`
}

type Attempt struct {
	ID           string
	SessionToken string
	QuizID       string
	CourseID     int
	Name         string
	StudentNo    string
	ClassName    string
	AttemptNo    int
	Status       AttemptStatus
	CreatedAt    time.Time
	UpdatedAt    time.Time
	SubmittedAt  *time.Time
}

type Answer struct {
	AttemptID  string
	QuestionID string
	Value      string
	UpdatedAt  time.Time
}

type ShortAnswerValue struct {
	V      int      `json:"v,omitempty"`
	Text   string   `json:"text,omitempty"`
	Images []string `json:"images,omitempty"`
}

type ResultSummary struct {
	Strengths     []string `json:"strengths"`
	Weaknesses    []string `json:"weaknesses"`
	NextActions   []string `json:"next_actions"`
	Priority      string   `json:"priority_level"`
	Encouragement string   `json:"encouragement"`
}

type QuizShare struct {
	ID         int
	CourseID   int
	QuizID     string
	ShareToken string
	CreatedAt  time.Time
	RevokedAt  *time.Time
}

type HomeworkFileSlot string

const (
	HomeworkSlotReport HomeworkFileSlot = "report"
	HomeworkSlotCode   HomeworkFileSlot = "code"
	HomeworkSlotExtra  HomeworkFileSlot = "extra"
	HomeworkSlotOthers HomeworkFileSlot = "others"
)

type HomeworkSubmission struct {
	ID                 string
	SessionToken       string
	Course             string // slug (legacy)
	CourseID           int    // preferred foreign key
	AssignmentID       string
	Name               string
	StudentNo          string
	ClassName          string
	SecretKey          string
	ReportOriginalName string
	ReportUploadedAt   *time.Time
	CodeOriginalName   string
	CodeUploadedAt     *time.Time
	ExtraOriginalName  string
	ExtraUploadedAt    *time.Time
	Score              *float64
	Feedback           string
	GradedAt           *time.Time
	GradeUpdatedAt     *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type HomeworkQA struct {
	ID             string
	Course         string
	CourseID       int
	AssignmentID   string
	Question       string
	QuestionImages []string
	Answer         string
	AnswerImages   []string
	Pinned         bool
	Hidden         bool
	CreatedAt      time.Time
	AnsweredAt     *time.Time
	UpdatedAt      time.Time
}

func (h HomeworkSubmission) HasUploadedFiles() bool {
	return h.ReportOriginalName != "" || h.CodeOriginalName != "" || h.ExtraOriginalName != ""
}

// QAIssue represents a discussion thread (like a GitHub issue).
type QAIssue struct {
	ID           int
	CourseID     int
	Course       string // slug (legacy)
	AssignmentID string
	StudentNo    string
	Title        string
	Status       string // "open" / "resolved"
	Pinned       bool
	Hidden       bool
	MessageCount int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// QAMessage represents a single message within a QAIssue thread.
type QAMessage struct {
	ID        int
	IssueID   int
	Sender    string // "student" / "teacher"
	Content   string
	Images    []string
	CreatedAt time.Time
}

func (h HomeworkSubmission) HasSlot(slot HomeworkFileSlot) bool {
	switch slot {
	case HomeworkSlotReport:
		return h.ReportOriginalName != ""
	case HomeworkSlotCode:
		return h.CodeOriginalName != ""
	case HomeworkSlotExtra:
		return h.ExtraOriginalName != ""
	default:
		return false
	}
}
