"""The headset side of Sony's v2 control protocol, as Betty's driver reads it.

This implements Betty's own understanding of the protocol, from
cmd/bt-driver-sony/sony.go. It can show that the driver and Betty work end
to end, not that real Sony firmware behaves this way.
"""

SERVICE_V2 = "956C7B26-D49A-4BA8-B03F-B17D393CB6E2"

DATA_ACK = 0x01
DATA_MDR = 0x0C
_START, _ESCAPE, _END = 0x3E, 0x3D, 0x3C


def encode(data_type: int, sequence: int, payload: bytes) -> bytes:
    body = bytes([data_type, sequence]) + len(payload).to_bytes(4, "big") + payload
    body += bytes([sum(body) & 0xFF])
    frame = bytearray([_START])
    for value in body:
        if value in (_END, _ESCAPE, _START):
            frame += bytes([_ESCAPE, value - 0x10])
        else:
            frame.append(value)
    frame.append(_END)
    return bytes(frame)


class Decoder:
    """Splits a byte stream into (data_type, sequence, payload) frames."""

    def __init__(self) -> None:
        self._buffer = bytearray()

    def feed(self, data: bytes):
        self._buffer += data
        frames = []
        while True:
            start = self._buffer.find(_START)
            if start < 0:
                self._buffer.clear()
                return frames
            end = self._buffer.find(_END, start + 1)
            if end < 0:
                del self._buffer[:start]
                return frames
            escaped = self._buffer[start + 1 : end]
            del self._buffer[: end + 1]
            body = bytearray()
            i = 0
            while i < len(escaped):
                if escaped[i] == _ESCAPE and i + 1 < len(escaped):
                    body.append(escaped[i + 1] + 0x10)
                    i += 2
                else:
                    body.append(escaped[i])
                    i += 1
            if len(body) >= 7 and (sum(body[:-1]) & 0xFF) == body[-1]:
                frames.append((body[0], body[1], bytes(body[6:-1])))


class Headset:
    """Answers the driver's v2 session setup and battery queries."""

    def __init__(self, battery: int, charging: bool = False) -> None:
        self.battery = battery
        self.charging = charging
        self._decoder = Decoder()

    def receive(self, data: bytes) -> list[bytes]:
        """Returns the frames to send back for data received from the driver."""
        replies = []
        for data_type, sequence, payload in self._decoder.feed(data):
            if data_type != DATA_MDR:
                continue
            replies.append(encode(DATA_ACK, 1 - (sequence & 1), b""))
            answer = self._answer(payload)
            if answer is not None:
                replies.append(encode(DATA_MDR, sequence, answer))
        return replies

    def _answer(self, payload: bytes):
        if payload[:2] == b"\x00\x00":  # session initialization
            return b"\x01\x00\x02\x00"
        if payload[:2] == b"\x22\x00":  # headset battery
            return bytes([0x23, 0x00, self.battery, 1 if self.charging else 0])
        return None
