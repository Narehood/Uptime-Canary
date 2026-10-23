package jsonquery

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"uptime-canary/apps/node-go/internal/probeworker/process"
)

// Every engine test self-execs through the production private worker protocol.
// No JSONata compilation/evaluation runs in the test controller process.
func TestMain(m *testing.M) {
	if handled, code := process.Dispatch(os.Args[1:], WorkerHandler()); handled {
		os.Exit(code)
	}
	processCode := m.Run()
	os.Exit(processCode)
}

func supported(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("worker resource controls unavailable on this platform")
	}
}

func request(expression string, expected string) Request {
	return Request{Type: Type, Timeout: 4 * time.Second,
		Query:    Config{Expression: expression, Operator: Equal, Expected: json.RawMessage(expected)},
		Document: json.RawMessage(`{}`)}
}

type fixture struct {
	Name       string          `json:"name"`
	Expression string          `json:"expression"`
	Document   json.RawMessage `json:"document"`
	Operator   Operator        `json:"operator"`
	Expected   json.RawMessage `json:"expected"`
	Reference  struct {
		State string `json:"state"`
	} `json:"reference"`
	GoState      string `json:"goState,omitempty"`
	Gap          string `json:"gap,omitempty"`
	Policy       string `json:"policy,omitempty"`
	PolicyReason string `json:"policyReason,omitempty"`
	Regression   string `json:"regression,omitempty"`
}

func TestOfficialReferenceFixtures(t *testing.T) {
	runReferenceFixtures(t, "testdata/fixtures.json", 76)
}

func TestNumericReferenceFixtures(t *testing.T) {
	runReferenceFixtures(t, "testdata/number-fixtures.json", 101)
}

func runReferenceFixtures(t *testing.T, path string, minimum int) {
	t.Helper()
	supported(t)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []fixture
	if err = json.Unmarshal(b, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) < minimum {
		t.Fatal("reference fixture coverage missing")
	}
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			r := request(f.Expression, string(f.Expected))
			r.Document, r.Query.Operator = f.Document, f.Operator
			if err := r.Validate(); err != nil {
				t.Fatal(err)
			}
			got := Check(context.Background(), r)
			want := f.Reference.State
			if f.Policy != "" {
				if f.Policy != "fail-closed-nonfinite" || f.GoState != "down" || f.PolicyReason == "" || f.Gap != "" {
					t.Fatal("invalid fail-closed policy record")
				}
				want = "down"
				t.Logf("deliberate fail-closed policy: %s", f.PolicyReason)
				if got.Message != "service check failed" {
					t.Fatalf("expected a bounded evaluation error: %+v", got)
				}
			} else if f.GoState != "" {
				if f.Gap == "" || f.GoState != "down" || f.GoState == want {
					t.Fatal("invalid compatibility gap record")
				}
				want = f.GoState
				t.Logf("documented reference difference: %s", f.Gap)
			}
			if got.State != want {
				t.Fatalf("state=%s want=%s, fixed message=%q", got.State, want, got.Message)
			}
			if got.LatencyMs <= 0 {
				t.Fatal("worker did not execute")
			}
		})
	}
}

