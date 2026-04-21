package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
)

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
}

type toolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type callResult struct {
	Content []textContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

var tools = []toolDef{
	{
		Name:        "list_courses",
		Description: "列出教师的所有课程",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	},
	{
		Name:        "get_quiz_attempts",
		Description: "获取某课程的答题记录，包含每位学生的成绩",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"course_id":{"type":"string","description":"课程ID"}},"required":["course_id"]}`),
	},
	{
		Name:        "get_homework_submissions",
		Description: "获取某课程的作业提交情况",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"course_id":{"type":"string","description":"课程ID"},"assignment_id":{"type":"string","description":"作业编号"}},"required":["course_id"]}`),
	},
	{
		Name:        "get_summary_stats",
		Description: "获取某课程的答题统计数据",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"course_id":{"type":"string","description":"课程ID"}},"required":["course_id"]}`),
	},
}

func main() {
	log.SetOutput(os.Stderr)

	serverURL := os.Getenv("SERVER_URL")
	teacherID := os.Getenv("TEACHER_ID")
	teacherPwd := os.Getenv("TEACHER_PWD")

	if serverURL == "" || teacherID == "" || teacherPwd == "" {
		log.Fatal("SERVER_URL, TEACHER_ID, and TEACHER_PWD must be set")
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		log.Fatalf("failed to create cookie jar: %v", err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	if err := login(client, serverURL, teacherID, teacherPwd); err != nil {
		log.Fatalf("login failed: %v", err)
	}
	log.Println("logged in successfully")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			log.Printf("failed to parse request: %v", err)
			continue
		}

		// Notifications have no id field (or null id) — no response needed.
		if len(req.ID) == 0 || string(req.ID) == "null" {
			log.Printf("notification: %s", req.Method)
			continue
		}

		var result json.RawMessage
		switch req.Method {
		case "initialize":
			result = json.RawMessage(`{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"course-assistant-mcp","version":"1.0.0"}}`)
		case "ping":
			result = json.RawMessage(`{}`)
		case "tools/list":
			r, _ := json.Marshal(struct {
				Tools []toolDef `json:"tools"`
			}{Tools: tools})
			result = r
		case "tools/call":
			result = handleToolCall(client, serverURL, req.Params)
		default:
			log.Printf("unknown method: %s", req.Method)
			continue
		}

		resp := jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  result,
		}
		out, _ := json.Marshal(resp)
		fmt.Fprintf(os.Stdout, "%s\n", out)
	}

	if err := scanner.Err(); err != nil {
		log.Printf("scanner error: %v", err)
	}
}

func login(client *http.Client, serverURL, id, password string) error {
	body, _ := json.Marshal(map[string]string{"id": id, "password": password})
	resp, err := client.Post(serverURL+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	log.Printf("login response: status=%d cookies=%d", resp.StatusCode, len(resp.Cookies()))
	for _, c := range resp.Cookies() {
		log.Printf("  cookie: %s=%s (path=%s secure=%v)", c.Name, c.Value[:min(8, len(c.Value))]+"...", c.Path, c.Secure)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(b))
	}
	// Verify session by calling /api/auth/me
	meResp, err := client.Get(serverURL + "/api/auth/me")
	if err != nil {
		return fmt.Errorf("session verify failed: %w", err)
	}
	defer meResp.Body.Close()
	meBody, _ := io.ReadAll(meResp.Body)
	if meResp.StatusCode != http.StatusOK {
		return fmt.Errorf("session invalid (status %d): %s", meResp.StatusCode, string(meBody))
	}
	log.Printf("session verified: %s", string(meBody))
	return nil
}

func handleToolCall(client *http.Client, serverURL string, params json.RawMessage) json.RawMessage {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return toolError("invalid params: " + err.Error())
	}

	var args map[string]string
	if len(p.Arguments) > 0 {
		_ = json.Unmarshal(p.Arguments, &args)
	}

	var apiURL string
	switch p.Name {
	case "list_courses":
		apiURL = serverURL + "/api/teacher/courses"
	case "get_quiz_attempts":
		courseID := args["course_id"]
		if courseID == "" {
			return toolError("missing required parameter: course_id")
		}
		apiURL = serverURL + "/api/teacher/courses/attempts?course_id=" + url.QueryEscape(courseID)
	case "get_homework_submissions":
		courseID := args["course_id"]
		if courseID == "" {
			return toolError("missing required parameter: course_id")
		}
		apiURL = serverURL + "/api/teacher/courses/homework/submissions?course_id=" + url.QueryEscape(courseID)
		if aid := args["assignment_id"]; aid != "" {
			apiURL += "&assignment_id=" + url.QueryEscape(aid)
		}
	case "get_summary_stats":
		courseID := args["course_id"]
		if courseID == "" {
			return toolError("missing required parameter: course_id")
		}
		apiURL = serverURL + "/api/teacher/courses/summary?course_id=" + url.QueryEscape(courseID)
	default:
		return toolError("unknown tool: " + p.Name)
	}

	resp, err := client.Get(apiURL)
	if err != nil {
		return toolError("API request failed: " + err.Error())
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return toolError("failed to read response: " + err.Error())
	}

	if resp.StatusCode != http.StatusOK {
		return toolError(fmt.Sprintf("API returned status %d: %s", resp.StatusCode, string(body)))
	}

	return toolSuccess(string(body))
}

func toolSuccess(text string) json.RawMessage {
	r, _ := json.Marshal(callResult{
		Content: []textContent{{Type: "text", Text: text}},
	})
	return r
}

func toolError(msg string) json.RawMessage {
	r, _ := json.Marshal(callResult{
		Content: []textContent{{Type: "text", Text: msg}},
		IsError: true,
	})
	return r
}
