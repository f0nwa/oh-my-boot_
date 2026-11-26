package control

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

const (
	urlDownloadMaxRedirects = 10
	urlDownloadUserAgent    = "OhMyBoot-ImageDownloader/1.0"
)

var (
	errInvalidDownloadURL = errors.New("invalid download URL")
	errBlockedDownloadIP  = errors.New("target address is blocked")
)

// URLProbeResult contains metadata returned by ProbeURL.
type URLProbeResult struct {
	FinalURL    string `json:"final_url"`
	Host        string `json:"host"`
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	StatusCode  int    `json:"status_code"`
}

// ValidateDownloadURL checks that raw is a valid HTTP or HTTPS URL with a host.
func ValidateDownloadURL(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > 2048 {
		return nil, errInvalidDownloadURL
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, errInvalidDownloadURL
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, errInvalidDownloadURL
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return nil, errInvalidDownloadURL
	}
	return parsed, nil
}

// redactDownloadURL strips userinfo credentials from a URL for display/logging.
func redactDownloadURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	clone := *u
	clone.User = nil
	return clone.String()
}

// IsBlockedDownloadIP blocks loopback, link-local (including cloud metadata
// 169.254.169.254), multicast and unspecified addresses while allowing public
// internet hosts and RFC1918/ULA LAN HTTP mirrors.
func IsBlockedDownloadIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	return ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() ||
		ip.IsMulticast()
}

// NewURLDownloadHTTPClient creates an HTTP client that checks every resolved IP
// against isBlocked (defaults to IsBlockedDownloadIP) on initial connection and
// every redirect.
func NewURLDownloadHTTPClient(insecureTLS bool, timeout time.Duration, isBlocked func(net.IP) bool) *http.Client {
	if isBlocked == nil {
		isBlocked = IsBlockedDownloadIP
	}
	dialer := &net.Dialer{
		Timeout:   15 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: 30 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		IdleConnTimeout:       60 * time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("no IP addresses for %s", host)
			}
			var allowed []net.IPAddr
			for _, ipAddr := range ips {
				if isBlocked(ipAddr.IP) {
					return nil, fmt.Errorf("%w: %s", errBlockedDownloadIP, ipAddr.IP.String())
				}
				allowed = append(allowed, ipAddr)
			}
			var lastErr error
			for _, ipAddr := range allowed {
				target := net.JoinHostPort(ipAddr.IP.String(), port)
				conn, err := dialer.DialContext(ctx, network, target)
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
	}
	if insecureTLS {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= urlDownloadMaxRedirects {
				return fmt.Errorf("too many redirects")
			}
			scheme := strings.ToLower(req.URL.Scheme)
			if scheme != "http" && scheme != "https" {
				return errInvalidDownloadURL
			}
			req.Header.Set("User-Agent", urlDownloadUserAgent)
			return nil
		},
	}
}

// ExtractFilenameFromResponse extracts a safe base filename from
// Content-Disposition or the final URL path.
func ExtractFilenameFromResponse(resp *http.Response, fallbackURL *url.URL) string {
	if resp != nil {
		if cd := strings.TrimSpace(resp.Header.Get("Content-Disposition")); cd != "" {
			if _, params, err := mime.ParseMediaType(cd); err == nil {
				if name := sanitizeCandidateFilename(params["filename"]); name != "" {
					return name
				}
			}
		}
		if resp.Request != nil && resp.Request.URL != nil {
			if name := filenameFromURL(resp.Request.URL); name != "" {
				return name
			}
		}
	}
	return filenameFromURL(fallbackURL)
}

func filenameFromURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	cleanPath := strings.TrimRight(u.Path, "/")
	if cleanPath == "" {
		return ""
	}
	base := path.Base(cleanPath)
	if decoded, err := url.PathUnescape(base); err == nil {
		base = decoded
	}
	return sanitizeCandidateFilename(base)
}

func sanitizeCandidateFilename(raw string) string {
	name := strings.TrimSpace(raw)
	if name == "" {
		return ""
	}
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == "/" || name == ".." {
		return ""
	}
	return name
}

// parseContentRangeTotal extracts total length from "bytes 0-0/12345".
func parseContentRangeTotal(header string) int64 {
	slash := strings.LastIndexByte(header, '/')
	if slash < 0 || slash+1 >= len(header) {
		return -1
	}
	totalStr := strings.TrimSpace(header[slash+1:])
	if totalStr == "" || totalStr == "*" {
		return -1
	}
	n, err := strconv.ParseInt(totalStr, 10, 64)
	if err != nil || n <= 0 {
		return -1
	}
	return n
}

// ProbeURL performs a HEAD request (with GET Range: bytes=0-0 fallback on
// 405/501/403) to inspect filename, size and reachability.
func ProbeURL(ctx context.Context, client *http.Client, rawURL string) (URLProbeResult, error) {
	parsed, err := ValidateDownloadURL(rawURL)
	if err != nil {
		return URLProbeResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, parsed.String(), nil)
	if err != nil {
		return URLProbeResult{}, err
	}
	req.Header.Set("User-Agent", urlDownloadUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return URLProbeResult{}, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()

	if resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusNotImplemented || resp.StatusCode == http.StatusForbidden {
		getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if err != nil {
			return URLProbeResult{}, err
		}
		getReq.Header.Set("User-Agent", urlDownloadUserAgent)
		getReq.Header.Set("Range", "bytes=0-0")
		getResp, err := client.Do(getReq)
		if err != nil {
			return URLProbeResult{}, err
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(getResp.Body, 4096))
		getResp.Body.Close()
		resp = getResp
	}

	finalURL := parsed
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL
	}
	size := resp.ContentLength
	if resp.StatusCode == http.StatusPartialContent {
		if total := parseContentRangeTotal(resp.Header.Get("Content-Range")); total > 0 {
			size = total
		}
	}
	ct := resp.Header.Get("Content-Type")
	if mediaType, _, err := mime.ParseMediaType(ct); err == nil {
		ct = mediaType
	}
	return URLProbeResult{
		FinalURL:    redactDownloadURL(finalURL),
		Host:        finalURL.Hostname(),
		Filename:    ExtractFilenameFromResponse(resp, finalURL),
		Size:        size,
		ContentType: ct,
		StatusCode:  resp.StatusCode,
	}, nil
}
