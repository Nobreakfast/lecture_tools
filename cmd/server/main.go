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
	baseURL := env("APP_BASE_URL", "")
	if baseURL == "" {
		baseURL = guessBaseURL(addr)
	}
	cfg := app.Config{
		Addr:          addr,
		BaseURL:       baseURL,
		AdminPassword: env("ADMIN_PASSWORD", "admin123"),
		DataDir:       env("DATA_DIR", "./data"),
		AIEndpoint:    env("AI_ENDPOINT", ""),
		AIKey:         env("AI_API_KEY", ""),
		AIModel:       env("AI_MODEL", ""),
	}
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "assets"), 0o755); err != nil {
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
	if err := srv.PrintQRCode(); err != nil {
		log.Printf("二维码生成失败: %v", err)
	}
	if len(loaded) > 0 {
		fmt.Printf(".env 已加载: %s\n", strings.Join(loaded, ", "))
	} else {
		fmt.Println(".env 未加载: 使用系统环境变量")
	}
	fmt.Printf("AI配置: endpoint=%s model=%s key_loaded=%v\n", mask(cfg.AIEndpoint), cfg.AIModel, cfg.AIKey != "")
	fmt.Printf("管理员页面: %s/admin\n", cfg.BaseURL)
	stopSig := make(chan os.Signal, 1)
	signal.Notify(stopSig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stopSig
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
	}()
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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

func guessBaseURL(addr string) string {
	host, port, err := splitHostPort(addr)
	if err != nil {
		return "http://127.0.0.1:8080"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = localIPv4()
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return "http://" + host + ":" + port
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
