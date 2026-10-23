package canary

import (
	"encoding/xml"
	"io"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPublicBadgesUseOnlyPublishedProjection(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	n := statusTestNode(t, &clock, ":memory:")
	in := input([]string{"a", "b"}, "http://private-badge-target.invalid/credential", "")
	in.Name = "private-badge-monitor"
	m, err := n.writeMonitor(in, nil, "selected-monitor", false)
	if err != nil {
		t.Fatal(err)
	}
	page := statusCreate(t, n, statusFixture("operations"), adminToken)
	base := publicBadgePrefix + page.Slug + ".svg"
	selection := base + "?" + url.Values{"group": {"public-services"}, "monitor": {"Public service"}}.Encode()
	set := func(state string) {
		for _, id := range []string{"a", "b"} {
			n.samples[m.ID+"/"+id] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: id, State: state, CheckedAt: clock.Load(), Message: "private-badge-message"}, Revision: m.Revision}
		}
	}
	for _, state := range []string{"up", "down", "pending", "unknown"} {
		set(state)
		for _, path := range []string{base, selection} {
			w := maintenanceAPIRequest(n, "GET", path, nil, "")
			if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "image/svg+xml") || !strings.Contains(w.Body.String(), "status: "+state) {
				t.Fatal("badge disagreed with public state", state, w.Code, w.Body.String())
			}
			for _, private := range []string{m.ID, m.Name, m.Target, "private-badge-message", "<script", "http://private"} {
				if strings.Contains(w.Body.String(), private) {
					t.Fatal("badge exposed private data or an external reference")
				}
			}
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Access-Control-Allow-Origin") != "*" || w.Header().Get("Content-Security-Policy") == "" {
				t.Fatal("missing public SVG response policy")
			}
		}
	}
	set("down")
	storeMaintenance(t, n, plannedWork())
	if w := maintenanceAPIRequest(n, "GET", selection, nil, ""); !strings.Contains(w.Body.String(), "status: down, maintenance") {
		t.Fatal("maintenance hid the measured failure")
	}
	set("up")
	clock.Add(86400000)
	if w := maintenanceAPIRequest(n, "GET", selection, nil, ""); !strings.Contains(w.Body.String(), "status: unknown") {
		t.Fatal("stale samples became healthy badge")
	}
	if w := maintenanceAPIRequest(n, "HEAD", selection, nil, ""); w.Code != 200 || w.Body.Len() != 0 || !strings.HasPrefix(w.Header().Get("Content-Type"), "image/svg+xml") {
		t.Fatal("HEAD badge has wrong headers or a body")
	}
	// A duplicated public name is ambiguous; neither private ID nor list order
	// is allowed to select a different monitor on the caller's behalf.
	page.Groups[0].Monitors = append(page.Groups[0].Monitors, StatusPageMonitor{MonitorID: "another-private-monitor", Name: "Public service"})
	w := maintenanceAPIRequest(n, "PUT", "/api/status-pages/"+page.ID, statusEdit(page.StatusPageInput, page.Revision, nil), adminToken)
	if w.Code != 200 {
		t.Fatal("public duplicate-name fixture", w.Code, w.Body.String())
	}
	if w = maintenanceAPIRequest(n, "GET", selection, nil, ""); w.Code != 404 {
		t.Fatal("ambiguous public selection was published")
	}
}