func TestNonfiniteFailClosedRegressions(t *testing.T) {
	supported(t)
	b, err := os.ReadFile("testdata/number-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []fixture
	if json.Unmarshal(b, &fixtures) != nil {
		t.Fatal("invalid numeric fixtures")
	}
	original := map[string]bool{
		"base overflow infinity":      false,
		"infinity number passthrough": false,
		"infinity builtin type":       false,
		"infinity passthrough type":   false,
		"NaN type classification":     false,
	}
	for _, f := range fixtures {
		if f.Regression != "original-downstream-nonfinite" {
			continue
		}
		seen, known := original[f.Name]
		if !known || seen || f.Policy != "fail-closed-nonfinite" || f.GoState != "down" {
			t.Fatal("original nonfinite regressions changed")
		}
		original[f.Name] = true
		t.Run(f.Name, func(t *testing.T) {
			r := request(f.Expression, string(f.Expected))
			r.Document, r.Query.Operator = f.Document, f.Operator
			got := Check(context.Background(), r)
			// Enforce safe DOWN independently of the golden outcome override.
			if got.State != "down" || got.Message != "service check failed" || got.LatencyMs <= 0 {
				t.Fatalf("nonfinite regression did not fail closed: %+v", got)
			}
		})
	}
	for name, seen := range original {
		if !seen {
			t.Fatalf("missing original regression: %s", name)
		}
	}
	for _, control := range []struct{ name, expression, expected string }{
		{"boolean", "$number(true)", "1"},
		{"finite native number", "$number($sum([1,2,3]))", "6"},
		{"lexical shadow", "($number := function($x){42}; $number('+2'))", "42"},
		{"nested eval", "$eval(\"$number('0x10000000000000000')\")", "18446744073709551616"},
	} {
		t.Run(control.name, func(t *testing.T) {
			if got := Check(context.Background(), request(control.expression, control.expected)); got.State != "up" {
				t.Fatalf("finite or lexical control failed: %+v", got)
			}
		})
	}
}

func TestValidationIsCheapAndDoesNotCompile(t *testing.T) {
	// Both malformed syntax and a runtime-error expression pass cheap validation.
	for _, expression := range []string{"(", "$error('private')", strings.Repeat("(", MaxExpressionBytes), "$"} {
		r := request(expression, `true`)
		if err := r.Validate(); err != nil {
			t.Fatalf("compiled during validation: %v", err)
		}
	}
	r := request("$", `true`)
	for _, mutate := range []func(*Request){
		func(r *Request) { r.Type = "http" },
		func(r *Request) { r.Timeout = 0 },
		func(r *Request) { r.Timeout = 5*time.Minute + 1 },
		func(r *Request) { r.Query.Expression = " " },
		func(r *Request) { r.Query.Expression = strings.Repeat("x", MaxExpressionBytes+1) },
		func(r *Request) { r.Query.Expression = string([]byte{0xff}) },
		func(r *Request) { r.Query.Operator = "=" },
		func(r *Request) { r.Query.Expected = nil },
		func(r *Request) { r.Query.Expected = json.RawMessage(`null`) },
		func(r *Request) { r.Query.Expected = json.RawMessage(`[]`) },
		func(r *Request) { r.Query.Expected = json.RawMessage(`{}`) },
		func(r *Request) { r.Query.Expected = json.RawMessage(`1e999`) },
		func(r *Request) { r.Query.Expected = json.RawMessage(`true false`) },
		func(r *Request) {
			r.Query.Expected = json.RawMessage(`"` + strings.Repeat("x", MaxExpectedBytes) + `"`)
		},
		func(r *Request) { r.Document = nil },
		func(r *Request) { r.Document = json.RawMessage(`{} {}`) },
		func(r *Request) { r.Document = json.RawMessage(strings.Repeat(" ", MaxDocumentBytes+1)) },
		func(r *Request) { r.Document = json.RawMessage{'"', 0xff, '"'} },
	} {
		bad := r
		mutate(&bad)
		if bad.Validate() == nil {
			t.Fatal("invalid data accepted")
		}
		if got := Check(context.Background(), bad); got.State != "down" || got.LatencyMs != 0 {
			t.Fatal("invalid request started a worker")
		}
	}
}

func TestMaximumDocumentThroughWorker(t *testing.T) {
	supported(t)
	r := request("$length($)", "1048574")
	r.Document = json.RawMessage(`"` + strings.Repeat("x", MaxDocumentBytes-2) + `"`)
	if len(r.Document) != MaxDocumentBytes {
		t.Fatal("bad boundary fixture")
	}
	got := Check(context.Background(), r)
	if got.State != "up" {
		t.Fatalf("1 MiB request failed: %+v", got)
	}
}

func TestCompilationAndExpectedBoundaries(t *testing.T) {
	supported(t)
	r := request("1"+strings.Repeat(" ", MaxExpressionBytes-1), "1")
	if got := Check(context.Background(), r); got.State != "up" {
		t.Fatalf("maximum expression rejected: %+v", got)
	}
	r = request("value", `"`+strings.Repeat("x", MaxExpectedBytes-2)+`"`)
	r.Document = append(append(json.RawMessage(`{"value":`), r.Query.Expected...), '}')
	if got := Check(context.Background(), r); got.State != "up" {
		t.Fatalf("maximum expected scalar rejected: %+v", got)
	}
	r = request(strings.Repeat("(", MaxExpressionBytes), "1")
	start := time.Now()
	if got := Check(context.Background(), r); got.State == "up" || time.Since(start) > 5*time.Second {
		t.Fatalf("malformed deeply nested compilation escaped controls: %+v", got)
	}
}

func TestAdvisoryEvaluationTimeout(t *testing.T) {
	supported(t)
	r := request("$reduce([1..10000], function($a,$v){$a+$sum([1..10000])}, 0)", "500050000000")
	start := time.Now()
	got := Check(context.Background(), r)
	elapsed := time.Since(start)
	if got.State != "down" || got.Message != "service check failed" || elapsed < MaxEvaluationTime-100*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("advisory timeout did not stop ordinary evaluation: %+v", got)
	}
}

