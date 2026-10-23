package services

import (
	"context"
	"crypto/tls"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

func grpcSchema(t *testing.T) []byte {
	t.Helper()
	field := func(name string, number int32, kind descriptorpb.FieldDescriptorProto_Type) *descriptorpb.FieldDescriptorProto {
		return &descriptorpb.FieldDescriptorProto{Name: proto.String(name), Number: proto.Int32(number), Type: kind.Enum(), Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()}
	}
	set := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{{Name: proto.String("fixture.proto"), Package: proto.String("fixture"), Syntax: proto.String("proto3"), MessageType: []*descriptorpb.DescriptorProto{
		{Name: proto.String("Request"), Field: []*descriptorpb.FieldDescriptorProto{field("query", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING)}},
		{Name: proto.String("Response"), Field: []*descriptorpb.FieldDescriptorProto{field("status", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING), field("count", 2, descriptorpb.FieldDescriptorProto_TYPE_INT64)}},
	}, Service: []*descriptorpb.ServiceDescriptorProto{{Name: proto.String("Monitor"), Method: []*descriptorpb.MethodDescriptorProto{{Name: proto.String("Probe"), InputType: proto.String(".fixture.Request"), OutputType: proto.String(".fixture.Response")}}}}}}}
	response := set.File[0].MessageType[1]
	nested := field("nested", 3, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE)
	nested.TypeName = proto.String(".fixture.Response")
	items := field("items", 4, descriptorpb.FieldDescriptorProto_TYPE_STRING)
	items.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
	tags := field("tags", 5, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE)
	tags.TypeName = proto.String(".fixture.Response.TagsEntry")
	tags.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
	response.Field = append(response.Field, nested, items, tags)
	response.NestedType = []*descriptorpb.DescriptorProto{{Name: proto.String("TagsEntry"), Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)}, Field: []*descriptorpb.FieldDescriptorProto{field("key", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING), field("value", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING)}}}
	b, err := proto.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type fixtureMonitor interface{ marker() }
type fixtureMonitorServer struct{}

func (fixtureMonitorServer) marker() {}

