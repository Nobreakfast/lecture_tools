package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
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
	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

func main() {
	loaded := loadDotEnvCandidates()
	requestedAutocert := parseBool(env("AUTOCERT_ENABLE", ""))
	autocertEnabled := requestedAutocert
	_, hasCustomAddr := os.LookupEnv("APP_ADDR")
	_, hasCustomBaseURL := os.LookupEnv("APP_BASE_URL")
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
	manualHTTPSEnabled := certFile != "" && keyFile != ""
	httpsEnabled := manualHTTPSEnabled || requestedAutocert
	defaultAddr := "0.0.0.0:8080"
	if httpsEnabled {
		defaultAddr = "0.0.0.0:443"
	}
	addr := env("APP_ADDR", defaultAddr)
	baseURL := env("APP_BASE_URL", "")
	if baseURL == "" {
		baseURL = guessBaseURL(addr, httpsEnabled)
	}
	if httpsEnabled {
		baseURL = normalizeHTTPSBaseURL(baseURL, addr)
	}
	redirectAddr := env("APP_HTTP_REDIRECT_ADDR", "")
	cfg := app.Config{
		Addr:          addr,
		BaseURL:       baseURL,
		DataDir:       env("DATA_DIR", "./data"),
		MetadataDir:   env("METADATA_DIR", "./metadata"),
		SnapshotDir:   env("SNAPSHOT_DIR", "./snapshots"),
		AIEndpoint:    env("AI_ENDPOINT", ""),
		AIKey:         env("AI_API_KEY", ""),
		AIModel:       env("AI_MODEL", ""),
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(cfg.MetadataDir, 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(cfg.SnapshotDir, 0o755); err != nil {
		log.Fatal(err)
	}
	if err := app.ApplyPendingSnapshotRestore(cfg); err != nil {
		log.Fatalf("应用待恢复快照失败: %v", err)
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
	var autocertManager *autocert.Manager
	autocertHosts := []string{}
	if autocertEnabled {
		autocertHosts = filterAutocertHosts(resolveAutocertHosts(env("AUTOCERT_HOSTS", ""), cfg.BaseURL))
		if len(autocertHosts) == 0 {
			fmt.Println("AUTOCERT未启用: 未检测到可签发证书的公网域名，已回退默认策略")
			autocertEnabled = false
		}
		if autocertEnabled {
			cacheDir := strings.TrimSpace(env("AUTOCERT_CACHE_DIR", filepath.Join(cfg.DataDir, "autocert")))
			if cacheDir == "" {
				log.Fatal("AUTOCERT_CACHE_DIR 不能为空")
			}
			if err := os.MkdirAll(cacheDir, 0o755); err != nil {
				log.Fatalf("AUTOCERT_CACHE_DIR创建失败: %v", err)
			}
			autocertManager = &autocert.Manager{
				Prompt:     autocert.AcceptTOS,
				Cache:      autocert.DirCache(cacheDir),
				Email:      strings.TrimSpace(env("AUTOCERT_EMAIL", "")),
				HostPolicy: autocert.HostWhitelist(autocertHosts...),
			}
			httpSrv.TLSConfig = &tls.Config{
				MinVersion:     tls.VersionTLS12,
				GetCertificate: autocertManager.GetCertificate,
				NextProtos:     []string{"h2", "http/1.1", acme.ALPNProto},
			}
		}
	}
	httpsEnabled = manualHTTPSEnabled || autocertEnabled
	if !httpsEnabled {
		_, addrPort, _ := splitHostPort(cfg.Addr)
		if !hasCustomAddr || addrPort == "443" {
			cfg.Addr = "0.0.0.0:8080"
			httpSrv.Addr = cfg.Addr
		}
		if !hasCustomBaseURL || addrPort == "443" {
			cfg.BaseURL = guessBaseURL(cfg.Addr, false)
		}
	}
	if httpsEnabled && strings.TrimSpace(redirectAddr) == "" {
		if autocertEnabled {
			redirectAddr = ":80"
		} else {
			redirectAddr = ":8080"
		}
	}
	var redirectSrv *http.Server
	if httpsEnabled && strings.TrimSpace(redirectAddr) != "" {
		redirectHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			target := buildRedirectTarget(cfg.BaseURL, r)
			http.Redirect(w, r, target, http.StatusMovedPermanently)
		})
		handler := http.Handler(redirectHandler)
		if autocertManager != nil {
			handler = autocertManager.HTTPHandler(redirectHandler)
		}
		redirectSrv = &http.Server{
			Addr:    redirectAddr,
			Handler: handler,
		}
	}
	shutdownAll := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
		if redirectSrv != nil {
			_ = redirectSrv.Shutdown(ctx)
		}
	}
	srv.SetShutdownFunc(shutdownAll)
	if len(loaded) > 0 {
		fmt.Printf(".env 已加载: %s\n", strings.Join(loaded, ", "))
	} else {
		fmt.Println(".env 未加载: 使用系统环境变量")
	}
	fmt.Printf("AI配置: endpoint=%s model=%s key_loaded=%v\n", mask(cfg.AIEndpoint), cfg.AIModel, cfg.AIKey != "")
	if httpsEnabled {
		if autocertEnabled {
			fmt.Printf("HTTPS已启用: autocert hosts=%s\n", strings.Join(autocertHosts, ","))
		} else {
			fmt.Printf("HTTPS已启用: cert=%s key=%s\n", certFile, keyFile)
		}
		if redirectSrv != nil {
			fmt.Printf("HTTP跳转已启用: addr=%s -> %s\n", redirectAddr, cfg.BaseURL)
		}
	} else {
		fmt.Println("HTTPS未启用: 使用HTTP")
	}
	fmt.Printf("学生入口:   %s/\n", cfg.BaseURL)
	fmt.Printf("教师面板:   %s/t\n", cfg.BaseURL)
	fmt.Printf("系统管理:   %s/admin\n", cfg.BaseURL)
	stopSig := make(chan os.Signal, 1)
	signal.Notify(stopSig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stopSig
		shutdownAll()
	}()
	if redirectSrv != nil {
		go func() {
			err := redirectSrv.ListenAndServe()
			if err != nil && err != http.ErrServerClosed {
				log.Printf("HTTP跳转服务启动失败: %v", err)
			}
		}()
	}
	if httpsEnabled {
		if autocertEnabled {
			err = httpSrv.ListenAndServeTLS("", "")
		} else {
			err = httpSrv.ListenAndServeTLS(certFile, keyFile)
		}
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
	defaultPort := "8080"
	if https {
		scheme = "https"
		defaultPort = "443"
	}
	host, port, err := splitHostPort(addr)
	if err != nil {
		return scheme + "://127.0.0.1:" + defaultPort
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = localIPv4()
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return scheme + "://" + host + ":" + port
}

func normalizeHTTPSBaseURL(baseURL, addr string) string {
	raw := strings.TrimSpace(baseURL)
	if raw == "" {
		return guessBaseURL(addr, true)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return guessBaseURL(addr, true)
	}
	u.Scheme = "https"
	_, port, err := splitHostPort(addr)
	if err == nil {
		u.Host = hostWithPort(u.Host, port)
	}
	return strings.TrimRight(u.String(), "/")
}

func hostWithPort(host, port string) string {
	if host == "" {
		return host
	}
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	trimmed := host
	if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
		trimmed = strings.TrimPrefix(strings.TrimSuffix(trimmed, "]"), "[")
	}
	return net.JoinHostPort(trimmed, port)
}

func buildRedirectTarget(baseURL string, r *http.Request) string {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return "https://" + r.Host + r.URL.RequestURI()
	}
	u.Path = r.URL.Path
	u.RawQuery = r.URL.RawQuery
	u.Fragment = ""
	if u.Scheme == "https" {
		u.Host = trimDefaultPort(u.Host, "443")
	} else if u.Scheme == "http" {
		u.Host = trimDefaultPort(u.Host, "80")
	}
	return u.String()
}

