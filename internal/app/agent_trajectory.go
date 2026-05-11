// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"course-assistant/internal/domain"
)

type agentTrajectory struct {
	Kind        string         `json:"kind"`
	CreatedAt   time.Time      `json:"created_at"`
	Teacher     string         `json:"teacher"`
	Course      string         `json:"course"`
	Name        string         `json:"name"`
	Request     map[string]any `json:"request,omitempty"`
	Prompt      string         `json:"prompt,omitempty"`
	RawResponse string         `json:"raw_response,omitempty"`
	Decision    any            `json:"decision,omitempty"`
	Answer      string         `json:"answer,omitempty"`
	Events      any            `json:"events,omitempty"`
	Error       string         `json:"error,omitempty"`
}

func (s *Server) saveAgentTrajectory(ctx context.Context, tr agentTrajectory) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return
	}
	now := tr.CreatedAt
	if now.IsZero() {
		now = time.Now()
		tr.CreatedAt = now
	}
	root := filepath.Join(home, ".lecture_tools",
		safePathPart(fallbackLabel(tr.Teacher, "unknown_teacher")),
		safePathPart(fallbackLabel(tr.Course, "unknown_course")),
		safePathPart(fallbackLabel(tr.Name, "unknown_name")),
		now.Format("2006_01"),
		now.Format("02_15_04"))
	dir := uniqueTrajectoryDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(tr, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "trajectory.json"), append(data, '\n'), 0o600)
}

func uniqueTrajectoryDir(root string) string {
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return root
	}
	for i := 2; i < 1000; i++ {
		candidate := root + "_" + strconv.Itoa(i)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
	return root + "_" + strconv.FormatInt(time.Now().UnixNano(), 10)
}

func fallbackLabel(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func (s *Server) teacherTrajectoryLabels(ctx context.Context, sess *authSession, courseID int) (string, string, string) {
	teacherID := "unknown_teacher"
	teacherName := "unknown_teacher"
	if sess != nil && strings.TrimSpace(sess.TeacherID) != "" {
		teacherID = strings.TrimSpace(sess.TeacherID)
		teacherName = teacherID
		if t, err := s.store.GetTeacher(ctx, sess.TeacherID); err == nil && t != nil {
			if strings.TrimSpace(t.Name) != "" {
				teacherName = strings.TrimSpace(t.Name)
			}
		}
	}
	courseName := "all_courses"
	if courseID > 0 {
		if c, err := s.store.GetCourse(ctx, courseID); err == nil && c != nil {
			courseName = courseTrajectoryName(c)
		}
	}
	return teacherID, courseName, teacherName
}

func (s *Server) studentTrajectoryLabels(ctx context.Context, submission *domain.HomeworkSubmission) (string, string, string) {
	if submission == nil {
		return "unknown_teacher", "unknown_course", "unknown_student"
	}
	teacherID := "unknown_teacher"
	courseName := fallbackLabel(submission.Course, "unknown_course")
	if c, err := s.store.GetCourse(ctx, submission.CourseID); err == nil && c != nil {
		teacherID = fallbackLabel(c.TeacherID, teacherID)
		courseName = courseTrajectoryName(c)
	}
	name := strings.TrimSpace(submission.Name)
	if strings.TrimSpace(submission.StudentNo) != "" {
		if name == "" {
			name = submission.StudentNo
		} else {
			name += "_" + submission.StudentNo
		}
	}
	return teacherID, courseName, fallbackLabel(name, "unknown_student")
}

func courseTrajectoryName(c *domain.Course) string {
	if c == nil {
		return "unknown_course"
	}
	if strings.TrimSpace(c.DisplayName) != "" {
		return strings.TrimSpace(c.DisplayName)
	}
	if strings.TrimSpace(c.Name) != "" {
		return strings.TrimSpace(c.Name)
	}
	if strings.TrimSpace(c.Slug) != "" {
		return strings.TrimSpace(c.Slug)
	}
	return "unknown_course"
}
