// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package app

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"course-assistant/internal/domain"

	"golang.org/x/crypto/bcrypt"
)

// ── Auth middleware ──

func (s *Server) requireStudent(r *http.Request) (*domain.Attempt, error) {
	cookie, err := r.Cookie("student_token")
	if err != nil {
		return nil, err
	}
	a, err := s.store.GetAttemptByToken(r.Context(), cookie.Value)
	if err != nil {
		return nil, err
	}
	return a, nil
}

func (s *Server) requireAdmin(r *http.Request) bool {
	sess := s.getAuthSession(r)
	return sess != nil && sess.Role == domain.RoleAdmin
}

func (s *Server) requireTeacherOrAdmin(r *http.Request) *authSession {
	sess := s.getAuthSession(r)
	if sess == nil {
		return nil
	}
	if sess.Role != domain.RoleTeacher && sess.Role != domain.RoleAdmin {
		return nil
	}
	return sess
}

// getAuthSession reads the auth_token cookie and returns the matching session.
// Uses RLock since the common case is read-only.
func (s *Server) getAuthSession(r *http.Request) *authSession {
	cookie, err := r.Cookie("auth_token")
	if err != nil || cookie.Value == "" {
		return nil
	}
	return s.getAuthSessionByToken(cookie.Value)
}

// getAuthSessionByToken looks up a session by its raw token string.
func (s *Server) getAuthSessionByToken(token string) *authSession {
	if token == "" {
		return nil
	}
	s.authMu.RLock()
	sess, ok := s.authTokens[token]
	tokenCount := len(s.authTokens)
	s.authMu.RUnlock()
	if !ok {
		log.Printf("mcp auth: token not found (len=%d, token_prefix=%s)", tokenCount, token[:min(8, len(token))])
		return nil
	}
	if time.Now().After(sess.Expiry) {
		log.Printf("mcp auth: token expired (expiry=%s)", sess.Expiry.Format(time.RFC3339))
		s.authMu.Lock()
		delete(s.authTokens, token)
		s.authMu.Unlock()
		return nil
	}
	return &sess
}

func hashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

func checkPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// ── Unified auth API ──

func (s *Server) apiAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID       string `json:"id"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	req.ID = strings.TrimSpace(req.ID)
	if req.ID == "" || req.Password == "" {
		http.Error(w, "工号和密码不能为空", http.StatusBadRequest)
		return
	}
	teacher, err := s.store.GetTeacher(r.Context(), req.ID)
	if err != nil {
		http.Error(w, "工号或密码错误", http.StatusUnauthorized)
		return
	}
	if !checkPassword(teacher.PasswordHash, req.Password) {
		http.Error(w, "工号或密码错误", http.StatusUnauthorized)
		return
	}
	token := newID() + newID()
	sess := authSession{
		TeacherID: teacher.ID,
		Role:      teacher.Role,
		Expiry:    time.Now().Add(24 * time.Hour),
	}
	s.authMu.Lock()
	s.authTokens[token] = sess
	s.authMu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     "auth_token",
		Value:    token,
		Path:     s.cookiePath(),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, map[string]any{"ok": true, "role": teacher.Role, "name": teacher.Name, "id": teacher.ID})
}

func (s *Server) apiAuthLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if cookie, err := r.Cookie("auth_token"); err == nil {
		s.authMu.Lock()
		delete(s.authTokens, cookie.Value)
		s.authMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "auth_token",
		Value:    "",
		Path:     s.cookiePath(),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	// Also clear the legacy admin_token cookie on old clients.
	http.SetCookie(w, &http.Cookie{
		Name:     "admin_token",
		Value:    "",
		Path:     s.cookiePath(),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiAuthMe(w http.ResponseWriter, r *http.Request) {
	sess := s.getAuthSession(r)
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	teacher, err := s.store.GetTeacher(r.Context(), sess.TeacherID)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	writeJSON(w, map[string]any{"id": teacher.ID, "name": teacher.Name, "role": teacher.Role})
}

func (s *Server) apiAuthChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sess := s.getAuthSession(r)
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if len(req.NewPassword) < 6 {
		http.Error(w, "新密码至少6个字符", http.StatusBadRequest)
		return
	}
	teacher, err := s.store.GetTeacher(r.Context(), sess.TeacherID)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !checkPassword(teacher.PasswordHash, req.OldPassword) {
		http.Error(w, "原密码错误", http.StatusForbidden)
		return
	}
	hash, err := hashPassword(req.NewPassword)
	if err != nil {
		http.Error(w, "加密失败", http.StatusInternalServerError)
		return
	}
	if err := s.store.UpdateTeacherPassword(r.Context(), sess.TeacherID, hash); err != nil {
		http.Error(w, "修改失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
