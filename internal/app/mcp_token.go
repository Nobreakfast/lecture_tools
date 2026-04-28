// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const teacherMCPTokenSettingPrefix = "teacher_mcp_token:"

type teacherMCPTokenState struct {
	Token     string `json:"token"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

func teacherMCPTokenSettingKey(teacherID string) string {
	return teacherMCPTokenSettingPrefix + teacherID
}

func newTeacherMCPToken(teacherID string) string {
	return "mcp-" + hex.EncodeToString([]byte(teacherID)) + "-" + newID() + newID()
}

func teacherIDFromMCPToken(token string) (string, bool) {
	rest, ok := strings.CutPrefix(token, "mcp-")
	if !ok {
		return "", false
	}
	teacherHex, _, ok := strings.Cut(rest, "-")
	if !ok || teacherHex == "" {
		return "", false
	}
	raw, err := hex.DecodeString(teacherHex)
	if err != nil || len(raw) == 0 {
		return "", false
	}
	return string(raw), true
}

func (s *Server) loadTeacherMCPTokenState(ctx context.Context, teacherID string) (teacherMCPTokenState, error) {
	raw, err := s.store.GetSetting(ctx, teacherMCPTokenSettingKey(teacherID))
	if err != nil || strings.TrimSpace(raw) == "" {
		return teacherMCPTokenState{}, nil
	}
	var state teacherMCPTokenState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return teacherMCPTokenState{}, err
	}
	return state, nil
}

func (s *Server) saveTeacherMCPTokenState(ctx context.Context, teacherID string, state teacherMCPTokenState) error {
	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return s.store.SetSetting(ctx, teacherMCPTokenSettingKey(teacherID), string(payload))
}

func (s *Server) getPersistentMCPSessionByToken(ctx context.Context, token string) *authSession {
	teacherID, ok := teacherIDFromMCPToken(token)
	if !ok {
		return nil
	}
	state, err := s.loadTeacherMCPTokenState(ctx, teacherID)
	if err != nil || !state.Enabled || state.Token == "" {
		return nil
	}
	if subtle.ConstantTimeCompare([]byte(state.Token), []byte(token)) != 1 {
		return nil
	}
	teacher, err := s.store.GetTeacher(ctx, teacherID)
	if err != nil {
		return nil
	}
	return &authSession{TeacherID: teacher.ID, Role: teacher.Role}
}

func (s *Server) writeTeacherMCPState(w http.ResponseWriter, r *http.Request, sess *authSession) {
	state, err := s.loadTeacherMCPTokenState(r.Context(), sess.TeacherID)
	if err != nil {
		http.Error(w, "读取 MCP 配置失败", http.StatusInternalServerError)
		return
	}
	resp := map[string]any{
		"enabled":    state.Enabled,
		"has_token":  state.Token != "",
		"created_at": state.CreatedAt,
		"updated_at": state.UpdatedAt,
	}
	if state.Enabled && state.Token != "" {
		resp["token"] = state.Token
	}
	writeJSON(w, resp)
}

func (s *Server) apiTeacherMCP(w http.ResponseWriter, r *http.Request) {
	sess := s.requireTeacherOrAdmin(r)
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.writeTeacherMCPState(w, r, sess)
		return
	case http.MethodPost:
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	now := time.Now().Format(time.RFC3339Nano)
	s.settingMu.Lock()
	state, err := s.loadTeacherMCPTokenState(r.Context(), sess.TeacherID)
	if err == nil {
		if state.Token == "" && req.Enabled {
			state.Token = newTeacherMCPToken(sess.TeacherID)
			state.CreatedAt = now
		}
		state.Enabled = req.Enabled
		if state.CreatedAt == "" && state.Token != "" {
			state.CreatedAt = now
		}
		state.UpdatedAt = now
		err = s.saveTeacherMCPTokenState(r.Context(), sess.TeacherID, state)
	}
	s.settingMu.Unlock()
	if err != nil {
		http.Error(w, "保存 MCP 配置失败", http.StatusInternalServerError)
		return
	}
	s.writeTeacherMCPState(w, r, sess)
}