func TestStrictWorkerDecode(t *testing.T) {
	supported(t)
	r := request("true", "true")
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range [][]byte{
		append(append([]byte(nil), b[:len(b)-1]...), []byte(`,"bindings":{"evil":"ignored"}}`)...),
		[]byte(`{"type":"json-query","timeout":1000000000,"query":{"expression":"true","operator":"==","expected":true,"native":"ignored"},"document":{}}`),
	} {
		got := process.Run(context.Background(), payload)
		if got.State != "down" || got.Message != "invalid service configuration" {
			t.Fatalf("unknown field accepted: %+v", got)
		}
	}
	// A failed Validate must clear a prior successful request in this handler.
	h := WorkerHandler()
	if h.Validate(b) != nil || h.Validate([]byte(`{}`)) == nil {
		t.Fatal("handler validation contract")
	}
	if got := h.Check(context.Background()); got.State != "down" {
		t.Fatal("stale handler request executed")
	}
}

func TestSensitiveErrorsStayPrivate(t *testing.T) {
	supported(t)
	t.Setenv("CANARY_ADMIN_TOKEN", "private-admin-token")
	for _, expression := range []string{
		"$error(secret)", "secret + 1", "$error('private-query-token')", "'private-syntax-token' (",
	} {
		r := request(expression, `"private-expected-token"`)
		r.Document = json.RawMessage(`{"secret":"private-body-token"}`)
		got := Check(context.Background(), r)
		if got.State != "down" || strings.Contains(got.Message, "private") || strings.Contains(got.Message, "token") {
			t.Fatalf("unsafe result: %+v", got)
		}
		if got.Message != "service check failed" {
			t.Fatalf("arbitrary diagnostic escaped: %+v", got)
		}
	}
}

func TestNumericConversionBoundsAndPrivacy(t *testing.T) {
	supported(t)
	for _, tt := range []struct {
		name, expression, expected, state string
	}{
		{"maximum string", "$number($pad('0',1048576,'0'))", "0", "up"},
		{"excessive computed string", "$number($pad('0',1048577,'0'))", "0", "down"},
		{"local shadow remains unrestricted by builtin shim", "($number := function($x){42}; $number($pad('0',1048577,'0')))", "42", "up"},
		{"final NaN cannot be JSON scalar", "$number('0x10z')", `"NaN"`, "down"},
		{"final infinity cannot be JSON scalar", "$number('0x' & $pad('f',300,'f'))", `"Infinity"`, "down"},
		{"credential argument", "$number(secret)", "0", "down"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := request(tt.expression, tt.expected)
			r.Document = json.RawMessage(`{"secret":"private-numeric-credential"}`)
			got := Check(context.Background(), r)
			if got.State != tt.state || strings.Contains(got.Message, "private") || strings.Contains(got.Message, "credential") {
				t.Fatalf("numeric conversion boundary: %+v", got)
			}
		})
	}
	// Per-expression native registration must not leak into another request,
	// including a request after an expression deliberately shadows $number.
	if got := Check(context.Background(), request("$number('+2')", "2")); got.State != "down" {
		t.Fatalf("shadow escaped its expression: %+v", got)
	}
}

