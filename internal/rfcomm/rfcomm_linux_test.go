//go:build linux

package rfcomm

import (
	"encoding/binary"
	"testing"

	"github.com/vladcuciureanu/betty/internal/btsock"
)

func TestServiceAttributeRequestContainsUUIDAndContinuation(t *testing.T) {
	uuid, err := parseUUID("956C7B26-D49A-4BA8-B03F-B17D393CB6E2")
	if err != nil {
		t.Fatal(err)
	}
	request := serviceAttributeRequest(7, uuid, []byte{0xaa, 0xbb})
	if request[0] != 0x06 || binary.BigEndian.Uint16(request[1:3]) != 7 || int(binary.BigEndian.Uint16(request[3:5])) != len(request)-5 {
		t.Fatalf("request=%x", request)
	}
	if request[len(request)-3] != 2 || request[len(request)-2] != 0xaa || request[len(request)-1] != 0xbb {
		t.Fatalf("continuation=%x", request[len(request)-3:])
	}
}

func TestParseServiceAttributeResponse(t *testing.T) {
	attributes := []byte{0x35, 0x06, 0x19, 0x00, 0x03, 0x08, 0x12}
	packet := []byte{0x07, 0x00, 0x01, 0x00, 0x00, 0x00, byte(len(attributes))}
	packet = append(packet, attributes...)
	packet = append(packet, 2, 0xaa, 0xbb)
	binary.BigEndian.PutUint16(packet[3:5], uint16(len(packet)-5))
	page, continuation, err := parseServiceAttributeResponse(packet, 1)
	if err != nil {
		t.Fatal(err)
	}
	if channel, ok := findRFCOMMChannel(page); !ok || channel != 18 {
		t.Fatalf("channel=%d ok=%t page=%x", channel, ok, page)
	}
	if len(continuation) != 2 || continuation[0] != 0xaa || continuation[1] != 0xbb {
		t.Fatalf("continuation=%x", continuation)
	}
}

func TestRFCOMMAddressIsLittleEndian(t *testing.T) {
	address, err := btsock.ParseAddress("bluez:F0:E2:09:00:00:01", "bluez:")
	if err != nil {
		t.Fatal(err)
	}
	if got := rfcommAddress(address); got != [6]byte{0x01, 0x00, 0x00, 0x09, 0xE2, 0xF0} {
		t.Fatalf("rfcommAddress=% x", got)
	}
}
