package remixware

// A client for the SCANOSS service: the project's fingerprints go out, the
// service's matches come back in the format of a SCANOSS results file, and
// the same code that reads those files turns them into findings. Nothing
// here runs unless asked for (remixware scan --scanoss).

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	// DefaultSCANOSSURL is the free SCANOSS service, the OSSKB run by the
	// Software Transparency Foundation.
	DefaultSCANOSSURL = "https://api.osskb.org"
	// SCANOSSKeyURL is the SCANOSS service for holders of an API key.
	SCANOSSKeyURL = "https://api.scanoss.com"

	scanossEndpoint  = "/scan/direct"
	scanossPostSize  = 32 << 10 // bytes of fingerprints per request, as scanoss.py sends
	scanossPostFiles = 32       // files per request

	scanossMaxBackoff = 60 * time.Second
)

// scanossMaxResponse is the biggest answer read (a variable for the tests).
var scanossMaxResponse = 64 << 20

// SCANOSSOptions controls ScanSCANOSS.
type SCANOSSOptions struct {
	URL         string // the service: a base URL, or a full URL whose path is the scan endpoint
	APIKey      string // sent as x-api-key; without URL, the key selects SCANOSSKeyURL
	Exclude     []string
	IncludeDeps bool
	Timeout     time.Duration // for each request (default 3 minutes)
	Retries     int           // tries after the first, for answers that can change (default 5)
	Workers     int           // requests at a time (default 4)
	Client      *http.Client
	Progress    func(format string, args ...any)

	sleep func(context.Context, time.Duration) error // waiting between tries, for the tests
}

// SCANOSSResult is what the SCANOSS service said about a project.
type SCANOSSResult struct {
	URL      string // the scan endpoint asked
	Files    int    // files whose fingerprints were sent
	Requests int
	// Results maps each project path to its list of matches, as in a
	// results file written by scanoss-py.
	Results map[string]json.RawMessage
}

// scanossURL returns the scan endpoint for a URL given by the user.
func scanossURL(u, key string) (string, error) {
	if u == "" {
		u = DefaultSCANOSSURL
		if key != "" {
			u = SCANOSSKeyURL
		}
	}
	p, err := url.Parse(u)
	if err != nil || (p.Scheme != "https" && p.Scheme != "http") || p.Host == "" {
		return "", fmt.Errorf("SCANOSS URL %q isn't an http or https address", u)
	}
	if p.Path == "" || p.Path == "/" {
		p.Path = scanossEndpoint
	}
	return p.String(), nil
}

type scanossBatch struct {
	wfp   string
	names map[string]string // name sent -> project path
}

