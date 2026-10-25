package canary

import (
	"encoding/json"
	"encoding/xml"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStatusPresentationStrictInputs(t *testing.T) {
	input := statusFixture("public")
	input.Appearance = &StatusPageAppearance{Theme: "dark", AccentColor: "#aAbB09", LogoURL: "https://images.example.invalid/logo.png", FooterText: "Public footer", RefreshIntervalMs: 10000, Markdown: true}
	input.PublicURL, input.RSSEnabled = "https://status.example.invalid/#/status/public", true
	raw, _ := json.Marshal(input)
	if _, err := parseStatusPage(raw); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*StatusPageInput){
		func(p *StatusPageInput) { p.Appearance.Theme = "auto" },
		func(p *StatusPageInput) { p.Appearance.AccentColor = "red;url(secret)" },
		func(p *StatusPageInput) { p.Appearance.RefreshIntervalMs = 9999 },
		func(p *StatusPageInput) { p.Appearance.RefreshIntervalMs = 300001 },
		func(p *StatusPageInput) { p.Appearance.LogoURL = "data:image/svg+xml,script" },
		func(p *StatusPageInput) { p.Appearance.LogoURL = "https://user:secret@images.invalid/logo" },
		func(p *StatusPageInput) { p.Appearance.LogoURL += "?secret=value" },
		func(p *StatusPageInput) { p.PublicURL = "javascript:alert(1)" },
		func(p *StatusPageInput) { p.PublicURL = "" },
		func(p *StatusPageInput) { p.Appearance.FooterText = "line\x00break" },
	} {
		var candidate StatusPageInput
		_ = json.Unmarshal(raw, &candidate)
		change(&candidate)
		data, _ := json.Marshal(candidate)
		if _, err := parseStatusPage(data); err == nil {
			t.Fatal("malformed public presentation admitted")
		}
	}
	for _, candidate := range []string{
		strings.Replace(string(raw), `"theme":"dark"`, `"theme":"dark","Theme":"light"`, 1),
		strings.Replace(string(raw), `"theme":"dark"`, `"theme":"dark","theme":"dark"`, 1),
		strings.Replace(string(raw), `"theme":"dark"`, `"theme":null`, 1),
		strings.Replace(string(raw), `"theme":"dark"`, `"css":"body{}"`, 1),
		strings.Replace(string(raw), `"rssEnabled":true`, `"rssEnabled":null`, 1),
	} {
		if _, err := parseStatusPage([]byte(candidate)); err == nil {
			t.Fatal("ambiguous or executable presentation accepted")
		}
	}
}

func TestStatusRSSIsExplicitPublicAnnouncementOnly(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Now().UnixMilli())
	node := statusTestNode(t, &clock, ":memory:")
	input := statusFixture("operations")
	input.Groups = []StatusPageGroup{}
	input.PublicURL, input.RSSEnabled = "https://status.example.invalid/#/status/operations", true
	input.Announcements = []StatusPageAnnouncement{{ID: "public-note", Title: "Published note", Message: `<img src=x onerror=alert(1)> & literal`, State: "investigating", CreatedAt: clock.Load() - 2000, UpdatedAt: clock.Load() - 1000}}
	created := statusCreate(t, node, input, adminToken)
	response := maintenanceAPIRequest(node, "GET", publicStatusPrefix+input.Slug+"/rss.xml", nil, "")
	if response.Code != 200 || response.Header().Get("Content-Type") != "application/rss+xml; charset=utf-8" {
		t.Fatalf("feed failed: %d %s", response.Code, response.Body.String())
	}
	var feed statusRSS
	if xml.Unmarshal(response.Body.Bytes(), &feed) != nil || len(feed.Channel.Items) != 1 || feed.Channel.Items[0].GUID.Value != "canary-status:operations:public-note" || feed.Channel.Link != input.PublicURL {
		t.Fatal("feed lost explicit authored content")
	}
	if strings.Contains(feed.Channel.Items[0].Description, "<img") || !strings.Contains(feed.Channel.Items[0].Description, "&lt;img") {
		t.Fatal("feed exposed executable description markup")
	}
	for _, private := range []string{created.ID, created.Revision, adminToken, clusterToken, "selected-monitor", "127.0.0.1"} {
		if strings.Contains(response.Body.String(), private) {
			t.Fatal("feed included private metadata")
		}
	}
	head := maintenanceAPIRequest(node, "HEAD", publicStatusPrefix+input.Slug+"/rss.xml", nil, "")
	if head.Code != 200 || head.Body.Len() != 0 {
		t.Fatal("feed HEAD returned a body")
	}
	for _, path := range []string{publicStatusPrefix + input.Slug + "/rss.xml?x=1", publicStatusPrefix + "missing/rss.xml", publicStatusPrefix + input.Slug + "/rss.xml/"} {
		if got := maintenanceAPIRequest(node, "GET", path, nil, ""); got.Code != 404 {
			t.Fatalf("unexpected feed route admitted: %d", got.Code)
		}
	}
	input.RSSEnabled = false
	changed := maintenanceAPIRequest(node, "PUT", "/api/status-pages/"+created.ID, statusEdit(input, created.Revision, nil), adminToken)
	if changed.Code != 200 {
		t.Fatal(changed.Body.String())
	}
	if response := maintenanceAPIRequest(node, "GET", publicStatusPrefix+input.Slug+"/rss.xml", nil, ""); response.Code != 404 {
		t.Fatal("disabled feed remained public")
	}
	if _, response := statusPublic(t, node, input.Slug); response.Code != 200 {
		t.Fatal("turning RSS off disabled the page")
	}
}
