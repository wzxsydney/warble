package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Page reads only public HTTP pages from the current turn's search results.
// DNS is resolved and checked at dial time; connections use the checked IP.
func (w *WebSearch) fetch(ctx context.Context, arguments string, allowed map[string]bool) string {
	var args struct {
		URL string `json:"url"`
	}
	if json.Unmarshal([]byte(arguments), &args) != nil || !allowed[args.URL] {
		return `{"error":"Page URL must come from this turn's search results."}`
	}
	target, err := url.Parse(args.URL)
	if err != nil || !validPageURL(target) {
		return `{"error":"Only public HTTP or HTTPS pages are allowed."}`
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	transport := &http.Transport{DialContext: publicDial}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || !validPageURL(req.URL) {
			return fmt.Errorf("redirect blocked")
		}
		return nil
	}}
	if w.pageHTTP != nil {
		client = w.pageHTTP
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, args.URL, nil)
	if err != nil {
		return `{"error":"Invalid page URL."}`
	}
	req.Header.Set("User-Agent", "Warble/1.0 (web search assistant)")
	req.Header.Set("Accept", "text/html,text/plain")
	resp, err := client.Do(req)
	if err != nil {
		return `{"error":"Page unavailable or timed out."}`
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Sprintf(`{"error":"Page returned HTTP %d."}`, resp.StatusCode)
	}
	kind := resp.Header.Get("Content-Type")
	if !strings.Contains(kind, "text/html") && !strings.Contains(kind, "text/plain") {
		return `{"error":"Page is not readable text."}`
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
	if err != nil || len(data) > 2*1024*1024 {
		return `{"error":"Page too large or unreadable."}`
	}
	text := string(data)
	if strings.Contains(kind, "text/html") {
		text = pageText(text)
	}
	text = clip(strings.Join(strings.Fields(text), " "), 6000)
	if w.Logger != nil {
		w.Logger.Info("web_fetch.done", "host", target.Hostname(), "text_chars", len([]rune(text)))
	}
	result, _ := json.Marshal(map[string]string{"url": resp.Request.URL.String(), "retrieved_at": time.Now().UTC().Format(time.RFC3339), "text": text})
	return string(result)
}

func validPageURL(u *url.URL) bool {
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return false
	}
	if p := u.Port(); p != "" && p != "80" && p != "443" {
		return false
	}
	return true
}

func publicIP(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range []string{"0.0.0.0/8", "240.0.0.0/4", "::/96", "64:ff9b:1::/48", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "2001:db8::/32", "64:ff9b::/96"} {
		if netip.MustParsePrefix(prefix).Contains(a) {
			return false
		}
	}
	return true
}
func publicDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no public address")
	}
	for _, ip := range ips {
		if !publicIP(ip) {
			return nil, fmt.Errorf("nonpublic address blocked")
		}
	}
	var last error
	for _, ip := range ips {
		conn, e := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if e == nil {
			return conn, nil
		}
		last = e
	}
	return nil, last
}

var activeHTML = regexp.MustCompile(`(?is)<(?:script|style|noscript)\b[^>]*>.*?</(?:script|style|noscript)\s*>`)
var htmlTags = regexp.MustCompile(`(?s)<[^>]+>`)

func pageText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(htmlTags.ReplaceAllString(activeHTML.ReplaceAllString(s, " "), " "))), " ")
}