func grpcFixture(t *testing.T, tlsCfg *tls.Config, variant string) (string, []byte) {
	t.Helper()
	descriptor := grpcSchema(t)
	base := GRPCConfig{DescriptorSet: descriptor, Method: "/fixture.Monitor/Probe", Request: []byte(`{"query":"fixture-secret"}`)}
	m, _, err := grpcMethod(base)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var opts []grpc.ServerOption
	if tlsCfg != nil {
		opts = append(opts, grpc.Creds(credentials.NewTLS(tlsCfg)))
	}
	s := grpc.NewServer(opts...)
	hs := health.NewServer()
	hs.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	hs.SetServingStatus("stopped", healthpb.HealthCheckResponse_NOT_SERVING)
	healthpb.RegisterHealthServer(s, hs)
	s.RegisterService(&grpc.ServiceDesc{ServiceName: "fixture.Monitor", HandlerType: (*fixtureMonitor)(nil), Methods: []grpc.MethodDesc{{MethodName: "Probe", Handler: func(_ any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
		in := dynamicpb.NewMessage(m.Input())
		if err := dec(in); err != nil {
			return nil, err
		}
		if in.Get(m.Input().Fields().ByName("query")).String() != "fixture-secret" {
			t.Error("incorrect gRPC JSON request")
		}
		md, _ := metadata.FromIncomingContext(ctx)
		if len(md.Get("authorization")) != 1 || md.Get("authorization")[0] != "Bearer fixture-secret" {
			return nil, status.Error(codes.Unauthenticated, "fixture-secret private-value")
		}
		if variant == "error" {
			return nil, status.Error(codes.PermissionDenied, "fixture-secret private-value")
		}
		if variant == "deadline" {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		out := dynamicpb.NewMessage(m.Output())
		v := "ready private-value"
		if variant == "oversize" {
			v = strings.Repeat("x", grpcMaxMessage+1)
		}
		out.Set(m.Output().Fields().ByName("status"), protoreflect.ValueOfString(v))
		out.Set(m.Output().Fields().ByName("count"), protoreflect.ValueOfInt64(9007199254740993))
		if variant == "json-budget" {
			list := out.Mutable(m.Output().Fields().ByName("items")).List()
			for i := 0; i < 12000; i++ {
				list.Append(protoreflect.ValueOfString(""))
			}
		}
		if variant == "maps" {
			out.Mutable(m.Output().Fields().ByName("tags")).Map().Set(protoreflect.ValueOfString("tag").MapKey(), protoreflect.ValueOfString("ready"))
		}
		if variant == "nested" || variant == "deep-response" {
			depth := 1
			if variant == "deep-response" {
				depth = 40
			}
			current := out
			for i := 0; i < depth; i++ {
				child := dynamicpb.NewMessage(m.Output())
				child.Set(m.Output().Fields().ByName("count"), protoreflect.ValueOfInt64(42))
				current.Set(m.Output().Fields().ByName("nested"), protoreflect.ValueOfMessage(child))
				current = child
			}
		}
		return out, nil
	}}}}, fixtureMonitorServer{})
	done := make(chan struct{})
	go func() { defer close(done); _ = s.Serve(ln) }()
	t.Cleanup(func() { s.Stop(); ln.Close(); <-done })
	return ln.Addr().String(), descriptor
}
func TestGRPCUnaryProtocol(t *testing.T) {
	for _, variant := range []string{"ok", "error", "deadline", "oversize", "auth", "json-budget", "deep-response", "nested", "maps"} {
		t.Run(variant, func(t *testing.T) {
			a, d := grpcFixture(t, nil, variant)
			c := GRPCConfig{Address: a, Mode: "unary", TLSMode: "plain", DescriptorSet: d, Method: "/fixture.Monitor/Probe", Request: []byte(`{"query":"fixture-secret"}`), Metadata: map[string]string{"authorization": "Bearer fixture-secret"}, Keyword: "ready", ScalarPath: []string{"count"}, Condition: &Condition{Operator: "eq", Expected: []byte(`9007199254740993`)}}
			want := "down"
			if variant == "ok" || variant == "maps" || variant == "nested" {
				want = "up"
			}
			if variant == "nested" {
				c.ScalarPath = []string{"nested", "count"}
				c.Condition.Expected = []byte(`42`)
			}
			if variant == "auth" {
				c.Metadata = nil
			}
			expectState(t, Config{Type: GRPC, Timeout: 150 * time.Millisecond, GRPC: &c}, want)
		})
	}
}

func TestGRPCRejectsInheritedCredentials(t *testing.T) {
	a, d := grpcFixture(t, nil, "ok")
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer fixture-secret"))
	c := Config{Type: GRPC, Timeout: time.Second, GRPC: &GRPCConfig{Address: a, Mode: "unary", TLSMode: "plain", DescriptorSet: d, Method: "/fixture.Monitor/Probe", Request: []byte(`{"query":"fixture-secret"}`)}}
	if r := Check(ctx, c); r.State != "down" {
		t.Fatal("inherited caller credentials")
	}
}
func TestGRPCHealthTLSAndKeywords(t *testing.T) {
	tc, trust := testTLS(t)
	a, d := grpcFixture(t, tc, "ok")
	c := GRPCConfig{Address: a, Mode: "health", TLS: trust}
	expectState(t, Config{Type: GRPC, Timeout: time.Second, GRPC: &c}, "up")
	c.HealthService = "stopped"
	expectState(t, Config{Type: GRPC, Timeout: time.Second, GRPC: &c}, "down")
	c.HealthService = "missing"
	expectState(t, Config{Type: GRPC, Timeout: time.Second, GRPC: &c}, "down")
	c.HealthService = ""
	c.TLS = nil
	expectState(t, Config{Type: GRPC, Timeout: time.Second, GRPC: &c}, "down")
	c.TLS = &TLSConfig{IgnoreVerification: true}
	expectState(t, Config{Type: GRPC, Timeout: time.Second, GRPC: &c}, "up")
	u := GRPCConfig{Address: a, Mode: "unary", TLS: trust, DescriptorSet: d, Method: "/fixture.Monitor/Probe", Request: []byte(`{"query":"fixture-secret"}`), Metadata: map[string]string{"authorization": "Bearer fixture-secret"}, Keyword: "absent", InvertKeyword: true}
	expectState(t, Config{Type: GRPC, Timeout: time.Second, GRPC: &u}, "up")
	u.InvertKeyword = false
	expectState(t, Config{Type: GRPC, Timeout: time.Second, GRPC: &u}, "down")
	u.Keyword = ""
	u.ScalarPath = []string{"status"}
	u.Condition = &Condition{Operator: "eq", Expected: []byte(`"other"`)}
	expectState(t, Config{Type: GRPC, Timeout: time.Second, GRPC: &u}, "down")
}
func TestGRPCValidationAndCancellation(t *testing.T) {
	base := GRPCConfig{Address: "127.0.0.1:1", Mode: "unary", DescriptorSet: grpcSchema(t), Method: "/fixture.Monitor/Probe"}
	for _, change := range []func(*GRPCConfig){func(c *GRPCConfig) { c.Mode = "" }, func(c *GRPCConfig) { c.Mode = "health" }, func(c *GRPCConfig) { c.Method = "bad" }, func(c *GRPCConfig) { c.Method = "/fixture.Monitor/Missing" }, func(c *GRPCConfig) { c.DescriptorSet = []byte("invalid") }, func(c *GRPCConfig) { c.DescriptorSet = make([]byte, 65537) }, func(c *GRPCConfig) { c.Request = []byte(`{"unknown":1}`) }, func(c *GRPCConfig) { c.ScalarPath = []string{"count"} }, func(c *GRPCConfig) { c.Metadata = map[string]string{"authorization": "bad\nsecret"} }, func(c *GRPCConfig) { c.Metadata = map[string]string{"grpc-timeout": "1S"} }} {
		c := base
		change(&c)
		if c.validate() == nil {
			t.Fatal("accepted invalid gRPC config")
		}
	}
	a := fixture(t, func(c net.Conn) error {
		var b [4096]byte
		for {
			if _, e := c.Read(b[:]); e != nil {
				return nil
			}
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	r := Check(ctx, Config{Type: GRPC, Timeout: time.Second, GRPC: &GRPCConfig{Address: a, Mode: "health"}})
	if r.Message != "service check canceled" {
		t.Fatal(r)
	}
}
