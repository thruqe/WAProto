package fetcher

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	BaseURL    = "https://web.whatsapp.com"
	CDNURL     = "https://static.whatsapp.net"
	MaxWorkers = 16
)

// userAgents cycles through different UA strings to reduce bot-detection fingerprinting.
var userAgents = []string{
	"Mozilla/5.0 (X11; Linux x86_64; rv:128.0) Gecko/20100101 Firefox/128.0",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:127.0) Gecko/20100101 Firefox/127.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36",
}

var (
	// Script src / link href regexps — covers both web.whatsapp.com and static.whatsapp.net
	scriptSrcRe  = regexp.MustCompile(`<script[^>]+\bsrc=["']([^"']+)["']`)
	preloadHref  = regexp.MustCompile(`<link[^>]+\bhref=["']([^"']+\.m?js[^"']*)["']`)
	// In-text JS URL finder (same regex as fetch.js jsInTextRegex)
	jsInTextRe   = regexp.MustCompile(`(?:https?:)?//[^\s"'` + "`" + `<>]+?\.m?js(?:[?#][^\s"'` + "`" + `<>]*)?|(?:/|\./|\.\./)[^\s"'` + "`" + `<>]+?\.m?js(?:[?#][^\s"'` + "`" + `<>]*)?`)
	// btmanifest attribute carries the WA client revision: data-btmanifest="1046904178_main"
	btManifestRe = regexp.MustCompile(`data-btmanifest=["']([0-9]+)_`)
	// Version string inside bundle JS
	versionStrRe = regexp.MustCompile(`(?:appVersion:|VERSION_STR=)"([0-9.]+)"`)
	// serviceworker.js / sw.js patterns
	manifestRe   = regexp.MustCompile(`assets-manifest-([0-9.]+)\.json`)
	clientRevRe  = regexp.MustCompile(`client_revision\\?":\s*([0-9]+)`)
)

// BundleResult contains the fetched WhatsApp Web version and downloaded bundle sources.
type BundleResult struct {
	Version string
	URLs    []string
	Sources []string
}

// FetchBundles discovers and downloads all WhatsApp Web JavaScript bundles.
func FetchBundles(ctx context.Context, client *http.Client) (*BundleResult, error) {
	if client == nil {
		client = &http.Client{
			Timeout: 60 * time.Second,
		}
	}

	result := &BundleResult{}
	urlSet := make(map[string]bool)

	// ── Step 1: Fetch the main page HTML with proper document-navigation headers ──
	htmlBody, err := fetchHTML(ctx, client, BaseURL+"/")
	if err != nil {
		return nil, fmt.Errorf("failed fetching WhatsApp Web main page: %w", err)
	}

	// Extract version from btmanifest attribute (most reliable from HTML)
	if m := btManifestRe.FindSubmatch(htmlBody); len(m) > 1 {
		result.Version = fmt.Sprintf("2.3000.%s", string(m[1]))
	}

	// ── Step 2: Find all JS bundle URLs referenced in the HTML ──
	addURLsFromHTML(htmlBody, urlSet)

	// ── Step 3: Check serviceworker / sw.js for asset manifest and more bundles ──
	fetchServiceWorkerBundles(ctx, client, result, urlSet)

	// ── Step 4: Scan inline <script> text for additional JS URLs ──
	for _, match := range jsInTextRe.FindAll(htmlBody, -1) {
		u := resolveURL(string(match))
		if isAllowedBundle(u) {
			urlSet[u] = true
		}
	}

	if len(urlSet) == 0 {
		return nil, fmt.Errorf("no JavaScript bundle URLs discovered from %s", BaseURL)
	}

	var urls []string
	for u := range urlSet {
		urls = append(urls, u)
	}
	sort.Strings(urls)
	result.URLs = urls

	// ── Step 5: Download all bundles concurrently ──
	sources := make([]string, len(urls))
	var wg sync.WaitGroup
	errCh := make(chan error, len(urls))
	sem := make(chan struct{}, MaxWorkers)
	var versionOnce sync.Once

	for i, bundleURL := range urls {
		wg.Add(1)
		go func(idx int, targetURL string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			data, err := downloadWithRetry(ctx, client, targetURL, 3)
			if err != nil {
				errCh <- fmt.Errorf("failed downloading %s: %w", targetURL, err)
				return
			}
			sources[idx] = string(data)

			// Capture version from bundle JS as fallback
			if result.Version == "" {
				if vm := versionStrRe.FindSubmatch(data); len(vm) > 1 {
					versionOnce.Do(func() {
						result.Version = string(vm[1])
					})
				}
			}
		}(i, bundleURL)
	}

	wg.Wait()
	close(errCh)

	if len(errCh) > 0 {
		for e := range errCh {
			fmt.Printf("Warning: %v\n", e)
		}
	}

	result.Sources = sources
	return result, nil
}

