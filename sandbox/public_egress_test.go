package sandbox

import (
	"reflect"
	"testing"
)

func TestPublicEgressPresenceAndAcknowledgement(t *testing.T) {
	if publicEgressToProto(nil) != nil {
		t.Fatal("absent restriction must remain absent")
	}
	denied := publicEgressToProto(&PublicEgress{})
	if denied == nil {
		t.Fatal("deny-all must remain present")
	}
	if publicEgressAcknowledged(&PublicEgress{}, nil) {
		t.Fatal("missing server support must fail")
	}
	requested := &PublicEgress{TCPPorts: []uint32{80, 443}}
	if !publicEgressAcknowledged(requested, &PublicEgress{TCPPorts: []uint32{443}}) {
		t.Fatal("narrowed acknowledgement must pass")
	}
	if publicEgressAcknowledged(requested, &PublicEgress{TCPPorts: []uint32{22}}) {
		t.Fatal("wider acknowledgement must fail")
	}
	converted := publicEgressFromProto(publicEgressToProto(requested))
	if !reflect.DeepEqual(requested, converted) {
		t.Fatal("restriction must round trip")
	}
}