func trimDefaultPort(host, defaultPort string) string {
	if host == "" {
		return host
	}
	parsedHost, parsedPort, err := net.SplitHostPort(host)
	if err != nil {
		return host
	}
	if parsedPort != defaultPort {
		return host
	}
	if strings.Contains(parsedHost, ":") {
		return "[" + strings.Trim(parsedHost, "[]") + "]"
	}
	return parsedHost
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

func parseBool(raw string) bool {
	v := strings.ToLower(strings.TrimSpace(raw))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func resolveAutocertHosts(rawHosts, baseURL string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	addHost := func(host string) {
		host = strings.TrimSpace(strings.ToLower(host))
		host = strings.TrimPrefix(host, "http://")
		host = strings.TrimPrefix(host, "https://")
		if host == "" {
			return
		}
		if strings.Contains(host, "/") {
			host = strings.SplitN(host, "/", 2)[0]
		}
		if strings.Contains(host, ":") {
			parsedHost, _, err := net.SplitHostPort(host)
			if err == nil {
				host = parsedHost
			}
		}
		host = strings.Trim(host, "[]")
		if host == "" {
			return
		}
		if _, exists := seen[host]; exists {
			return
		}
		seen[host] = struct{}{}
		out = append(out, host)
	}
	for _, item := range strings.Split(rawHosts, ",") {
		addHost(item)
	}
	if len(out) > 0 {
		return out
	}
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Host == "" {
		return out
	}
	addHost(u.Host)
	return out
}

func filterAutocertHosts(hosts []string) []string {
	out := make([]string, 0, len(hosts))
	for _, host := range hosts {
		h := strings.TrimSpace(strings.ToLower(host))
		h = strings.TrimSuffix(h, ".")
		if h == "" || h == "localhost" {
			continue
		}
		if ip := net.ParseIP(h); ip != nil {
			continue
		}
		if !strings.Contains(h, ".") {
			continue
		}
		out = append(out, h)
	}
	return out
}
