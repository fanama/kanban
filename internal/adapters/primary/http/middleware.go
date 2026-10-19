package http

import (
	"compress/gzip"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// WithTiming reports the application time on every response and logs requests
// that exceed the interactive budget. WebSocket upgrades keep their own conn.
func WithTiming(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isUpgrade(r) {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		timed := &timingWriter{ResponseWriter: w, start: start}
		next.ServeHTTP(timed, r)
		elapsed := time.Since(start)
		if elapsed > 250*time.Millisecond {
			log.Printf("slow request: %s %s board=%q status=%d bytes=%d in %s",
				r.Method, r.URL.Path, boardHint(r), timed.status, timed.bytes, elapsed)
		}
	})
}

func isUpgrade(r *http.Request) bool {
	return r.URL.Path == "/ws" || strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

func boardHint(r *http.Request) string {
	if board := r.URL.Query().Get("board"); board != "" {
		return board
	}
	if cookie, err := r.Cookie(boardCookie); err == nil {
		return cookie.Value
	}
	return ""
}

type timingWriter struct {
	http.ResponseWriter
	start  time.Time
	status int
	bytes  int
	wrote  bool
}

func (w *timingWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.writeTiming()
	w.ResponseWriter.WriteHeader(code)
}

func (w *timingWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.writeTiming()
	n, err := w.ResponseWriter.Write(data)
	w.bytes += n
	return n, err
}

func (w *timingWriter) writeTiming() {
	if w.wrote {
		return
	}
	w.wrote = true
	w.Header().Set("Server-Timing", fmt.Sprintf("app;dur=%.1f", float64(time.Since(w.start).Microseconds())/1000))
}

// WithGzip compresses the response types the app serves when the client accepts
// gzip. WebSocket upgrades and already encoded responses pass through untouched.
func WithGzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isUpgrade(r) || !acceptsGzip(r) {
			next.ServeHTTP(w, r)
			return
		}
		wrapped := &gzipWriter{ResponseWriter: w}
		defer wrapped.Close()
		next.ServeHTTP(wrapped, r)
	})
}

func acceptsGzip(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept-Encoding")), "gzip")
}

type gzipWriter struct {
	http.ResponseWriter
	writer  *gzip.Writer
	decided bool
}

func (w *gzipWriter) WriteHeader(code int) {
	w.decide()
	w.ResponseWriter.WriteHeader(code)
}

func (w *gzipWriter) Write(data []byte) (int, error) {
	w.decide()
	if w.writer != nil {
		return w.writer.Write(data)
	}
	return w.ResponseWriter.Write(data)
}

func (w *gzipWriter) decide() {
	if w.decided {
		return
	}
	w.decided = true
	if w.writer != nil || w.Header().Get("Content-Encoding") != "" || !compressible(w.Header().Get("Content-Type")) {
		return
	}
	w.Header().Del("Content-Length")
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Add("Vary", "Accept-Encoding")
	w.writer = gzip.NewWriter(w.ResponseWriter)
}

func (w *gzipWriter) Close() error {
	if w.writer == nil {
		return nil
	}
	return w.writer.Close()
}

func compressible(contentType string) bool {
	contentType = strings.ToLower(contentType)
	for _, prefix := range []string{
		"text/html", "text/css", "text/plain",
		"application/json", "application/javascript", "text/javascript",
	} {
		if strings.HasPrefix(contentType, prefix) {
			return true
		}
	}
	return false
}