func TestNoHostFunctionsOrBindings(t *testing.T) {
	supported(t)
	for _, expression := range []string{"$getenv('CANARY_ADMIN_TOKEN')", "$readFile('/etc/passwd')", "$fetch('http://127.0.0.1')", "$exec('whoami')", "$env('PATH')", "$bindings()"} {
		if got := Check(context.Background(), request(expression, `true`)); got.State != "down" {
			t.Fatalf("host function callable: %+v", got)
		}
	}
	// Time and random standard functions deliberately remain available.
	for _, expression := range []string{"$random() >= 0 and $random() < 1", "$millis() > 0", "$length($now()) > 0", "$count($shuffle([1,2,3])) = 3"} {
		if got := Check(context.Background(), request(expression, `true`)); got.State != "up" {
			t.Fatalf("standard nondeterministic function failed: %+v", got)
		}
	}
}

func TestResourceBoundsAndRecovery(t *testing.T) {
	supported(t)
	for _, tt := range []struct {
		name, expression string
		state            string
	}{
		{"range", "$sum([1..10001])", "down"},
		{"depth", "($f := function($n){$n = 0 ? 0 : 1 + $f($n-1)}; $f(1000))", "down"},
		{"output", "$pad('x', 1048577, 'x')", "down"},
		{"allocation", "$length($pad('x', 268435456, 'x'))", "pending"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()
			expected := "1"
			if tt.name == "allocation" {
				// Without effective resource controls the matching result would be
				// UP, rather than merely miss an unrelated expected value.
				expected = "268435456"
			}
			got := Check(context.Background(), request(tt.expression, expected))
			// RLIMIT_DATA may return ENOMEM without terminating the process.
			// Allocation must stop the worker or fail within the VM/parent
			// deadline, then release its slot for the success control below.
			boundedAllocation := tt.name == "allocation" && got.State == "down" &&
				(got.Message == "service check timed out" || got.Message == "service check failed")
			if got.State != tt.state && !boundedAllocation {
				t.Fatalf("resource result: %+v", got)
			}
			if time.Since(start) > 5*time.Second {
				t.Fatal("resource work escaped parent deadline")
			}
			if got.State == "pending" && got.Message != "service worker stopped without a valid result" {
				t.Fatalf("unexpected resource failure: %+v", got)
			}
			if next := Check(context.Background(), request("$sum([1,2,3])", "6")); next.State != "up" {
				t.Fatalf("failed to recover worker capacity: %+v", next)
			}
		})
	}
}

func TestParentDeadlineAndCancellation(t *testing.T) {
	supported(t)
	// Go's RE2 match runs synchronously inside a native builtin without engine
	// timeout checkpoints. Its finite-state CPU workload needs little memory;
	// the parent must interrupt it even though advisory checks cannot run.
	const expensive = "$contains($pad('a', 1000000, 'a'), /a{0,1000}b/)"
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "cancellation"}[cancel], func(t *testing.T) {
			r := request(expensive, "true")
			r.Timeout = 300 * time.Millisecond
			ctx := context.Background()
			var stop context.CancelFunc
			if cancel {
				r.Timeout = 4 * time.Second
				ctx, stop = context.WithCancel(ctx)
				defer stop()
				timer := time.AfterFunc(300*time.Millisecond, stop)
				defer timer.Stop()
			}
			start := time.Now()
			got := Check(ctx, r)
			want := "service check timed out"
			if cancel {
				want = "service check canceled"
			}
			if got.State != "down" || got.Message != want || time.Since(start) > 2*time.Second {
				t.Fatalf("unbounded interruption: %+v", got)
			}
			if next := Check(context.Background(), request("true", "true")); next.State != "up" {
				t.Fatalf("interrupted child not reaped: %+v", next)
			}
		})
	}
	ctx, stop := context.WithCancel(context.Background())
	stop()
	if got := Check(ctx, request("true", "true")); got.State != "pending" || got.LatencyMs != 0 {
		t.Fatalf("pre-admission cancellation: %+v", got)
	}
}

func TestRangeLimitSourceGap(t *testing.T) {
	supported(t)
	// The official runtime applies the finite profile's cap in lexical frames;
	// this regression must no longer preserve the old Go-port range bypass.
	r := request("($n := 10001; $count([1..$n]))", "10001")
	if got := Check(context.Background(), r); got.State != "down" {
		t.Fatalf("lexical block escaped range bound: %+v", got)
	}
}
