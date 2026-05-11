package app

import (
	"net/url"
	"strings"
	"testing"
)

func TestStudentQAIssueLinkCarriesReturnTarget(t *testing.T) {
	s := &Server{}

	link := s.studentQAIssueLink(12, "task-1", 34)
	if !strings.HasPrefix(link, "/student/qa?") {
		t.Fatalf("unexpected link prefix: %s", link)
	}
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse link: %v", err)
	}
	q := parsed.Query()
	if q.Get("course_id") != "12" || q.Get("assignment_id") != "task-1" || q.Get("focus") != "34" {
		t.Fatalf("unexpected qa link params: %s", link)
	}
	if q.Get("return_to") != "/?tab=tab-homework&assignment_id=task-1" {
		t.Fatalf("unexpected return_to: %q", q.Get("return_to"))
	}
}