// ScanSCANOSS sends the fingerprints of a project's source files to the
// SCANOSS service and returns what it found. No source code is sent: each
// file goes as a number with the file's extension, its MD5 and size, and
// the winnowing hashes of its text (see WFP). Answers are mapped back to the
// files by those numbers; an answer about any other name is dropped. The
// files are the ones a scan reads, minified ones included (SCANOSS can name
// a whole minified library), generated and binary ones left out.
func ScanSCANOSS(project string, opt SCANOSSOptions) (*SCANOSSResult, error) {
	endpoint, err := scanossURL(opt.URL, opt.APIKey)
	if err != nil {
		return nil, err
	}
	abs, err := realPath(project)
	if err != nil {
		return nil, err
	}
	if opt.Timeout <= 0 {
		opt.Timeout = 3 * time.Minute
	}
	if opt.Retries < 0 {
		opt.Retries = 0
	} else if opt.Retries == 0 {
		opt.Retries = 5
	}
	if opt.Workers <= 0 {
		opt.Workers = 4
	}
	if opt.Client == nil {
		opt.Client = &http.Client{}
	}
	if opt.sleep == nil {
		opt.sleep = sleepCtx
	}
	progress := opt.Progress
	if progress == nil {
		progress = func(string, ...any) {}
	}

	files, _, unreadable, err := projectFiles(abs, opt.Exclude, opt.IncludeDeps, func(name string) bool {
		low := strings.ToLower(name)
		return isSource(name) || strings.HasSuffix(low, ".min.js") || strings.HasSuffix(low, ".min.css")
	})
	if err != nil {
		return nil, err
	}
	var batches []scanossBatch
	cur := scanossBatch{names: map[string]string{}}
	var buf strings.Builder
	sent := 0
	for _, f := range files {
		rel := relSlash(abs, f)
		data, skip := readSource(f)
		switch skip {
		case "", "minified":
			if skip == "minified" {
				data, err = os.ReadFile(f)
				if err != nil {
					unreadable = append(unreadable, rel)
					continue
				}
			}
		case "unreadable":
			unreadable = append(unreadable, rel)
			continue
		default:
			continue
		}
		sent++
		name := strconv.Itoa(sent) + pySuffix(rel)
		w := WFP(name, rel, data)
		if buf.Len() > 0 && (buf.Len()+len(w) >= scanossPostSize || len(cur.names) >= scanossPostFiles) {
			cur.wfp = buf.String()
			batches = append(batches, cur)
			cur = scanossBatch{names: map[string]string{}}
			buf.Reset()
		}
		buf.WriteString(w)
		cur.names[name] = rel
	}
	if buf.Len() > 0 {
		cur.wfp = buf.String()
		batches = append(batches, cur)
	}
	if len(unreadable) > 0 {
		return nil, fmt.Errorf("can't read %s in %s: %s. Fix the permissions, or leave them out with --exclude",
			countNoun(len(unreadable), "path", "paths"), project, strings.Join(unreadable, ", "))
	}
	res := &SCANOSSResult{URL: endpoint, Files: sent, Requests: len(batches), Results: map[string]json.RawMessage{}}
	if len(batches) == 0 {
		return res, nil
	}
	host := endpoint
	if p, err := url.Parse(endpoint); err == nil {
		host = p.Host
	}
	progress("SCANOSS: sending the fingerprints of %s to %s in %s", countNoun(sent, "file", "files"), host,
		countNoun(len(batches), "request", "requests"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var (
		mu       sync.Mutex
		firstErr error
		wg       sync.WaitGroup
		next     = make(chan scanossBatch)
	)
	for w := 0; w < min(opt.Workers, len(batches)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for b := range next {
				got, err := scanossPost(ctx, endpoint, host, b, opt)
				mu.Lock()
				if err != nil {
					if firstErr == nil {
						firstErr = err
						cancel()
					}
				} else {
					for name, matches := range got {
						res.Results[b.names[name]] = matches
					}
				}
				mu.Unlock()
			}
		}()
	}
	for _, b := range batches {
		select {
		case next <- b:
		case <-ctx.Done():
		}
	}
	close(next)
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return res, nil
}

// scanossPost sends one request and returns the answers about the names it
// sent. Answers that can change (no answer, a timeout, HTTP 429 or 5xx) are
// tried again; the rest end the scan.
func scanossPost(ctx context.Context, endpoint, host string, b scanossBatch, opt SCANOSSOptions) (map[string]json.RawMessage, error) {
	id := requestID()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", id+".wfp")
	if err != nil {
		return nil, err
	}
	io.WriteString(fw, b.wfp)
	mw.WriteField("format", "plain")
	if err := mw.Close(); err != nil {
		return nil, err
	}
	for try := 0; ; try++ {
		last := try >= opt.Retries
		rctx, rcancel := context.WithTimeout(ctx, opt.Timeout)
		req, err := http.NewRequestWithContext(rctx, http.MethodPost, endpoint, bytes.NewReader(body.Bytes()))
		if err != nil {
			rcancel()
			return nil, err
		}
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("User-Agent", "remixware/"+Version)
		req.Header.Set("x-request-id", id)
		if opt.APIKey != "" {
			req.Header.Set("x-api-key", opt.APIKey)
			req.Header.Set("X-Session", opt.APIKey)
		}
		resp, err := opt.Client.Do(req)
		if err != nil {
			rcancel()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if last {
				return nil, fmt.Errorf("SCANOSS: no answer from %s after %d tries: %v", host, try+1, err)
			}
			if err := opt.sleep(ctx, 5*time.Second); err != nil {
				return nil, err
			}
			continue
		}
		data, rerr := io.ReadAll(io.LimitReader(resp.Body, int64(scanossMaxResponse)+1))
		resp.Body.Close()
		rcancel()
		switch {
		case resp.StatusCode == http.StatusOK && rerr == nil:
			if len(data) > scanossMaxResponse {
				return nil, fmt.Errorf("SCANOSS: the answer from %s is over %d MB, too big to read", host, scanossMaxResponse>>20)
			}
			return scanossAnswer(data, host, b)
		case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
			// A cut-off answer, a rate limit or a server error may pass.
			if last {
				what := "the service is unavailable"
				switch {
				case resp.StatusCode == http.StatusTooManyRequests:
					what = "the service's rate limit was reached"
				case rerr != nil:
					what = "the answer was cut off: " + rerr.Error()
				}
				return nil, fmt.Errorf("SCANOSS: %s (HTTP %d from %s after %d tries)%s", what, resp.StatusCode, host, try+1, bodyNote(data))
			}
			wait := 5 * time.Second
			if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
				wait = retryAfter(resp.Header.Get("Retry-After"), data)
			}
			if err := opt.sleep(ctx, wait); err != nil {
				return nil, err
			}
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			return nil, fmt.Errorf("SCANOSS: %s refused the request (HTTP %d)%s. Check the API key (--scanoss-key or SCANOSS_API_KEY) and the URL",
				host, resp.StatusCode, bodyNote(data))
		default:
			return nil, fmt.Errorf("SCANOSS: %s refused the request (HTTP %d)%s", host, resp.StatusCode, bodyNote(data))
		}
	}
}