func TestPublicBadgePublicationAndWorkBudgetGates(t *testing.T) {
	for _, testCase := range []string{"disabled", "deleted", "conflicted", "incomplete proof", "head budget"} {
		t.Run(testCase, func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(maintenanceClock(12, 1))
			n := statusTestNode(t, &clock, ":memory:")
			page := statusCreate(t, n, statusFixture("operations"), adminToken)
			key := resourceKey(resourceStatusPage, page.ID)
			switch testCase {
			case "disabled":
				page.Enabled = false
				if w := maintenanceAPIRequest(n, "PUT", "/api/status-pages/"+page.ID, statusEdit(page.StatusPageInput, page.Revision, nil), adminToken); w.Code != 200 {
					t.Fatal("disable fixture", w.Code)
				}
			case "deleted":
				if w := maintenanceAPIRequest(n, "DELETE", "/api/status-pages/"+page.ID, map[string]string{"revision": page.Revision}, adminToken); w.Code != 204 {
					t.Fatal("delete fixture", w.Code)
				}
			case "conflicted":
				n.resources[key] = append(n.resources[key], n.resources[key][0])
			case "incomplete proof":
				n.resources[key][0].EdgeID = "missing-proof"
			case "head budget":
				n.resources[resourceKey(resourceChannel, "large-conflict")] = make([]ResourceVersion, publicStatusHeadBudget)
			}
			before := len(n.resources)
			w := maintenanceAPIRequest(n, "GET", publicBadgePrefix+page.Slug+".svg", nil, adminToken)
			if w.Code != 404 || w.Body.String() != `{"error":"Not found"}` || len(n.resources) != before {
				t.Fatal("unpublished or over-budget page acquired badge authority", w.Code)
			}
		})
	}
}

func TestPublicBadgeRoutesFailClosedWithoutReflection(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	n := statusTestNode(t, &clock, ":memory:")
	statusCreate(t, n, statusFixture("operations"), adminToken)
	base := publicBadgePrefix + "operations.svg"
	paths := []string{
		"/public/badge", "/public/badge/operations", "/public/badge/operations.svg/extra",
		"/public/badge/missing.svg", "/public/badge/Operations.svg", base + "?",
		base + "?group=public-services", base + "?monitor=Public+service",
		base + "?group=public-services&monitor=private-badge-monitor",
		base + "?group=public-services&monitor=Public+service&monitor=Public+service",
		base + "?group=public-services&monitor=Public+service&private=secret",
		base + "?group=public-services;private=secret&monitor=Public+service",
		base + "?group=public-services&monitor=%ZZ", base + "?" + strings.Repeat("x", 4097),
	}
	for _, path := range paths {
		w := maintenanceAPIRequest(n, "GET", path, nil, adminToken)
		if w.Code != 404 || w.Body.String() != `{"error":"Not found"}` {
			t.Fatal("badge request reflected input or escaped its route", path, w.Code, w.Body.String())
		}
	}
	for _, method := range []string{"POST", "PUT", "OPTIONS", "DELETE"} {
		w := maintenanceAPIRequest(n, method, base, map[string]string{"private": "secret"}, adminToken)
		if w.Code != 404 || w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("badge route granted a write or management CORS")
		}
	}
	r := httptest.NewRequest("GET", base, nil)
	r.URL.RawPath = "/public/badge/%6fperations.svg"
	w := httptest.NewRecorder()
	n.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal("encoded path alias published a badge")
	}
}

func TestStatusBadgeSVGUsesFixedAccessibleDesign(t *testing.T) {
	for _, state := range []string{"up", "down", "pending", "unknown", "<script>alert(1)</script>"} {
		for _, maintenance := range []bool{false, true} {
			body := renderStatusBadge(state, maintenance)
			if strings.Contains(body, "<script") || !strings.Contains(body, `role="img"`) || !strings.Contains(body, `<title id="title">status:`) {
				t.Fatal("unbounded or inaccessible SVG")
			}
			if strings.Contains(body, ", maintenance") != maintenance || strings.Contains(body, "#fab219") != maintenance {
				t.Fatal("maintenance replaced or concealed measured state")
			}
			decoder := xml.NewDecoder(strings.NewReader(body))
			for {
				token, err := decoder.Token()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal("invalid SVG", err)
				}
				if element, ok := token.(xml.StartElement); ok {
					for _, attr := range element.Attr {
						if (attr.Name.Local == "href" || attr.Name.Local == "src") && !strings.HasPrefix(attr.Value, "#") {
							t.Fatal("external SVG reference")
						}
						if strings.Contains(attr.Value, "url(") && !strings.Contains(attr.Value, "url(#") {
							t.Fatal("external SVG paint reference")
						}
					}
				}
			}
		}
	}
}
