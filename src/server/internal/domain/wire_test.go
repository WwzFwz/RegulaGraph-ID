// C01 semantic and interop tests use synthetic records, real generated wire bytes, and explicit failures.
// No fixtures claim legal truth/model quality. Optional external fixture run is driven by integration/wire_roundtrip.py.
package domain

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"os"
	"path/filepath"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"strconv"
	"strings"
	"testing"
)

func TestUTF8SpanBoundaries(t *testing.T) {
	raw := []byte("Pasal é —")
	if CheckUTF8Span(raw, 0, 7) == nil {
		t.Fatal("split rune accepted")
	}
	if e := CheckUTF8Span(raw, 0, 8); e != nil {
		t.Fatal(e)
	}
	if CheckUTF8Span(raw, 0, 100) == nil {
		t.Fatal("out of bounds accepted")
	}
}
func TestStreamTerminalAndSequence(t *testing.T) {
	s := AnswerStreamValidator{}
	event := &pb.AnswerEvent{SchemaVersion: 1, RequestId: "req-test", StreamSequence: 1, Payload: &pb.AnswerEvent_TextDelta{TextDelta: "provisional"}}
	if e := s.Accept(event); e != nil {
		t.Fatal(e)
	}
	if s.Finish() == nil {
		t.Fatal("missing terminal accepted")
	}
	if s.Accept(event) == nil {
		t.Fatal("duplicate sequence accepted")
	}
	terminal := &pb.AnswerEvent{SchemaVersion: 1, RequestId: "req-test", StreamSequence: 2, Payload: &pb.AnswerEvent_Error{Error: &pb.OperationError{Code: pb.ErrorCode_ERROR_CODE_UNAVAILABLE, SafeMessage: "backend unavailable"}}}
	if e := s.Accept(terminal); e != nil {
		t.Fatal(e)
	}
	if e := s.Finish(); e != nil {
		t.Fatal(e)
	}
	if s.Accept(terminal) == nil {
		t.Fatal("second terminal accepted")
	}
}
func TestWireFixtureInterop(t *testing.T) {
	dir := os.Getenv("REGULAGRAPH_WIRE_FIXTURES")
	if dir == "" {
		t.Skip("run tests/integration/wire_roundtrip.py for four-language fixtures")
	}
	raw, e := os.ReadFile(filepath.Join(dir, "cases.tsv"))
	if e != nil {
		t.Fatal(e)
	}
	out := filepath.Join(dir, "go")
	if e = os.MkdirAll(out, 0755); e != nil {
		t.Fatal(e)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		f := strings.Split(strings.TrimSpace(line), "\t")
		t.Run(f[0], func(t *testing.T) {
			mt, e := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName(f[1]))
			if e != nil {
				t.Fatal(e)
			}
			m := mt.New().Interface()
			b, e := os.ReadFile(filepath.Join(dir, f[0]+".bin"))
			if e != nil {
				t.Fatal(e)
			}
			l := DefaultWireLimits
			l.MaxBytes, e = strconv.Atoi(f[3])
			if e != nil {
				t.Fatal(e)
			}
			err := DecodeWire(b, m, l)
			if (err == nil) != (f[2] == "valid") {
				t.Fatalf("validation: %v", err)
			}
			if e = proto.Unmarshal(b, m); e != nil {
				t.Fatal(e)
			}
			b, e = proto.MarshalOptions{Deterministic: true}.Marshal(m)
			if e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(out, f[0]+".bin"), b, 0600); e != nil {
				t.Fatal(e)
			}
		})
	}
}