// scanossAnswer reads one answer: an object that maps each name sent to a
// list of matches. Names that weren't in the request are dropped.
func scanossAnswer(data []byte, host string, b scanossBatch) (map[string]json.RawMessage, error) {
	var got map[string]json.RawMessage
	if err := json.Unmarshal(data, &got); err != nil {
		return nil, fmt.Errorf("SCANOSS: the answer from %s isn't SCANOSS results: %v", host, err)
	}
	out := map[string]json.RawMessage{}
	for name, matches := range got {
		if _, ok := b.names[name]; !ok {
			continue
		}
		var list []json.RawMessage
		if err := json.Unmarshal(matches, &list); err != nil {
			return nil, fmt.Errorf("SCANOSS: the answer from %s about %s isn't a list of matches", host, b.names[name])
		}
		out[name] = matches
	}
	return out, nil
}

// retryAfter reads how long a rate-limited service asks to wait: the
// Retry-After header in seconds, or a retry_after field in the answer, at
// most a minute, 5 seconds when it doesn't say.
func retryAfter(header string, body []byte) time.Duration {
	secs := -1.0
	if v, err := strconv.ParseFloat(strings.TrimSpace(header), 64); err == nil {
		secs = v
	} else {
		var b struct {
			RetryAfter *float64 `json:"retry_after"`
		}
		if json.Unmarshal(body, &b) == nil && b.RetryAfter != nil {
			secs = *b.RetryAfter
		}
	}
	if secs < 0 {
		return 5 * time.Second
	}
	return min(time.Duration(secs*float64(time.Second)), scanossMaxBackoff)
}

// bodyNote quotes the start of an error answer, printable characters only,
// since it comes from elsewhere and goes to a terminal.
func bodyNote(data []byte) string {
	s := strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return ' '
	}, string(data))
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	if s == "" {
		return ""
	}
	return ": " + s
}

// pySuffix is a file's extension as Python's pathlib gives it: ".gz" for
// "a.tar.gz", nothing for ".bashrc" or "file.".
func pySuffix(name string) string {
	base := path.Base(name)
	i := strings.LastIndexByte(base, '.')
	if i <= 0 || i == len(base)-1 {
		return ""
	}
	return base[i:]
}

func requestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// JSON returns the results as a SCANOSS results file, which --from reads.
func (r *SCANOSSResult) JSON() ([]byte, error) {
	if r.Results == nil {
		return []byte("{}\n"), nil
	}
	out, err := json.MarshalIndent(r.Results, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// Findings turns the results into findings, with the Input that names the
// service.
func (r *SCANOSSResult) Findings(project string) ([]Finding, Input, error) {
	abs, err := realPath(project)
	if err != nil {
		return nil, Input{}, err
	}
	found, err := scanossFindings(r.Results, "the SCANOSS answers", abs)
	if err != nil {
		return nil, Input{}, err
	}
	return found, Input{Kind: "scanoss-api", Path: r.URL, Files: r.Files, Matches: len(found)}, nil
}
