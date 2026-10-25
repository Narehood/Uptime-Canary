package canary

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"html"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"uptime-canary/apps/node-go/internal/jsonshape"
)

func validateStatusPresentation(fields map[string]json.RawMessage, in *StatusPageInput) error {
	bad := fail(400, "Invalid status page presentation")
	for _, name := range []string{"appearance", "publicUrl", "rssEnabled"} {
		if bytes.Equal(bytes.TrimSpace(fields[name]), []byte("null")) {
			return bad
		}
	}
	if in.PublicURL != "" {
		if len(in.PublicURL) > 2048 || !statusText(in.PublicURL, 2048, false, false) {
			return bad
		}
		parsed, err := url.Parse(in.PublicURL)
		if err != nil || parsed.User != nil || parsed.Hostname() == "" || parsed.Opaque != "" || parsed.Scheme != "https" && parsed.Scheme != "http" || strings.ContainsAny(in.PublicURL, "\\\r\n\t") {
			return bad
		}
		// It is an explicit public link, including a dashboard's registration
		// selector or hash route. It is never fetched by the node.
	}
	if in.RSSEnabled && in.PublicURL == "" {
		return bad
	}
	if in.Appearance == nil {
		return nil
	}
	if jsonshape.Validate(fields["appearance"]) != nil {
		return bad
	}
	object, err := statusObject(fields["appearance"], "theme", "accentColor", "logoUrl", "footerText", "refreshIntervalMs", "markdown")
	if err != nil {
		return bad
	}
	for _, raw := range object {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return bad
		}
	}
	appearance := in.Appearance
	if !slices.Contains([]string{"", "system", "light", "dark"}, appearance.Theme) || !statusText(appearance.FooterText, 2048, true, true) ||
		appearance.RefreshIntervalMs != 0 && (appearance.RefreshIntervalMs < 10000 || appearance.RefreshIntervalMs > 300000) {
		return bad
	}
	if appearance.AccentColor != "" {
		if len(appearance.AccentColor) != 7 || appearance.AccentColor[0] != '#' {
			return bad
		}
		for _, ch := range appearance.AccentColor[1:] {
			if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f' || ch >= 'A' && ch <= 'F') {
				return bad
			}
		}
	}
	if appearance.LogoURL != "" {
		value, err := url.Parse(appearance.LogoURL)
		if err != nil || value.Scheme != "https" || value.Hostname() == "" || value.User != nil || value.Opaque != "" || value.RawQuery != "" || value.ForceQuery || value.Fragment != "" ||
			!statusText(appearance.LogoURL, 2048, false, false) || strings.Contains(appearance.LogoURL, "\\") {
			return bad
		}
	}
	return nil
}

type statusRSS struct {
	XMLName xml.Name         `xml:"rss"`
	Version string           `xml:"version,attr"`
	Channel statusRSSChannel `xml:"channel"`
}
type statusRSSChannel struct {
	Title       string          `xml:"title"`
	Link        string          `xml:"link"`
	Description string          `xml:"description"`
	Items       []statusRSSItem `xml:"item"`
}
type statusRSSItem struct {
	Title       string        `xml:"title"`
	Link        string        `xml:"link"`
	Description string        `xml:"description"`
	GUID        statusRSSGUID `xml:"guid"`
	PubDate     string        `xml:"pubDate"`
}
type statusRSSGUID struct {
	PermaLink bool   `xml:"isPermaLink,attr"`
	Value     string `xml:",chardata"`
}

func encodeStatusRSS(page *PublicStatusPage) ([]byte, error) {
	feed := statusRSS{Version: "2.0", Channel: statusRSSChannel{Title: page.Title, Link: page.PublicURL, Description: html.EscapeString(page.Description), Items: []statusRSSItem{}}}
	items := slices.Clone(page.Announcements)
	slices.SortFunc(items, func(a, b StatusPageAnnouncement) int {
		if a.UpdatedAt > b.UpdatedAt {
			return -1
		}
		if a.UpdatedAt < b.UpdatedAt {
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
	for _, note := range items {
		feed.Channel.Items = append(feed.Channel.Items, statusRSSItem{Title: note.Title, Link: page.PublicURL, Description: html.EscapeString(note.Message),
			GUID: statusRSSGUID{Value: "canary-status:" + page.Slug + ":" + note.ID}, PubDate: time.UnixMilli(note.UpdatedAt).UTC().Format(time.RFC1123Z)})
	}
	raw, err := xml.Marshal(feed)
	if err != nil || len(raw) > 256*1024 {
		return nil, fail(404, "Not found")
	}
	return append([]byte(xml.Header), raw...), nil
}

func (n *Node) serveStatusRSS(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, publicStatusPrefix) || !strings.HasSuffix(r.URL.Path, "/rss.xml") {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	slug := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, publicStatusPrefix), "/rss.xml")
	if (r.Method != "GET" && r.Method != "HEAD") || !statusSlug(slug) || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" {
		errorReply(w, fail(404, "Not found"))
		return true
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	n.mu.Lock()
	page := n.publicStatusPage(slug)
	n.mu.Unlock()
	if page == nil || !page.RSSEnabled || page.PublicURL == "" {
		errorReply(w, fail(404, "Not found"))
		return true
	}
	// No history query, private incidents, monitor IDs or maintenance names enter
	// this feed. Its only items are the page's authored public announcements.
	raw, err := encodeStatusRSS(page)
	if err != nil {
		errorReply(w, fail(404, "Not found"))
		return true
	}
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.WriteHeader(200)
	if r.Method != "HEAD" {
		_, _ = w.Write(raw)
	}
	return true
}
