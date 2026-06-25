package abi

import "encoding/binary"

// EventLen is the fixed-header wire size of an encoded Event. Most kinds encode
// to exactly this; KindMessage appends a variable-length topic and payload.
const EventLen = 13

// EncodeEvent serializes an input event. Fixed header layout (LE):
//
//	[0] magic=0x01  [1] version  [2] kind  [3] keyType
//	[4:8] rune int32  [8] mods  [9:11] w uint16  [11:13] h uint16
//
// For KindMessage the header is followed by a variable tail:
//
//	[13:15] topicLen uint16  [15:15+t] topic  [..:..+4] dataLen uint32  [..] data
func EncodeEvent(e Event) []byte {
	tail := 0
	if e.Kind == KindMessage {
		tail = 2 + len(e.Topic) + 4 + len(e.Data)
	}
	b := make([]byte, EventLen+tail)
	b[0] = magicEvent
	b[1] = Version
	b[2] = byte(e.Kind)
	b[3] = byte(e.Key)
	binary.LittleEndian.PutUint32(b[4:8], uint32(e.Ch))
	b[8] = byte(e.Mods)
	binary.LittleEndian.PutUint16(b[9:11], uint16(e.W))
	binary.LittleEndian.PutUint16(b[11:13], uint16(e.H))
	if e.Kind == KindMessage {
		off := EventLen
		binary.LittleEndian.PutUint16(b[off:off+2], uint16(len(e.Topic)))
		off += 2
		off += copy(b[off:], e.Topic)
		binary.LittleEndian.PutUint32(b[off:off+4], uint32(len(e.Data)))
		off += 4
		copy(b[off:], e.Data)
	}
	return b
}

// DecodeEvent parses bytes produced by EncodeEvent.
func DecodeEvent(b []byte) (Event, error) {
	if len(b) < EventLen {
		return Event{}, ErrShort
	}
	if b[0] != magicEvent {
		return Event{}, ErrMagic
	}
	if b[1] != Version {
		return Event{}, ErrVersion
	}
	e := Event{
		Kind: Kind(b[2]),
		Key:  KeyType(b[3]),
		Ch:   rune(binary.LittleEndian.Uint32(b[4:8])),
		Mods: Mods(b[8]),
		W:    int(binary.LittleEndian.Uint16(b[9:11])),
		H:    int(binary.LittleEndian.Uint16(b[11:13])),
	}
	if e.Kind != KindMessage {
		return e, nil
	}
	off := EventLen
	if len(b) < off+2 {
		return Event{}, ErrShort
	}
	tlen := int(binary.LittleEndian.Uint16(b[off : off+2]))
	off += 2
	if len(b) < off+tlen+4 {
		return Event{}, ErrShort
	}
	e.Topic = string(b[off : off+tlen])
	off += tlen
	dlen := int(binary.LittleEndian.Uint32(b[off : off+4]))
	off += 4
	if len(b) < off+dlen {
		return Event{}, ErrShort
	}
	e.Data = append([]byte(nil), b[off:off+dlen]...)
	return e, nil
}
