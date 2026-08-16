package transport

import (
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"net"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/internal/protocol/control"
	"github.com/Ceinl/plumtree/internal/protocol/pairing"
)

func serverInfo(id, fingerprint string) ServerInfo {
	return ServerInfo{StableID: id, HostKeyAlgorithm: "ssh-ed25519", HostKeyFingerprint: fingerprint,
		ProductVersion: "v1", Protocols: []string{pairing.ProtocolName, control.ProtocolName}}
}

func TestDiscoverOrderAndAmbiguity(t *testing.T) {
	var ports []int
	got, info, err := Discover(context.Background(), "example.test", 0, func(_ context.Context, endpoint Endpoint) (ServerInfo, error) {
		ports = append(ports, endpoint.Port)
		return serverInfo("same", "host"), nil
	})
	if err != nil || got.Port != 2222 || info.StableID != "same" || !reflect.DeepEqual(ports, []int{2222, 22}) {
		t.Fatalf("discover=%+v %+v ports=%v err=%v", got, info, ports, err)
	}
	_, _, err = Discover(context.Background(), "example.test", 22, func(_ context.Context, endpoint Endpoint) (ServerInfo, error) {
		if endpoint.Port != 22 {
			t.Fatalf("unexpected port %d", endpoint.Port)
		}
		return serverInfo("one", "one"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = Discover(context.Background(), "example.test", 0, func(_ context.Context, endpoint Endpoint) (ServerInfo, error) {
		return serverInfo(string(rune(endpoint.Port)), "different"), nil
	})
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("ambiguity err=%v", err)
	}
}

func TestPinAndExactParity(t *testing.T) {
	info := serverInfo("server", "host")
	endpoint := Endpoint{Host: "localhost", Port: 2222}
	if _, err := FirstUsePin(endpoint, info, false); !errors.Is(err, ErrConfirmationRequired) {
		t.Fatal(err)
	}
	pin, err := FirstUsePin(endpoint, info, true)
	if err != nil || pin.Verify(endpoint, info) != nil {
		t.Fatalf("pin=%+v err=%v", pin, err)
	}
	changed := info
	changed.HostKeyFingerprint = "new"
	if !errors.Is(pin.Verify(endpoint, changed), ErrHostKeyChanged) {
		t.Fatal("host key change accepted")
	}
	changed = info
	changed.StableID = "new"
	if !errors.Is(pin.Verify(endpoint, changed), ErrServerIDChanged) {
		t.Fatal("server ID change accepted")
	}
	if err := RequireProtocols(info, "v2"); !errors.Is(err, ErrVersionMismatch) {
		t.Fatal(err)
	}
}

func TestPrincipalAndLeafPolicy(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(nil)
	principal := DevicePrincipal{ServerID: "s", AuthorID: "a", DeviceID: "d", Fingerprint: "f"}
	envelope, err := SignPrincipal(private, principal, []byte("0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := VerifyPrincipal(public, envelope)
	if err != nil || got != principal {
		t.Fatalf("principal=%+v err=%v", got, err)
	}
	envelope.ServerID = "spoof"
	if _, err := VerifyPrincipal(public, envelope); !errors.Is(err, ErrSpoofedIdentity) {
		t.Fatal(err)
	}

	if err := AuthorizeSubsystem(Principal{Kind: PrincipalCandidate}, ControlSubsystem); !errors.Is(err, ErrNotAuthorized) {
		t.Fatal(err)
	}
	device := Principal{Kind: PrincipalDevice, ServerID: "s", Active: true}
	if err := AuthorizeControl(device, "wrong"); !errors.Is(err, ErrSpoofedIdentity) {
		t.Fatal(err)
	}
	if err := AuthorizeForwarding(device); !errors.Is(err, ErrNotAuthorized) {
		t.Fatal(err)
	}
	if _, err := AuthorizeLeaf(LeafRestricted, "visitor", true, map[string]bool{}); !errors.Is(err, ErrLeafDenied) {
		t.Fatal(err)
	}
	leaf, err := AuthorizeLeaf(LeafPublic, "visitor", true, nil)
	if err != nil || !leaf.Authenticated {
		t.Fatal(err)
	}
	args, err := AuthorizeLeafExec(Principal{Kind: PrincipalVisitor}, `get_identity "hello world"`)
	if err != nil || !reflect.DeepEqual(args, []string{"get_identity", "hello world"}) {
		t.Fatalf("args=%q err=%v", args, err)
	}
}

func TestControlHTTPStream(t *testing.T) {
	left, right := net.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- ServeHTTPStream(right, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, "ok")
		}), "v1")
	}()
	client, closer, err := NewHTTPClient(left, "v1")
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get("http://plumtree/api/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "ok" || response.Header.Get(control.VersionHeader) != "v1" {
		t.Fatalf("body=%q header=%q err=%v", body, response.Header.Get(control.VersionHeader), err)
	}
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("control stream did not stop")
	}
}

func TestCheckedClientRejectsIdentityHeader(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	client, closer, err := NewHTTPClient(left, "v1")
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	request, _ := http.NewRequest(http.MethodGet, "http://plumtree/api/v1/status", strings.NewReader(""))
	request.Header.Set("X-Plumtree-Principal", "spoof")
	if _, err := client.Do(request); !errors.Is(err, control.ErrIdentityHeader) {
		t.Fatalf("err=%v", err)
	}
}

func TestStreamConnectionDeadlinePassThrough(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	conn := &streamConn{rw: left}
	if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
}
