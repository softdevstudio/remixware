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
	"errors"
	"fmt"
	"io"
	"math"
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
	now   func() time.Time                           // the clock, for the tests
}

// SCANOSSResult is what the SCANOSS service said about a project.
type SCANOSSResult struct {
	URL      string // the scan endpoint asked, without any user name, password or query
	Files    int    // files whose fingerprints were sent
	Requests int
	Symlinks int // symbolic links passed over, as a scan does
	// Results maps each project path to its list of matches, as in a
	// results file written by scanoss-py.
	Results map[string]json.RawMessage
}

// scanossURL returns the scan endpoint for a URL given by the user: the
// URL's scheme and host (with any user name and password) followed by
// /scan/direct, as SCANOSS's own scanner builds it. dropped is a path the
// URL had that was left out.
func scanossURL(u, key string) (endpoint, dropped string, err error) {
	if u == "" {
		u = DefaultSCANOSSURL
		if key != "" {
			u = SCANOSSKeyURL
		}
	}
	p, err := url.Parse(strings.TrimSpace(u))
	if err != nil || (p.Scheme != "https" && p.Scheme != "http") || p.Host == "" {
		return "", "", errors.New("the SCANOSS URL isn't an http or https address with a host name")
	}
	if p.Path != "" && p.Path != "/" && p.Path != scanossEndpoint {
		dropped = p.Path
	}
	p.Path, p.RawPath, p.RawQuery, p.ForceQuery, p.Fragment, p.RawFragment = scanossEndpoint, "", "", false, "", ""
	return p.String(), dropped, nil
}

// publicURL is a URL to show or record: scheme, host and path, with no user
// name, password, query or control characters.
func publicURL(p *url.URL) string {
	return printable((&url.URL{Scheme: p.Scheme, Host: p.Host, Path: p.Path}).String(), 300)
}

