// largely adapted from https://github.com/gorilla/handlers/blob/master/handlers.go
// to add logging of request duration as last value (and drop referrer)

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"text/template"
	"time"
)

const (
	defaultRequestLoggingFormat = "{{.Client}} - {{.Username}} [{{.Timestamp}}] {{.Host}} {{.RequestMethod}} {{.Upstream}} {{.RequestURI}} {{.Protocol}} {{.UserAgent}} {{.StatusCode}} {{.ResponseSize}} {{.RequestDuration}}"
)

// responseLogger is wrapper of http.ResponseWriter that keeps track of its HTTP status
// code and body size
type responseLogger struct {
	w        http.ResponseWriter
	status   int
	size     int
	upstream string
	authInfo string
}

func (l *responseLogger) Header() http.Header {
	return l.w.Header()
}

// Support Websocket
func (l *responseLogger) Hijack() (rwc net.Conn, buf *bufio.ReadWriter, err error) {
	if hj, ok := l.w.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, errors.New("http.Hijacker is not available on writer")
}

func (l *responseLogger) ExtractGAPMetadata() {
	upstream := l.w.Header().Get("GAP-Upstream-Address")
	if upstream != "" {
		l.upstream = upstream
		l.w.Header().Del("GAP-Upstream-Address")
	}
	authInfo := l.w.Header().Get("GAP-Auth")
	if authInfo != "" {
		l.authInfo = authInfo
		l.w.Header().Del("GAP-Auth")
	}
}

func (l *responseLogger) Write(b []byte) (int, error) {
	if l.status == 0 {
		// The status will be StatusOK if WriteHeader has not been called yet
		l.status = http.StatusOK
	}
	l.ExtractGAPMetadata()
	size, err := l.w.Write(b)
	l.size += size
	return size, err
}

func (l *responseLogger) WriteHeader(s int) {
	l.ExtractGAPMetadata()
	l.w.WriteHeader(s)
	l.status = s
}

func (l *responseLogger) Status() int {
	return l.status
}

func (l *responseLogger) Size() int {
	return l.size
}

func (l *responseLogger) Flush() {
	if flusher, ok := l.w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// logMessageData is the container for all values that are available as variables in the request logging format.
// All values are pre-formatted strings so it is easy to use them in the format string.
type logMessageData struct {
	Client,
	Host,
	Protocol,
	RequestDuration,
	RequestMethod,
	RequestURI,
	ResponseSize,
	StatusCode,
	Timestamp,
	Upstream,
	UserAgent,
	Username string
}

// loggingHandler is the http.Handler implementation for LoggingHandlerTo and its friends
type loggingHandler struct {
	writer      io.Writer
	handler     http.Handler
	ipHeader    string
	logTemplate *template.Template
}

func LoggingHandler(out io.Writer, h http.Handler, ipHeader, requestLoggingTpl string) http.Handler {
	return loggingHandler{
		writer:      out,
		handler:     h,
		ipHeader:    ipHeader,
		logTemplate: template.Must(template.New("request-log").Parse(requestLoggingTpl + "\n")),
	}
}

func (h loggingHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	t := time.Now()
	url := *req.URL
	logger := &responseLogger{w: w}
	h.handler.ServeHTTP(logger, req)
	h.writeLogLine(logger.authInfo, logger.upstream, req, url, t, logger.Status(), logger.Size())
}

// Log entry for req similar to Apache Common Log Format.
// ts is the timestamp with which the entry should be logged.
// status, size are used to provide the response HTTP status and size.
func (h loggingHandler) writeLogLine(username, upstream string, req *http.Request, url url.URL, ts time.Time, status int, size int) {
	if username == "" {
		username = "-"
	}
	if upstream == "" {
		upstream = "-"
	}
	if url.User != nil && username == "-" {
		if name := url.User.Username(); name != "" {
			username = name
		}
	}

	client := req.RemoteAddr
	hval := extractClientIP(req, h.ipHeader)
	if hval != "" {
		client = hval
	}

	if c, _, err := net.SplitHostPort(client); err == nil {
		client = c
	}

	duration := float64(time.Now().Sub(ts)) / float64(time.Second)

	h.logTemplate.Execute(h.writer, logMessageData{
		Client:          client,
		Host:            req.Host,
		Protocol:        req.Proto,
		RequestDuration: fmt.Sprintf("%0.3f", duration),
		RequestMethod:   req.Method,
		RequestURI:      fmt.Sprintf("%q", url.RequestURI()),
		ResponseSize:    fmt.Sprintf("%d", size),
		StatusCode:      fmt.Sprintf("%d", status),
		Timestamp:       ts.Format("02/Jan/2006:15:04:05 -0700"),
		Upstream:        upstream,
		UserAgent:       fmt.Sprintf("%q", req.UserAgent()),
		Username:        username,
	})
}

// if logging is disabled, more efficient
// but still need to remove GAP-* metadata response headers via responseLogger
type noLoggingHandler struct {
	handler http.Handler
}

func (h noLoggingHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	logger := &responseLogger{w: w}
	h.handler.ServeHTTP(logger, req)
}

func NoLoggingHandler(h http.Handler) http.Handler {
	return noLoggingHandler{
		handler: h,
	}
}
