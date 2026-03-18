package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"course-assistant/internal/app"
	"course-assistant/internal/store"
)

func main() {
	loaded := loadDotEnvCandidates()
	addr := env("APP_ADDR", "0.0.0.0:8080")
	certPath := env("CERT_PATH", "")
	certFile := ""
	keyFile := ""
	if strings.TrimSpace(certPath) != "" {
		var err error
		certFile, keyFile, err = discoverTLSFiles(certPath)
		if err != nil {
			fmt.Printf("HTTPS证书加载失败，回退HTTP: %v\n", err)
			certFile = ""
			keyFile = ""
		}
	}
	baseURL := env("APP_BASE_URL", "")
	if baseURL == "" {
		baseURL = guessBaseURL(addr, certFile != "" && keyFile != "")
	}
	cfg := app.Config{
		Addr:          addr,
		BaseURL:       baseURL,
		AdminPassword: env("ADMIN_PASSWORD", "admin123"),
		DataDir:       env("DATA_DIR", "./data"),
		QuizAssetsDir: env("QUIZ_ASSETS_DIR", "./quiz/assets"),
		AIEndpoint:    env("AI_ENDPOINT", ""),
		AIKey:         env("AI_API_KEY", ""),
		AIModel:       env("AI_MODEL", ""),
	}
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "assets"), 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(cfg.QuizAssetsDir, 0o755); err != nil {
		log.Fatal(err)
	}
	st, err := store.NewSQLiteStore(filepath.Join(cfg.DataDir, "app.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()
	srv := app.New(cfg, st)
	if err := srv.Init(context.Background()); err != nil {
		log.Fatal(err)
	}
	httpSrv := &http.Server{
		Addr:    cfg.Addr,
		Handler: srv.Routes(),
	}
	srv.SetShutdownFunc(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
	})
	if len(loaded) > 0 {
		fmt.Printf(".env 已加载: %s\n", strings.Join(loaded, ", "))
	} else {
		fmt.Println(".env 未加载: 使用系统环境变量")
	}
	fmt.Printf("AI配置: endpoint=%s model=%s key_loaded=%v\n", mask(cfg.AIEndpoint), cfg.AIModel, cfg.AIKey != "")
	if certFile != "" && keyFile != "" {
		fmt.Printf("HTTPS已启用: cert=%s key=%s\n", certFile, keyFile)
	} else {
		fmt.Println("HTTPS未启用: 使用HTTP")
	}
	fmt.Printf("管理员页面: %s/admin\n", cfg.BaseURL)
	stopSig := make(chan os.Signal, 1)
	signal.Notify(stopSig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stopSig
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
	}()
	if certFile != "" && keyFile != "" {
		err = httpSrv.ListenAndServeTLS(certFile, keyFile)
	} else {
		err = httpSrv.ListenAndServe()
	}
	if err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func env(k, v string) string {
	got := os.Getenv(k)
	if got == "" {
		return v
	}
	return got
}

func guessBaseURL(addr string, https bool) string {
	scheme := "http"
	if https {
		scheme = "https"
	}
	host, port, err := splitHostPort(addr)
	if err != nil {
		return scheme + "://127.0.0.1:8080"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = localIPv4()
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return scheme + "://" + host + ":" + port
}

func discoverTLSFiles(certPath string) (string, string, error) {
	dir := strings.TrimSpace(certPath)
	if dir == "" {
		return "", "", nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", fmt.Errorf("读取CERT_PATH失败: %w", err)
	}
	pemFiles := make([]string, 0)
	keyFiles := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		switch ext {
		case ".pem":
			pemFiles = append(pemFiles, filepath.Join(dir, name))
		case ".key":
			keyFiles = append(keyFiles, filepath.Join(dir, name))
		}
	}
	sort.Strings(pemFiles)
	sort.Strings(keyFiles)
	if len(pemFiles) == 0 {
		return "", "", fmt.Errorf("CERT_PATH目录未找到*.pem证书文件: %s", dir)
	}
	if len(keyFiles) == 0 {
		return "", "", fmt.Errorf("CERT_PATH目录未找到*.key私钥文件: %s", dir)
	}
	return pemFiles[0], keyFiles[0], nil
}

func splitHostPort(addr string) (string, string, error) {
	if !strings.Contains(addr, ":") {
		return "", "", fmt.Errorf("invalid addr")
	}
	if strings.HasPrefix(addr, ":") {
		p := strings.TrimPrefix(addr, ":")
		if _, err := strconv.Atoi(p); err != nil {
			return "", "", err
		}
		return "", p, nil
	}
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "", "", err
	}
	return h, p, nil
}

func localIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipNet.IP.To4()
			if ip == nil || ip.IsLoopback() {
				continue
			}
			return ip.String()
		}
	}
	return ""
}

func loadDotEnv(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kv := strings.SplitN(line, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		if key == "" {
			continue
		}
		if os.Getenv(key) != "" {
			continue
		}
		_ = os.Setenv(key, strings.Trim(val, `"'`))
	}
	return true
}

func loadDotEnvCandidates() []string {
	loaded := []string{}
	seen := map[string]struct{}{}
	candidates := []string{".env"}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), ".env"))
	}
	for _, p := range candidates {
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		if loadDotEnv(abs) {
			loaded = append(loaded, abs)
		}
	}
	return loaded
}

func mask(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(empty)"
	}
	if len(s) <= 12 {
		return "****"
	}
	return s[:8] + "..." + s[len(s)-4:]
}
