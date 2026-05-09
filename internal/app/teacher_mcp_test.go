package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"course-assistant/internal/domain"
)

func TestTeacherMCPQAIssuesPrioritizesOpenPinnedIssues(t *testing.T) {
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	st := &memStore{
		courses: []domain.Course{{ID: 7, TeacherID: "t1", DisplayName: "软件工程", Slug: "se"}},
		qaIssues: []domain.QAIssue{
			{ID: 1, CourseID: 7, AssignmentID: "hw1", StudentNo: "S1", Title: "普通问题", Status: "open", MessageCount: 1, UpdatedAt: now.Add(-time.Hour)},
			{ID: 2, CourseID: 7, AssignmentID: "hw1", StudentNo: "S2", Title: "置顶问题", Status: "open", Pinned: true, MessageCount: 2, UpdatedAt: now.Add(-2 * time.Hour)},
			{ID: 3, CourseID: 7, AssignmentID: "hw1", StudentNo: "S3", Title: "已解决问题", Status: "resolved", MessageCount: 3, UpdatedAt: now},
		},
		qaMessages: []domain.QAMessage{
			{IssueID: 2, Sender: "student", Content: "这里不理解", CreatedAt: now.Add(-2 * time.Hour)},
			{IssueID: 2, Sender: "teacher", Content: "先看例题", CreatedAt: now.Add(-time.Hour)},
		},
	}
	srv := &Server{store: st}
	text, err := srv.teacherMCPQAIssues(context.Background(), &authSession{TeacherID: "t1"}, 7, "", "open", false, 10, 2)
	if err != nil {
		t.Fatalf("teacherMCPQAIssues returned error: %v", err)
	}
	for _, want := range []string{"建议优先处理：#2", "置顶问题", "普通问题", "student", "先看例题"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, text)
		}
	}
	if strings.Contains(text, "已解决问题") {
		t.Fatalf("default open filter should omit resolved issues, got:\n%s", text)
	}
}

func TestTeacherMCPReplyQAIssueSavesTeacherReplyAndResolves(t *testing.T) {
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	st := &memStore{
		courses:  []domain.Course{{ID: 7, TeacherID: "t1", DisplayName: "软件工程", Slug: "se"}},
		qaIssues: []domain.QAIssue{{ID: 9, CourseID: 7, AssignmentID: "hw1", StudentNo: "S1", Title: "怎么提交", Status: "open", MessageCount: 1, UpdatedAt: now}},
	}
	srv := &Server{store: st}
	text, err := srv.teacherMCPReplyQAIssue(context.Background(), &authSession{TeacherID: "t1"}, 9, "请先提交报告，再补交代码。", true)
	if err != nil {
		t.Fatalf("teacherMCPReplyQAIssue returned error: %v", err)
	}
	if !strings.Contains(text, "已保存教师回复，并标记为已解决") {
		t.Fatalf("unexpected result: %s", text)
	}
	if got := st.qaIssues[0].Status; got != "resolved" {
		t.Fatalf("status = %q, want resolved", got)
	}
	if got := st.qaIssues[0].MessageCount; got != 2 {
		t.Fatalf("message count = %d, want 2", got)
	}
	if len(st.qaMessages) != 1 || st.qaMessages[0].Sender != "teacher" || st.qaMessages[0].Content != "请先提交报告，再补交代码。" {
		t.Fatalf("teacher message not saved correctly: %#v", st.qaMessages)
	}
}
