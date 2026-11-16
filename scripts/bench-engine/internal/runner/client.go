// Package runner drives Fyredocs tools through the real stack — gateway, job
// service, NATS, workers, object storage — the way a browser does, so a
// benchmark measures what users get, not a tool invoked in isolation.
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// toolGroup maps a tool to its API group (the worker service that runs it).
//
// NOTE: mirrors job-service/internal/routing/routing.go ToolServiceMap,
// duplicated by design (CLAUDE.md §1 forbids importing another service's code);
// a tool missing here is reported as unknown, never silently skipped.
var toolGroup = map[string]string{
	"pdf-to-word": "convert-from-pdf", "pdf-to-excel": "convert-from-pdf", "pdf-to-image": "convert-from-pdf",
	"pdf-to-text": "convert-from-pdf", "pdf-to-pdfa": "convert-from-pdf", "pdf-to-odt": "convert-from-pdf",
	"pdf-to-ods": "convert-from-pdf", "pdf-to-odp": "convert-from-pdf", "pdf-to-html": "convert-from-pdf",
	"pdf-to-powerpoint": "convert-from-pdf",
	"word-to-pdf":       "convert-to-pdf", "excel-to-pdf": "convert-to-pdf", "powerpoint-to-pdf": "convert-to-pdf",
	"html-to-pdf": "convert-to-pdf", "image-to-pdf": "convert-to-pdf", "odt-to-pdf": "convert-to-pdf",
	"ods-to-pdf": "convert-to-pdf", "odp-to-pdf": "convert-to-pdf", "word-to-odt": "convert-to-pdf",
	"excel-to-ods": "convert-to-pdf", "powerpoint-to-odp": "convert-to-pdf",
	"merge-pdf": "organize-pdf", "split-pdf": "organize-pdf", "rotate-pdf": "organize-pdf",
	"scan-to-pdf": "organize-pdf", "protect-pdf": "organize-pdf", "unlock-pdf": "organize-pdf",
	"pdf-metadata": "organize-pdf", "watermark-pdf": "organize-pdf",
	"compress-pdf": "optimize-pdf", "repair-pdf": "optimize-pdf", "ocr-pdf": "optimize-pdf", "flatten-pdf": "optimize-pdf",
}

// Group returns the API group (worker service) of tool.
func Group(tool string) (string, bool) { g, ok := toolGroup[tool]; return g, ok }

// Client is a logged-in browser-like session against the stack.
type Client struct {
	base string
	http *http.Client
}

// NewClient returns a client for publicOrigin (the URL the stack signs
// presigned URLs with, e.g. http://localhost). When edgeAddr is set (e.g.
// "caddy:80"), every connection to publicOrigin's host is dialled to edgeAddr
// instead: inside the stack's Docker network "localhost" is the harness
// container itself, but the edge must still see Host: localhost or the
// presigned upload signatures (which cover the host) would not match.
func NewClient(publicOrigin, edgeAddr string) (*Client, error) {
	u, err := url.Parse(publicOrigin)
	if err != nil {
		return nil, err
	}
	jar, _ := cookiejar.New(nil)
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if edgeAddr != "" && strings.HasPrefix(addr, u.Hostname()+":") {
				addr = edgeAddr
			}
			return dialer.DialContext(ctx, network, addr)
		},
		MaxIdleConnsPerHost: 8,
	}
	return &Client{base: strings.TrimRight(publicOrigin, "/"), http: &http.Client{Jar: jar, Transport: tr, Timeout: 10 * time.Minute}}, nil
}

// envelope is the standard API response shape (CLAUDE.md §7).
type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *struct {
		Code    string `json:"code"`
		Details string `json:"details"`
	} `json:"error"`
}

// APIError is a non-2xx API response.
type APIError struct {
	Status int
	Code   string
	Detail string
}

func (e *APIError) Error() string { return fmt.Sprintf("HTTP %d %s: %s", e.Status, e.Code, e.Detail) }

// call sends a JSON request and decodes the envelope's data into out.
func (c *Client) call(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.base)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var env envelope
	_ = json.Unmarshal(raw, &env)
	if resp.StatusCode/100 != 2 {
		e := &APIError{Status: resp.StatusCode, Detail: strings.TrimSpace(string(raw))}
		if env.Error != nil {
			e.Code, e.Detail = env.Error.Code, env.Error.Details
		}
		return e
	}
	if out != nil && len(env.Data) > 0 {
		return json.Unmarshal(env.Data, out)
	}
	return nil
}

