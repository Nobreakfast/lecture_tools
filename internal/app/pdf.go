package app

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func (s *Server) pptDir() string {
	return filepath.Join(filepath.Dir(s.cfg.DataDir), "ppt")
}

func (s *Server) pagePDF(w http.ResponseWriter, _ *http.Request) {
	s.servePage(w, "web/pdf.html")
}

func (s *Server) apiPDFs(w http.ResponseWriter, _ *http.Request) {
	type pdfItem struct {
		Folder string `json:"folder"`
		File   string `json:"file"`
		URL    string `json:"url"`
		Size   int64  `json:"size"`
	}
	var items []pdfItem
	root := s.pptDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		writeJSON(w, map[string]any{"items": items})
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		folder := entry.Name()
		files, err := os.ReadDir(filepath.Join(root, folder))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			name := f.Name()
			if !strings.HasSuffix(strings.ToLower(name), ".pdf") {
				continue
			}
			info, _ := f.Info()
			size := int64(0)
			if info != nil {
				size = info.Size()
			}
			items = append(items, pdfItem{
				Folder: folder,
				File:   name,
				URL:    "/ppt/" + folder + "/" + name,
				Size:   size,
			})
		}
	}
	writeJSON(w, map[string]any{"items": items})
}

func (s *Server) servePPT(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/ppt/")
	cleaned := filepath.Clean(rel)
	if cleaned == "." || strings.Contains(cleaned, "..") || filepath.IsAbs(cleaned) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	if !strings.HasSuffix(strings.ToLower(cleaned), ".pdf") {
		http.Error(w, "only PDF allowed", http.StatusBadRequest)
		return
	}
	fp := filepath.Join(s.pptDir(), cleaned)
	if _, err := os.Stat(fp); os.IsNotExist(err) {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	http.ServeFile(w, r, fp)
}

func (s *Server) apiAdminPDFUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 100<<20)
	if err := r.ParseMultipartForm(100 << 20); err != nil {
		http.Error(w, "文件过大或格式错误", http.StatusBadRequest)
		return
	}
	folder := strings.TrimSpace(r.FormValue("folder"))
	if folder == "" || strings.Contains(folder, "..") || strings.Contains(folder, "/") || strings.Contains(folder, "\\") {
		http.Error(w, "文件夹名称无效", http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "未找到上传文件", http.StatusBadRequest)
		return
	}
	defer file.Close()
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".pdf") {
		http.Error(w, "仅支持 PDF 文件", http.StatusBadRequest)
		return
	}
	data, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "读取文件失败", http.StatusInternalServerError)
		return
	}
	dir := filepath.Join(s.pptDir(), folder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		http.Error(w, "创建目录失败", http.StatusInternalServerError)
		return
	}
	fp := filepath.Join(dir, header.Filename)
	if err := os.WriteFile(fp, data, 0o644); err != nil {
		http.Error(w, "写入文件失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "url": "/ppt/" + folder + "/" + header.Filename})
}

func (s *Server) apiAdminPDFDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		Folder string `json:"folder"`
		File   string `json:"file"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if req.Folder == "" || req.File == "" {
		http.Error(w, "参数不完整", http.StatusBadRequest)
		return
	}
	if strings.Contains(req.Folder, "..") || strings.Contains(req.File, "..") ||
		strings.Contains(req.Folder, "/") || strings.Contains(req.File, "/") {
		http.Error(w, "路径无效", http.StatusBadRequest)
		return
	}
	fp := filepath.Join(s.pptDir(), req.Folder, req.File)
	if err := os.Remove(fp); err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "文件不存在", http.StatusNotFound)
			return
		}
		http.Error(w, "删除失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiAdminPDFRename(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireAdmin(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		Folder  string `json:"folder"`
		OldName string `json:"old_name"`
		NewName string `json:"new_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if req.Folder == "" || req.OldName == "" || req.NewName == "" {
		http.Error(w, "参数不完整", http.StatusBadRequest)
		return
	}
	for _, s := range []string{req.Folder, req.OldName, req.NewName} {
		if strings.Contains(s, "..") || strings.Contains(s, "/") || strings.Contains(s, "\\") {
			http.Error(w, "路径无效", http.StatusBadRequest)
			return
		}
	}
	if !strings.HasSuffix(strings.ToLower(req.NewName), ".pdf") {
		req.NewName += ".pdf"
	}
	dir := filepath.Join(s.pptDir(), req.Folder)
	oldPath := filepath.Join(dir, req.OldName)
	newPath := filepath.Join(dir, req.NewName)
	if _, err := os.Stat(oldPath); os.IsNotExist(err) {
		http.Error(w, "原文件不存在", http.StatusNotFound)
		return
	}
	if oldPath == newPath {
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	if _, err := os.Stat(newPath); err == nil {
		http.Error(w, "目标文件名已存在", http.StatusConflict)
		return
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		http.Error(w, "重命名失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func fileExists(path string) (os.FileInfo, error) {
	return os.Stat(path)
}