// addURLsFromHTML extracts all JS bundle URLs from WhatsApp Web HTML.
// Bundles are now served from static.whatsapp.net via rsrc.php paths.
func addURLsFromHTML(html []byte, urlSet map[string]bool) {
	// <script src="...">
	for _, m := range scriptSrcRe.FindAllSubmatch(html, -1) {
		if len(m) > 1 {
			u := resolveURL(string(m[1]))
			if isAllowedBundle(u) {
				urlSet[u] = true
			}
		}
	}

	// <link rel="preload/modulepreload/prefetch" href="...">
	for _, m := range preloadHref.FindAllSubmatch(html, -1) {
		if len(m) > 1 {
			u := resolveURL(string(m[1]))
			if isAllowedBundle(u) {
				urlSet[u] = true
			}
		}
	}
}

// fetchServiceWorkerBundles checks sw.js / serviceworker.js for asset manifests.
func fetchServiceWorkerBundles(ctx context.Context, client *http.Client, result *BundleResult, urlSet map[string]bool) {
	for _, swPath := range []string{"/sw.js", "/serviceworker.js"} {
		swData, err := fetchScript(ctx, client, BaseURL+swPath)
		if err != nil || len(swData) == 0 {
			continue
		}

		// Extract version from client_revision
		if result.Version == "" {
			if m := clientRevRe.FindSubmatch(swData); len(m) > 1 {
				result.Version = fmt.Sprintf("2.3000.%s", string(m[1]))
			}
		}

		// Find assets-manifest-{ver}.json references
		for _, mm := range manifestRe.FindAllSubmatch(swData, -1) {
			if len(mm) < 2 {
				continue
			}
			manifestURL := fmt.Sprintf("%s/assets-manifest-%s.json", BaseURL, string(mm[1]))
			fetchManifestBundles(ctx, client, manifestURL, urlSet)
		}

		// Also scan sw.js text for inline JS URLs
		for _, match := range jsInTextRe.FindAll(swData, -1) {
			u := resolveURL(string(match))
			if isAllowedBundle(u) {
				urlSet[u] = true
			}
		}
	}
}

// fetchManifestBundles fetches an assets-manifest JSON and adds all .js entries.
func fetchManifestBundles(ctx context.Context, client *http.Client, manifestURL string, urlSet map[string]bool) {
	data, err := fetchScript(ctx, client, manifestURL)
	if err != nil || len(data) == 0 {
		return
	}
	// Quick string scan — avoid full JSON parsing for speed
	jsPathRe := regexp.MustCompile(`"([^"]+\.m?js)"`)
	for _, m := range jsPathRe.FindAllSubmatch(data, -1) {
		if len(m) > 1 {
			raw := string(m[1])
			// Manifest keys are bare paths like "app.abc123.js" or full URLs
			var u string
			if strings.HasPrefix(raw, "http") {
				u = raw
			} else if strings.HasPrefix(raw, "/") {
				u = BaseURL + raw
			} else {
				u = BaseURL + "/" + raw
			}
			if isAllowedBundle(u) {
				urlSet[u] = true
			}
		}
	}
}

// fetchHTML performs an HTTP GET with browser document-navigation headers (used for main HTML page).
func fetchHTML(ctx context.Context, client *http.Client, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	setDocumentHeaders(req)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, rawURL)
	}

	return io.ReadAll(resp.Body)
}

// fetchScript performs an HTTP GET with browser script-fetch headers.
func fetchScript(ctx context.Context, client *http.Client, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	setScriptHeaders(req)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, rawURL)
	}

	return io.ReadAll(resp.Body)
}

// setDocumentHeaders sets headers that mimic a real browser navigating to a page.
func setDocumentHeaders(req *http.Request) {
	req.Header.Set("User-Agent", userAgents[0])
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
}

// setScriptHeaders sets headers that mimic a browser fetching a script resource.
func setScriptHeaders(req *http.Request) {
	req.Header.Set("User-Agent", userAgents[0])
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Sec-Fetch-Dest", "script")
	req.Header.Set("Sec-Fetch-Mode", "no-cors")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Referer", BaseURL+"/")
}

func resolveURL(raw string) string {
	raw = strings.TrimSpace(raw)
	// Escaped slashes (from JSON-within-HTML)
	raw = strings.ReplaceAll(raw, `\/`, "/")
	if strings.HasPrefix(raw, "//") {
		return "https:" + raw
	}
	if strings.HasPrefix(raw, "/") {
		// Could be web.whatsapp.com or static.whatsapp.net — default to BaseURL
		return BaseURL + raw
	}
	return raw
}

// isAllowedBundle returns true if the URL points to a WhatsApp JS bundle.
// Bundles are served from web.whatsapp.com and static.whatsapp.net.
func isAllowedBundle(rawURL string) bool {
	if rawURL == "" {
		return false
	}
	// Skip data: URIs
	if strings.HasPrefix(rawURL, "data:") {
		return false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	allowed := host == "static.whatsapp.net" || strings.HasSuffix(host, ".whatsapp.net")
	if !allowed {
		return false
	}
	// Must end with .js or .mjs (ignoring query/fragment)
	path := parsed.Path
	return strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".mjs")
}

func downloadWithRetry(ctx context.Context, client *http.Client, rawURL string, retries int) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= retries; attempt++ {
		data, err := fetchScript(ctx, client, rawURL)
		if err == nil && len(data) > 0 {
			return data, nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("empty response")
		}
		time.Sleep(time.Duration(attempt*300) * time.Millisecond)
	}
	return nil, lastErr
}
