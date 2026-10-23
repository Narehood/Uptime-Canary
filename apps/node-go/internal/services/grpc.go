package services

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync/atomic"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// GRPCConfig has explicit health or unary semantics. DescriptorSet is an inline
// binary FileDescriptorSet (base64 in JSON), including its dependencies. No
// reflection request, filesystem import, source compiler or service discovery.
type GRPCConfig struct {
	Address       string            `json:"address"`
	Mode          string            `json:"mode"` // health or unary; required
	TLSMode       string            `json:"tlsMode,omitempty"`
	TLS           *TLSConfig        `json:"tls,omitempty"`
	HealthService string            `json:"healthService,omitempty"`
	DescriptorSet []byte            `json:"descriptorSet,omitempty"`
	Method        string            `json:"method,omitempty"` // /package.Service/Method
	Request       json.RawMessage   `json:"request,omitempty"`
	Keyword       string            `json:"keyword,omitempty"`
	InvertKeyword bool              `json:"invertKeyword,omitempty"`
	ScalarPath    []string          `json:"scalarPath,omitempty"` // protobuf field names
	Condition     *Condition        `json:"condition,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

func (c GRPCConfig) validate() error {
	total := 0
	if len(c.Metadata) > 16 {
		return errors.New("gRPC metadata exceeds limit")
	}
	for k, v := range c.Metadata {
		total += len(k) + len(v)
		if k == "" || len(k) > 64 || len(v) > 4096 || total > 8192 || strings.HasPrefix(k, "grpc-") || strings.HasSuffix(k, "-bin") || k == "content-type" || k == "te" || k == "user-agent" {
			return errors.New("invalid gRPC metadata")
		}
		for _, ch := range k {
			if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-' || ch == '.') {
				return errors.New("invalid gRPC metadata key")
			}
		}
		for _, ch := range v {
			if ch < 32 || ch > 126 {
				return errors.New("invalid gRPC metadata value")
			}
		}
	}
	if err := validAddress(c.Address); err != nil {
		return err
	}
	if err := validMode(c.TLSMode, c.TLS); err != nil {
		return err
	}
	h, _, _ := net.SplitHostPort(c.Address)
	if c.TLSMode != "plain" {
		if _, err := tlsSettings(c.TLS, h); err != nil {
			return err
		}
	}
	if len(c.HealthService) > 256 || strings.ContainsAny(c.HealthService, "\x00\r\n") {
		return errors.New("invalid gRPC health service")
	}
	switch c.Mode {
	case "health":
		if len(c.DescriptorSet) > 0 || c.Method != "" || len(c.Request) > 0 || c.Keyword != "" || c.InvertKeyword || len(c.ScalarPath) > 0 || c.Condition != nil {
			return errors.New("gRPC health mode cannot use custom method options")
		}
		return nil
	case "unary":
		if c.HealthService != "" || len(c.Keyword) > 4096 || (c.InvertKeyword && c.Keyword == "") {
			return errors.New("invalid gRPC unary options")
		}
		if (c.Condition == nil) != (len(c.ScalarPath) == 0) {
			return errors.New("gRPC scalar path and condition must be paired")
		}
		if err := validateScalarPath(c.ScalarPath); err != nil {
			return err
		}
		if c.Condition != nil {
			if err := c.Condition.validate(); err != nil {
				return err
			}
		}
		_, _, err := grpcMethod(c)
		return err
	default:
		return errors.New("gRPC mode must be health or unary")
	}
}

func grpcMethod(c GRPCConfig) (protoreflect.MethodDescriptor, *dynamicpb.Message, error) {
	bad := errors.New("invalid gRPC descriptor, method, or JSON request")
	if len(c.DescriptorSet) == 0 || len(c.DescriptorSet) > 65536 || len(c.Method) > 512 || len(c.Request) > 65536 {
		return nil, nil, bad
	}
	parts := strings.Split(c.Method, "/")
	if len(parts) != 3 || parts[0] != "" || parts[1] == "" || parts[2] == "" {
		return nil, nil, bad
	}
	set := new(descriptorpb.FileDescriptorSet)
	if (proto.UnmarshalOptions{RecursionLimit: 32, DiscardUnknown: true, Resolver: new(protoregistry.Types)}).Unmarshal(c.DescriptorSet, set) != nil || len(set.File) == 0 || len(set.File) > 16 {
		return nil, nil, bad
	}
	// Bound descriptor work before constructing linked descriptors. Source info
	// and nonessential message/field options are discarded; map-entry semantics
	// remain. No extension/global registry resolution.
	nodes := 0
	var visit func([]*descriptorpb.DescriptorProto, int) bool
	visit = func(ms []*descriptorpb.DescriptorProto, depth int) bool {
		if depth > 16 {
			return false
		}
		for _, m := range ms {
			nodes += 1 + len(m.Field) + len(m.EnumType) + len(m.OneofDecl)
			if nodes > 512 || len(m.Extension) > 0 || len(m.ExtensionRange) > 0 {
				return false
			}
			mapEntry := m.GetOptions().GetMapEntry()
			m.Options = nil
			if mapEntry {
				m.Options = &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)}
			}
			for _, f := range m.Field {
				f.Options = nil
			}
			if !visit(m.NestedType, depth+1) {
				return false
			}
		}
		return true
	}
	for _, f := range set.File {
		if f.GetSyntax() != "proto3" || len(f.Extension) > 0 || !visit(f.MessageType, 0) {
			return nil, nil, bad
		}
		f.Options = nil
		f.SourceCodeInfo = nil
		for _, s := range f.Service {
			nodes += 1 + len(s.Method)
			s.Options = nil
			for _, m := range s.Method {
				m.Options = nil
			}
		}
		if nodes > 512 {
			return nil, nil, bad
		}
	}
	files, err := protodesc.NewFiles(set)
	if err != nil {
		return nil, nil, bad
	}
	d, err := files.FindDescriptorByName(protoreflect.FullName(parts[1]))
	if err != nil {
		return nil, nil, bad
	}
	s, ok := d.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, nil, bad
	}
	m := s.Methods().ByName(protoreflect.Name(parts[2]))
	if m == nil || m.IsStreamingClient() || m.IsStreamingServer() {
		return nil, nil, bad
	}
	if len(c.ScalarPath) > 0 && !validGRPCScalarPath(m.Output(), c.ScalarPath) {
		return nil, nil, bad
	}
	request := c.Request
	if len(request) == 0 {
		request = json.RawMessage(`{}`)
	}
	in := dynamicpb.NewMessage(m.Input())
	if (protojson.UnmarshalOptions{RecursionLimit: 32, Resolver: new(protoregistry.Types)}).Unmarshal(request, in) != nil {
		return nil, nil, bad
	}
	if proto.Size(in) > 65536 {
		return nil, nil, bad
	}
	return m, in, nil
}

func validGRPCScalarPath(m protoreflect.MessageDescriptor, path []string) bool {
	for i, name := range path {
		f := m.Fields().ByName(protoreflect.Name(name))
		if f == nil || f.IsMap() || f.IsList() {
			return false
		}
		if i < len(path)-1 {
			if f.Kind() != protoreflect.MessageKind {
				return false
			}
			m = f.Message()
			continue
		}
		switch f.Kind() {
		case protoreflect.StringKind, protoreflect.BoolKind, protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind, protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind, protoreflect.Uint32Kind, protoreflect.Fixed32Kind, protoreflect.Uint64Kind, protoreflect.Fixed64Kind, protoreflect.FloatKind, protoreflect.DoubleKind:
			return true
		}
	}
	return false
}

// Per-call codec limits protobuf recursion without registering a global codec.
type boundedProtoCodec struct{}

const grpcMaxMessage = 128 << 10

func (boundedProtoCodec) Name() string { return "proto" }
func (boundedProtoCodec) Marshal(v any) ([]byte, error) {
	m, ok := v.(proto.Message)
	if !ok {
		return nil, errors.New("invalid protobuf message")
	}
	return proto.Marshal(m)
}
func (boundedProtoCodec) Unmarshal(b []byte, v any) error {
	m, ok := v.(proto.Message)
	if !ok || len(b) > grpcMaxMessage {
		return errors.New("protobuf result exceeds limit")
	}
	return (proto.UnmarshalOptions{RecursionLimit: 32, DiscardUnknown: true, Resolver: new(protoregistry.Types)}).Unmarshal(b, m)
}

func checkGRPC(ctx context.Context, c GRPCConfig) Result {
	// Replace caller metadata even when empty: credentials must be explicit.
	ctx = metadata.NewOutgoingContext(ctx, metadata.New(c.Metadata))
	var transport credentials.TransportCredentials
	if c.TLSMode == "plain" {
		transport = insecure.NewCredentials()
	} else {
		h, _, _ := net.SplitHostPort(c.Address)
		cfg, err := tlsSettings(c.TLS, h)
		if err != nil {
			return down("invalid gRPC TLS configuration")
		}
		transport = credentials.NewTLS(cfg)
	}
	var attempts atomic.Int32
	conn, err := grpc.NewClient("passthrough:///"+c.Address, grpc.WithTransportCredentials(transport), grpc.WithNoProxy(), grpc.WithDisableRetry(), grpc.WithDisableServiceConfig(), grpc.WithMaxHeaderListSize(16384), grpc.WithStaticStreamWindowSize(65536), grpc.WithStaticConnWindowSize(65536), grpc.WithContextDialer(func(_ context.Context, address string) (net.Conn, error) {
		if address != c.Address || attempts.Add(1) > 1 {
			return nil, errors.New("gRPC endpoint or connection limit")
		}
		return dial(ctx, "tcp", c.Address)
	}), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(grpcMaxMessage), grpc.MaxCallSendMsgSize(65536), grpc.ForceCodec(boundedProtoCodec{})))
	if err != nil {
		return down("gRPC client configuration failed")
	}
	defer conn.Close()
	if c.Mode == "health" {
		r, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{Service: c.HealthService})
		if err != nil {
			return down("gRPC health request failed")
		}
		if r.Status != healthpb.HealthCheckResponse_SERVING {
			return down("gRPC health service is not serving")
		}
		return up("gRPC health service is serving")
	}
	m, in, err := grpcMethod(c)
	if err != nil {
		return down("invalid gRPC custom method configuration")
	}
	out := dynamicpb.NewMessage(m.Output())
	if conn.Invoke(ctx, c.Method, in, out) != nil {
		return down("gRPC custom method request failed")
	}
	// Count rendered field names, escaping and repeated entries before protojson
	// allocates its output. Descriptor-controlled names can amplify small messages.
	budget, nodes := maxBody, 0
	if !grpcJSONBudget(out.ProtoReflect(), 0, &budget, &nodes) {
		return down("gRPC custom method result exceeds limit")
	}
	b, err := (protojson.MarshalOptions{Resolver: new(protoregistry.Types)}).Marshal(out)
	if err != nil || len(b) > maxBody {
		return down("gRPC custom method result exceeds limit")
	}
	if c.Keyword != "" && strings.Contains(string(b), c.Keyword) == c.InvertKeyword {
		return down("gRPC response keyword did not match")
	}
	if c.Condition != nil {
		value, isNull, ok := grpcScalar(out.ProtoReflect(), c.ScalarPath)
		if !ok || !c.Condition.matches(value, isNull) {
			return down("gRPC scalar condition did not match")
		}
	}
	return up("gRPC custom unary method succeeded")
}

func grpcJSONBudget(m protoreflect.Message, depth int, budget, nodes *int) bool {
	if depth > 32 {
		return false
	}
	*budget -= 2
	ok := true
	m.Range(func(f protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		*budget -= 6*len(f.JSONName()) + 4
		var value func(protoreflect.FieldDescriptor, protoreflect.Value) bool
		value = func(fd protoreflect.FieldDescriptor, x protoreflect.Value) bool {
			*nodes++
			*budget -= 64
			if *nodes > 8192 || *budget < 0 {
				return false
			}
			switch fd.Kind() {
			case protoreflect.MessageKind:
				return grpcJSONBudget(x.Message(), depth+1, budget, nodes)
			case protoreflect.StringKind:
				*budget -= 6 * len(x.String())
			case protoreflect.BytesKind:
				*budget -= 2 * len(x.Bytes())
			case protoreflect.EnumKind:
				e := fd.Enum().Values().ByNumber(x.Enum())
				if e != nil {
					*budget -= 6 * len(e.Name())
				}
			}
			return *budget >= 0
		}
		if f.IsMap() {
			v.Map().Range(func(k protoreflect.MapKey, x protoreflect.Value) bool {
				*budget -= 6*len(k.String()) + 4
				ok = value(f.MapValue(), x)
				return ok
			})
		} else if f.IsList() {
			for i := 0; i < v.List().Len(); i++ {
				if !value(f, v.List().Get(i)) {
					ok = false
					break
				}
			}
		} else {
			ok = value(f, v)
		}
		return ok
	})
	return ok && *budget >= 0
}

func grpcScalar(m protoreflect.Message, path []string) ([]byte, bool, bool) {
	for i, name := range path {
		f := m.Descriptor().Fields().ByName(protoreflect.Name(name))
		if f == nil || f.IsList() || f.IsMap() {
			return nil, false, false
		}
		if f.HasPresence() && !m.Has(f) {
			return nil, true, true
		}
		v := m.Get(f)
		if i < len(path)-1 {
			if f.Kind() != protoreflect.MessageKind {
				return nil, false, false
			}
			m = v.Message()
			continue
		}
		switch f.Kind() {
		case protoreflect.StringKind:
			return []byte(v.String()), false, true
		case protoreflect.BoolKind, protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind, protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind, protoreflect.Uint32Kind, protoreflect.Fixed32Kind, protoreflect.Uint64Kind, protoreflect.Fixed64Kind, protoreflect.FloatKind, protoreflect.DoubleKind:
			return []byte(v.String()), false, true
		default:
			return nil, false, false
		}
	}
	return nil, false, false
}
