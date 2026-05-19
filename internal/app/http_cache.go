package app

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func setRevalidatingFileCacheHeaders(w http.ResponseWriter, r *http.Request, entityKey string, info os.FileInfo) bool {
	etag := fileCacheETag(entityKey, info)
	lastModified := info.ModTime().UTC().Truncate(time.Second)
	h := w.Header()
	h.Set("Cache-Control", "private, no-cache")
	h.Set("ETag", etag)
	h.Set("Last-Modified", lastModified.Format(http.TimeFormat))
	h.Add("Vary", "Cookie")

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if ifNoneMatch := strings.TrimSpace(r.Header.Get("If-None-Match")); ifNoneMatch != "" {
		if etagListMatches(ifNoneMatch, etag) {
			w.WriteHeader(http.StatusNotModified)
			return true
		}
		return false
	}
	if ifModifiedSince := strings.TrimSpace(r.Header.Get("If-Modified-Since")); ifModifiedSince != "" {
		if t, err := http.ParseTime(ifModifiedSince); err == nil && !lastModified.After(t) {
			w.WriteHeader(http.StatusNotModified)
			return true
		}
	}
	return false
}

func fileCacheETag(entityKey string, info os.FileInfo) string {
	sum := sha256.Sum256([]byte(entityKey + "\x00" + strconv.FormatInt(info.ModTime().UnixNano(), 10) + "\x00" + info.Name() + "\x00" + strconv.FormatInt(info.Size(), 10)))
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

func etagListMatches(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || candidate == etag || strings.TrimPrefix(candidate, "W/") == etag {
			return true
		}
	}
	return false
}
