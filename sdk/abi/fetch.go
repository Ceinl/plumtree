package abi

import "encoding/binary"

// Fetch host function performs a gated outbound HTTP request. Egress is
// default-deny: the host only dispatches requests to hosts on the app's
// allowlist, and only for claimed apps. Like kv_get it uses the ptr/len + grow
// convention: the guest passes an encoded FetchRequest and an output buffer; the
// host writes an encoded FetchResponse and returns its length (or the needed
// length when the buffer is too small).

const (
	// FetchMaxURL caps a request URL length in bytes.
	FetchMaxURL = 2048
	// FetchMaxBody caps request and response body length in bytes.
	FetchMaxBody = 1 << 20 // 1 MiB
)

// Fetch result codes. A non-negative fetch return is the response length.
const (
	FetchErrDenied   int32 = -1 // egress not allowed (unclaimed app or host not on allowlist)
	FetchErrTooLarge int32 = -2 // URL or body exceeds its cap
	FetchErrInternal int32 = -3 // request failed (DNS, connection, timeout, bad request)
	FetchErrUnavail  int32 = -4 // no fetch capability present
)

// FetchRequest is an outbound HTTP request. Headers are intentionally omitted in
// v1; Method defaults to GET when empty.
type FetchRequest struct {
	Method string
	URL    string
	Body   []byte
}

// FetchResponse is the result of a FetchRequest. Status is the HTTP status code.
type FetchResponse struct {
	Status int
	Body   []byte
}

// EncodeFetchRequest serializes a request. Layout (LE):
//
//	[0:2] methodLen u16  [..] method  [..:..+2] urlLen u16  [..] url
//	[..:..+4] bodyLen u32  [..] body
func EncodeFetchRequest(r FetchRequest) []byte {
	b := make([]byte, 0, 8+len(r.Method)+len(r.URL)+len(r.Body))
	b = appendU16(b, len(r.Method))
	b = append(b, r.Method...)
	b = appendU16(b, len(r.URL))
	b = append(b, r.URL...)
	b = appendU32(b, len(r.Body))
	b = append(b, r.Body...)
	return b
}

// DecodeFetchRequest parses bytes produced by EncodeFetchRequest.
func DecodeFetchRequest(b []byte) (FetchRequest, error) {
	var r FetchRequest
	method, rest, ok := takeU16Bytes(b)
	if !ok {
		return r, ErrShort
	}
	r.Method = string(method)
	urlb, rest, ok := takeU16Bytes(rest)
	if !ok {
		return r, ErrShort
	}
	r.URL = string(urlb)
	body, _, ok := takeU32Bytes(rest)
	if !ok {
		return r, ErrShort
	}
	r.Body = append([]byte(nil), body...)
	return r, nil
}

// EncodeFetchResponse serializes a response. Layout (LE):
//
//	[0:2] status u16  [2:6] bodyLen u32  [6:] body
func EncodeFetchResponse(r FetchResponse) []byte {
	b := make([]byte, 0, 6+len(r.Body))
	b = appendU16(b, r.Status)
	b = appendU32(b, len(r.Body))
	b = append(b, r.Body...)
	return b
}

// DecodeFetchResponse parses bytes produced by EncodeFetchResponse.
func DecodeFetchResponse(b []byte) (FetchResponse, error) {
	if len(b) < 6 {
		return FetchResponse{}, ErrShort
	}
	status := int(binary.LittleEndian.Uint16(b[0:2]))
	body, _, ok := takeU32Bytes(b[2:])
	if !ok {
		return FetchResponse{}, ErrShort
	}
	return FetchResponse{Status: status, Body: append([]byte(nil), body...)}, nil
}

func appendU16(b []byte, n int) []byte {
	return binary.LittleEndian.AppendUint16(b, uint16(n))
}

func appendU32(b []byte, n int) []byte {
	return binary.LittleEndian.AppendUint32(b, uint32(n))
}

func takeU16Bytes(b []byte) (val, rest []byte, ok bool) {
	if len(b) < 2 {
		return nil, nil, false
	}
	n := int(binary.LittleEndian.Uint16(b[0:2]))
	if len(b) < 2+n {
		return nil, nil, false
	}
	return b[2 : 2+n], b[2+n:], true
}

func takeU32Bytes(b []byte) (val, rest []byte, ok bool) {
	if len(b) < 4 {
		return nil, nil, false
	}
	n := int(binary.LittleEndian.Uint32(b[0:4]))
	if len(b) < 4+n {
		return nil, nil, false
	}
	return b[4 : 4+n], b[4+n:], true
}