// Login logs in, signing the account up first if login fails because it does
// not exist yet. Signup needs fullName and country (auth-service signupRequest).
func (c *Client) Login(ctx context.Context, email, password string) error {
	err := c.call(ctx, http.MethodPost, "/auth/login", map[string]string{"email": email, "password": password}, nil)
	if err == nil {
		return nil
	}
	if e, ok := err.(*APIError); !ok || e.Status != http.StatusUnauthorized {
		return err
	}
	if err := c.call(ctx, http.MethodPost, "/auth/signup", map[string]string{
		"email": email, "password": password, "fullName": "Bench Engine", "country": "IN",
	}, nil); err != nil {
		return fmt.Errorf("signup: %w", err)
	}
	return c.call(ctx, http.MethodPost, "/auth/login", map[string]string{"email": email, "password": password}, nil)
}

// Upload sends path through the presigned multipart protocol and returns the
// upload ID.
func (c *Client) Upload(ctx context.Context, path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	ct := mime.TypeByExtension(filepath.Ext(path))
	if ct == "" {
		ct = "application/octet-stream"
	}
	var init struct {
		UploadID string `json:"uploadId"`
		PartSize int64  `json:"partSize"`
		Parts    []struct {
			PartNumber int    `json:"partNumber"`
			URL        string `json:"url"`
		} `json:"parts"`
	}
	if err := c.call(ctx, http.MethodPost, "/api/upload/init",
		map[string]any{"fileName": filepath.Base(path), "fileSize": fi.Size(), "contentType": ct}, &init); err != nil {
		return "", fmt.Errorf("upload init: %w", err)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	type part struct {
		PartNumber int    `json:"partNumber"`
		ETag       string `json:"etag"`
	}
	var done []part
	for _, p := range init.Parts {
		off := int64(p.PartNumber-1) * init.PartSize
		n := min(init.PartSize, fi.Size()-off)
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, p.URL, io.NewSectionReader(f, off, n))
		if err != nil {
			return "", err
		}
		req.ContentLength = n
		resp, err := c.http.Do(req)
		if err != nil {
			return "", fmt.Errorf("upload part %d: %w", p.PartNumber, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			return "", fmt.Errorf("upload part %d: HTTP %d", p.PartNumber, resp.StatusCode)
		}
		done = append(done, part{p.PartNumber, strings.Trim(resp.Header.Get("ETag"), `"`)})
	}
	if err := c.call(ctx, http.MethodPost, "/api/upload/"+init.UploadID+"/complete", map[string]any{"parts": done}, nil); err != nil {
		return "", fmt.Errorf("upload complete: %w", err)
	}
	return init.UploadID, nil
}

// Job is the subset of the job API response the harness reads.
type Job struct {
	ID            string     `json:"id"`
	Status        string     `json:"status"`
	FailureReason string     `json:"failureReason"`
	CreatedAt     time.Time  `json:"createdAt"`
	CompletedAt   *time.Time `json:"completedAt"`
	OutputName    string     `json:"outputFileName"`
	OutputSize    int64      `json:"outputFileSize"`
}

// CreateJob starts tool on the uploads with options.
func (c *Client) CreateJob(ctx context.Context, tool string, uploadIDs []string, options map[string]any) (Job, error) {
	g, ok := Group(tool)
	if !ok {
		return Job{}, fmt.Errorf("unknown tool %q", tool)
	}
	var j Job
	err := c.call(ctx, http.MethodPost, "/api/"+g+"/"+tool, map[string]any{"uploadIds": uploadIDs, "options": options}, &j)
	return j, err
}

// Wait polls the job every interval until it completes or fails, or ctx ends.
func (c *Client) Wait(ctx context.Context, tool, id string, interval time.Duration) (Job, error) {
	g, _ := Group(tool)
	for {
		var j Job
		if err := c.call(ctx, http.MethodGet, "/api/"+g+"/"+tool+"/"+id, nil, &j); err != nil {
			return j, err
		}
		if j.Status == "completed" || j.Status == "failed" {
			return j, nil
		}
		select {
		case <-ctx.Done():
			return j, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// Download saves a completed job's output to dst.
func (c *Client) Download(ctx context.Context, tool, id, dst string) error {
	g, _ := Group(tool)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/"+g+"/"+tool+"/"+id+"/download", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Origin", c.base)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("download: HTTP %d: %s", resp.StatusCode, b)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// DefaultOptions are the tool options a benchmark job sends: fixed, valid
// values so every run of a tool does the same work. password is the corpus
// item's password param (unlock-pdf).
func DefaultOptions(tool, password string) map[string]any {
	switch tool {
	case "rotate-pdf":
		return map[string]any{"rotation": "90"}
	case "split-pdf":
		return map[string]any{"mode": "range", "range": "1-2"}
	case "protect-pdf":
		return map[string]any{"password": "bench-protect-1"}
	case "unlock-pdf":
		return map[string]any{"password": password}
	case "pdf-metadata":
		return map[string]any{"title": "Bench Engine", "keywords": "bench, fixture"}
	case "pdf-to-image":
		return map[string]any{"format": "png", "dpi": "150"}
	default:
		return map[string]any{}
	}
}
