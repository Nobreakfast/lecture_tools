package domain

import "time"

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
	ID            string       `json:"id" yaml:"id"`
	Type          QuestionType `json:"type" yaml:"type"`
	Stem          string       `json:"stem" yaml:"stem"`
	Options       []Option     `json:"options" yaml:"options"`
	AllowMultiple bool         `json:"allow_multiple,omitempty" yaml:"allow_multiple,omitempty"`
	CorrectAnswer string       `json:"correct_answer,omitempty" yaml:"correct_answer,omitempty"`
	ReferenceAnswer string     `json:"reference_answer,omitempty" yaml:"reference_answer,omitempty"`
	Explanation   string       `json:"explanation,omitempty" yaml:"explanation,omitempty"`
	KnowledgeTag  string       `json:"knowledge_tag,omitempty" yaml:"knowledge_tag,omitempty"`
	PoolTag       string       `json:"pool_tag,omitempty" yaml:"pool_tag,omitempty"`
	Image         string       `json:"image,omitempty" yaml:"image,omitempty"`
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
	ID          string
	SessionToken string
	QuizID      string
	Name        string
	StudentNo   string
	ClassName   string
	AttemptNo   int
	Status      AttemptStatus
	CreatedAt   time.Time
	UpdatedAt   time.Time
	SubmittedAt *time.Time
}

type Answer struct {
	AttemptID  string
	QuestionID string
	Value      string
	UpdatedAt  time.Time
}

type ResultSummary struct {
	Strengths    []string `json:"strengths"`
	Weaknesses   []string `json:"weaknesses"`
	NextActions  []string `json:"next_actions"`
	Priority     string   `json:"priority_level"`
	Encouragement string  `json:"encouragement"`
}