// printable keeps text from elsewhere fit for a terminal or a Markdown
// file: anything that isn't a printable character (line breaks, escape
// sequences, direction marks) becomes a space, runs of spaces become one,
// and the text is cut at max characters.
func printable(s string, max int) string {
	s = strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return ' '
	}, s)), " ")
	if r := []rune(s); len(r) > max {
		s = string(r[:max]) + "..."
	}
	return s
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
	endpoint, dropped, err := scanossURL(opt.URL, opt.APIKey)
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
		// A redirect could send the key and the fingerprints to any host,
		// so none is followed.
		opt.Client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	if opt.sleep == nil {
		opt.sleep = sleepCtx
	}
	if opt.now == nil {
		opt.now = time.Now
	}
	var progressMu sync.Mutex
	say := opt.Progress
	progress := func(format string, args ...any) {
		if say != nil {
			progressMu.Lock()
			say(format, args...)
			progressMu.Unlock()
		}
	}
	opt.Progress = progress

	files, symlinks, unreadable, err := projectFiles(abs, opt.Exclude, opt.IncludeDeps, func(name string) bool {
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
	ep, _ := url.Parse(endpoint)
	host := printable(ep.Host, 200)
	res := &SCANOSSResult{URL: publicURL(ep), Files: sent, Requests: len(batches), Symlinks: symlinks,
		Results: map[string]json.RawMessage{}}
	if len(batches) == 0 {
		return res, nil
	}
	if dropped != "" {
		progress("SCANOSS: asking %s, the service at the URL's host; the path %s is left out, as SCANOSS's scanner does",
			res.URL, printable(dropped, 200))
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
// sent. Answers that can change (no answer, a timeout, HTTP 429 or 5xx, an
// answer that leaves files out) are tried again; the rest end the scan.
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
	tries := opt.Retries + 1
	again := func(try int, why string, wait time.Duration) error {
		opt.Progress("SCANOSS: %s; asking again in %s (try %d of %d)", why, waitText(wait, opt.now()), try+2, tries)
		return opt.sleep(ctx, wait)
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
			// The error without the URL, which can hold a user name or a query.
			var ue *url.Error
			if errors.As(err, &ue) {
				err = ue.Err
			}
			if last {
				return nil, fmt.Errorf("SCANOSS: no answer from %s after %d tries: %s", host, try+1, printable(err.Error(), 300))
			}
			if err := again(try, "no answer from "+host, 5*time.Second); err != nil {
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
			got, err := scanossAnswer(data, host, b)
			if err != nil {
				return nil, err
			}
			if len(got) == len(b.names) {
				return got, nil
			}
			// The service drops the files of a part of its work that failed
			// and still answers 200 with the rest. A check must not pass on
			// the files it didn't answer about.
			why := fmt.Sprintf("the answer from %s covers %d of the %d files sent", host, len(got), len(b.names))
			if last {
				return nil, fmt.Errorf("SCANOSS: %s, after %d tries", why, try+1)
			}
			if err := again(try, why, 5*time.Second); err != nil {
				return nil, err
			}
		case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
			// A cut-off answer, a rate limit or a server error may pass.
			wait, asked := 5*time.Second, false
			if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
				if d, ok := serviceWait(resp.Header.Get("Retry-After"), data, opt.now()); ok {
					wait, asked = d, true
				}
			}
			// A service that asks for a longer wait than the tries left
			// could cover won't answer this scan: say so instead of asking
			// again (the free service answers a used-up monthly limit with a
			// wait until the next month).
			if asked && wait > 0 && (last || wait > time.Duration(opt.Retries-try)*scanossMaxBackoff) {
				after := ""
				if try > 0 {
					after = fmt.Sprintf(", after %d tries", try+1)
				}
				hint := ""
				if opt.APIKey == "" {
					hint = ", or use a SCANOSS API key (--scanoss-key)"
				}
				return nil, fmt.Errorf("SCANOSS: %s asks Remixware to wait %s (HTTP %d%s)%s. Try again then%s",
					host, waitText(wait, opt.now()), resp.StatusCode, after, bodyNote(data), hint)
			}
			what := "the service is unavailable"
			switch {
			case resp.StatusCode == http.StatusTooManyRequests:
				what = "the service's rate limit was reached"
			case rerr != nil:
				what = "the answer was cut off: " + printable(rerr.Error(), 200)
			}
			if last {
				return nil, fmt.Errorf("SCANOSS: %s (HTTP %d from %s after %d tries)%s", what, resp.StatusCode, host, try+1, bodyNote(data))
			}
			if err := again(try, fmt.Sprintf("%s (HTTP %d from %s)", what, resp.StatusCode, host), min(wait, scanossMaxBackoff)); err != nil {
				return nil, err
			}
		case resp.StatusCode >= 300 && resp.StatusCode < 400:
			to := ""
			if loc, err := req.URL.Parse(resp.Header.Get("Location")); err == nil && resp.Header.Get("Location") != "" {
				to = " to " + publicURL(loc)
			}
			return nil, fmt.Errorf("SCANOSS: %s answered with a redirect%s (HTTP %d). Remixware doesn't follow redirects, since they could take the key and the fingerprints elsewhere: if you trust the new address, give it with --scanoss-url",
				host, to, resp.StatusCode)
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			return nil, fmt.Errorf("SCANOSS: %s refused the request (HTTP %d)%s. Check the API key (--scanoss-key or SCANOSS_API_KEY) and the URL",
				host, resp.StatusCode, bodyNote(data))
		default:
			return nil, fmt.Errorf("SCANOSS: %s refused the request (HTTP %d)%s", host, resp.StatusCode, bodyNote(data))
		}
	}
}

// scanossAnswer reads one answer: an object that maps each name sent to a
// list of matches. Names that weren't in the request are dropped, and so
// are null or empty lists: the service gives every file it answers about
// at least one entry, "none" when nothing matched.
func scanossAnswer(data []byte, host string, b scanossBatch) (map[string]json.RawMessage, error) {
	var got map[string]json.RawMessage
	if err := json.Unmarshal(data, &got); err != nil {
		return nil, fmt.Errorf("SCANOSS: the answer from %s isn't SCANOSS results: %s", host, printable(err.Error(), 200))
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
		if len(list) == 0 {
			continue
		}
		out[name] = matches
	}
	return out, nil
}

// serviceWait reads how long a service that turned a request away asks to
// wait: the Retry-After header, in seconds or as a date, or else a
// retry_after field in the answer. It reports false when the service
// doesn't say.
func serviceWait(header string, body []byte, now time.Time) (time.Duration, bool) {
	header = strings.TrimSpace(header)
	if v, err := strconv.ParseFloat(header, 64); err == nil {
		if d, ok := waitSeconds(v); ok {
			return d, true
		}
	} else if t, err := http.ParseTime(header); err == nil {
		return max(t.Sub(now), 0), true
	}
	var b struct {
		RetryAfter *float64 `json:"retry_after"`
	}
	if json.Unmarshal(body, &b) == nil && b.RetryAfter != nil {
		return waitSeconds(*b.RetryAfter)
	}
	return 0, false
}

// waitSeconds turns a number of seconds from a service into a wait of at
// most a year. It reports false for numbers that can't be a wait.
func waitSeconds(v float64) (time.Duration, bool) {
	const year = 365 * 24 * time.Hour
	switch {
	case math.IsNaN(v) || v < 0:
		return 0, false
	case v >= year.Seconds():
		return year, true
	}
	return time.Duration(v * float64(time.Second)), true
}

// waitText says how long a wait is, and for an hour or more until when.
func waitText(d time.Duration, now time.Time) string {
	n := func(v float64, one, many string) string {
		s := strconv.FormatFloat(v, 'f', 1, 64)
		s = strings.TrimSuffix(s, ".0")
		if s == "1" {
			return "1 " + one
		}
		return s + " " + many
	}
	switch {
	case d < 90*time.Second:
		return n(math.Round(d.Seconds()), "second", "seconds")
	case d < 90*time.Minute:
		return n(math.Round(d.Minutes()), "minute", "minutes")
	}
	until := ", until " + now.Add(d).UTC().Round(time.Minute).Format("2006-01-02 15:04 UTC")
	if d < 48*time.Hour {
		return n(d.Hours(), "hour", "hours") + until
	}
	return n(d.Hours()/24, "day", "days") + until
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
	found, err := scanossFindings(r.Results, "the SCANOSS answers", abs, true)
	if err != nil {
		return nil, Input{}, err
	}
	return found, Input{Kind: "scanoss-api", Path: r.URL, Files: r.Files, Matches: len(found)}, nil
}
