package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"uptime-canary/apps/node-go/internal/notifyhttp"
)

func TestFullReviewEmailAcknowledgements(t *testing.T) {
	type receipt struct {
		name, body string
		want       Status
	}
	for _, kind := range []Kind{Mailgun, Brevo, Mandrill} {
		status, field, escaped := 200, "id", `\u0069d`
		if kind == Brevo {
			status, field, escaped = 201, "messageId", `\u006dessageId`
		}
		cases := []receipt{
			{"valid-with-metadata", `{"` + field + `":"fixture-ack","extra":{"version":1}}`, Accepted},
			{"escaped-canonical-key", `{"` + escaped + `":"fixture-ack"}`, Accepted},
			{"duplicate-id", `{"` + field + `":"","` + field + `":"fixture-ack"}`, Unknown},
			{"escaped-duplicate-id", `{"` + field + `":"","` + escaped + `":"fixture-ack"}`, Unknown},
			{"case-only-id", `{"` + strings.ToUpper(field) + `":"fixture-ack"}`, Unknown},
			{"case-conflicting-id", `{"` + field + `":"","` + strings.ToUpper(field) + `":"fixture-ack"}`, Unknown},
			{"invalid-utf8-id", `{"` + field + `":"fixture` + string([]byte{0xff}) + `"}`, Unknown},
			{"maximum-id", `{"` + field + `":"` + strings.Repeat("a", 1024) + `"}`, Accepted},
			{"overlimit-id", `{"` + field + `":"` + strings.Repeat("a", 1025) + `"}`, Unknown},
			{"overlimit-body", `{"` + field + `":"fixture-ack"}` + strings.Repeat(" ", MaxResponseBytes), Unknown},
		}
		if kind == Mandrill {
			cases = []receipt{
				{"valid-with-metadata", `{"email":"first@example.test","status":"sent","_id":"fixture-ack","extra":{"version":1}}`, Accepted},
				{"valid-rejected", `{"email":"first@example.test","status":"rejected","_id":null}`, Rejected},
				{"escaped-canonical-status", `{"email":"first@example.test","\u0073tatus":"queued","_id":"fixture-ack"}`, Accepted},
				{"contradictory-status", `{"email":"first@example.test","status":"rejected","status":"sent","_id":"fixture-ack"}`, Unknown},
				{"escaped-duplicate-status", `{"email":"first@example.test","status":"rejected","\u0073tatus":"sent","_id":"fixture-ack"}`, Unknown},
				{"case-only-status", `{"email":"first@example.test","Status":"sent","_id":"fixture-ack"}`, Unknown},
				{"case-conflicting-status", `{"email":"first@example.test","status":"rejected","STATUS":"sent","_id":"fixture-ack"}`, Unknown},
				{"duplicate-email", `{"email":"other@example.test","email":"first@example.test","status":"sent","_id":"fixture-ack"}`, Unknown},
				{"escaped-duplicate-email", `{"email":"other@example.test","\u0065mail":"first@example.test","status":"sent","_id":"fixture-ack"}`, Unknown},
				{"case-only-email", `{"Email":"first@example.test","status":"sent","_id":"fixture-ack"}`, Unknown},
				{"duplicate-id", `{"email":"first@example.test","status":"sent","_id":"","_id":"fixture-ack"}`, Unknown},
				{"escaped-duplicate-id", `{"email":"first@example.test","status":"sent","_id":"","\u005fid":"fixture-ack"}`, Unknown},
				{"case-only-id", `{"email":"first@example.test","status":"sent","_ID":"fixture-ack"}`, Unknown},
				{"invalid-utf8-id", `{"email":"first@example.test","status":"sent","_id":"fixture` + string([]byte{0xff}) + `"}`, Unknown},
				{"maximum-id", `{"email":"first@example.test","status":"sent","_id":"` + strings.Repeat("a", 1024) + `"}`, Accepted},
				{"overlimit-id", `{"email":"first@example.test","status":"sent","_id":"` + strings.Repeat("a", 1025) + `"}`, Unknown},
			}
			for i := range cases {
				cases[i].body = "[" + cases[i].body + "]"
			}
		}
		for _, tc := range cases {
			t.Run(string(kind)+"/"+tc.name, func(t *testing.T) {
				calls := 0
				client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					calls++ // An in-memory transport: no DNS or external request.
					return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
				})}
				c := fixtureConfig(kind, "https://example.test")
				c.Recipients = c.Recipients[:1]
				got := New(Options{HTTPClient: client}).Send(context.Background(), c, fixtureMessage())
				if calls != 1 || got.HTTPStatus != status || got.Status != tc.want || got.Accepted != (tc.want == Accepted) {
					t.Fatalf("receipt = %+v, calls = %d; want %s", got, calls, tc.want)
				}
				if tc.want == Accepted && (got.AcceptedRecipients != 1 || got.ProviderID == "") {
					t.Fatal("valid receipt lost acceptance or its hashed identity")
				}
				if tc.want == Unknown && (got.AcceptedRecipients != 0 || got.RejectedRecipients != 0 || got.ProviderID != "" || len(got.Recipients) != 0) {
					t.Fatalf("ambiguous receipt gained recipient authority: %+v", got)
				}
			})
		}
	}
}

func TestFullReviewMessagingConfigEscapedRoundTrip(t *testing.T) {
	for _, char := range []string{"a", "&"} {
		c := Config{Kind: Ntfy, Ntfy: &notifyhttp.NtfyConfig{Topic: "fixture", Username: "fixture",
			Password: strings.Repeat(char, 512), BaseURL: "https://example.test/" + strings.Repeat(char, 1800)}}
		for i := 0; i < 16; i++ {
			c.Ntfy.Tags = append(c.Ntfy.Tags, strings.Repeat(char, 64))
		}
		if err := Validate(c); err != nil {
			t.Fatalf("bounded input rejected before round trip: %v", err)
		}
		for _, escape := range []bool{true, false} {
			t.Run(char+map[bool]string{true: "/marshal", false: "/unescaped-input"}[escape], func(t *testing.T) {
				var data []byte
				var err error
				if escape {
					data, err = json.Marshal(c)
				} else {
					var buffer bytes.Buffer
					encoder := json.NewEncoder(&buffer)
					encoder.SetEscapeHTML(false)
					err = encoder.Encode(c)
					data = buffer.Bytes()
				}
				if err != nil {
					t.Fatal(err)
				}
				var got Config
				if err := json.Unmarshal(data, &got); err != nil {
					t.Fatalf("valid delivery envelope rejected (%d bytes): %v", len(data), err)
				}
				if !reflect.DeepEqual(got, c) || Validate(got) != nil {
					t.Fatal("delivery round trip changed bounded provider fields")
				}
			})
		}
	}
}
