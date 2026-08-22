package driver

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
)

// Session implements the sequential duplex JSON protocol used by a driver process.
// A driver receives a Betty request, may make host calls while handling it, and
// finishes by sending one response with the same request ID.
type Session struct {
	decoder *json.Decoder
	encoder *json.Encoder
	nextID  uint64
}

func NewSession(input io.Reader, output io.Writer) *Session {
	return &Session{decoder: json.NewDecoder(input), encoder: json.NewEncoder(output)}
}
func (s *Session) Receive() (Request, error) {
	var request Request
	err := s.decoder.Decode(&request)
	return request, err
}
func (s *Session) Respond(response Response) error {
	response.Protocol = Version
	response.Type = TypeResponse
	return s.encoder.Encode(response)
}
func (s *Session) CallHost(method string, params, result any) error {
	s.nextID++
	id := "host-" + strconv.FormatUint(s.nextID, 10)
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if err = s.encoder.Encode(Message{Protocol: Version, Type: TypeHostRequest, ID: id, Method: method, Params: raw}); err != nil {
		return err
	}
	var response Message
	if err = s.decoder.Decode(&response); err != nil {
		return err
	}
	if response.Type != TypeHostResponse || response.ID != id {
		return fmt.Errorf("unexpected host response %q for %q", response.ID, id)
	}
	if response.Error != "" {
		return &RemoteError{Code: response.ErrorCode, Message: "host: " + response.Error, Details: response.Details}
	}
	if result == nil || len(response.Result) == 0 {
		return nil
	}
	return json.Unmarshal(response.Result, result)
}
