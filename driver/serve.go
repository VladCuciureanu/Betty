package driver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
)

// Driver describes a Betty driver executable. Every handler is optional: a
// missing handler, or one that returns a nil *Enrichment, tells Betty that this
// driver does not handle the request so that other drivers may.
type Driver struct {
	Manifest Manifest

	// Inspect identifies the devices this driver recognizes and reports their
	// capabilities and any cheaply available properties. The result is keyed
	// by device ID; devices the driver does not recognize are omitted.
	Inspect func(host *Session, request InspectRequest) (map[string]Enrichment, error)

	// Get reads request.Property from request.Device.
	Get func(host *Session, request PropertyRequest) (*Enrichment, error)

	// Set writes request.Value to request.Property and returns the state read
	// back from the device. A driver that cannot verify a write returns an
	// error instead of echoing the requested value.
	Set func(host *Session, request PropertyRequest) (*Enrichment, error)

	// Watch reports request.Property of request.Device as it changes, by
	// calling emit with an enrichment holding the current value: once
	// straight away, then after each change. It returns when watching ends,
	// for example because the device disconnected, or when emit fails
	// because Betty stopped listening. Without a Watch handler, bt watch
	// polls Get instead.
	Watch func(host *Session, request PropertyRequest, emit func(Enrichment) error) error
}

// InspectRequest is an inspect request from Betty.
type InspectRequest struct {
	// Purpose is PurposeScan or PurposeList. Treat unknown values as PurposeScan.
	Purpose string
	Devices []Device
}

// PropertyRequest is a get or set request for one property of one device.
type PropertyRequest struct {
	// Device is the target device, resolved from the request's device ID.
	Device   Device
	Property string
	// Value is the JSON value to write. It is empty for get requests.
	Value json.RawMessage
	// Devices holds every device Betty supplied, including Device.
	Devices []Device
}

// DecodeValue unmarshals the requested value into v and reports a CodeUsage
// error when it does not have the expected JSON type.
func (r PropertyRequest) DecodeValue(v any) error {
	if err := json.Unmarshal(r.Value, v); err != nil {
		return Errorf(CodeUsage, "invalid value for %s", r.Property)
	}
	return nil
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*/[a-z0-9][a-z0-9._-]*$`)

// ValidID reports whether id is a valid driver ID of the form
// "publisher/name", using lowercase letters, digits, '.', '_', and '-'.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// Serve runs the driver with the process's arguments and standard streams and
// exits the process. Call it from main.
func Serve(d Driver) {
	if err := d.Run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

// Run executes the driver protocol over the given streams. With the single
// argument "--manifest" it prints the manifest; with no arguments it handles
// requests until input ends. Run is exposed for tests; drivers call Serve.
func (d Driver) Run(args []string, in io.Reader, out io.Writer) error {
	if len(args) == 1 && args[0] == "--manifest" {
		manifest := d.Manifest
		if manifest.Protocol == 0 {
			manifest.Protocol = Version
		}
		if !ValidID(manifest.ID) {
			return fmt.Errorf("invalid driver ID %q", manifest.ID)
		}
		return json.NewEncoder(out).Encode(manifest)
	}
	if len(args) != 0 {
		return fmt.Errorf("unexpected arguments %q", args)
	}
	session := NewSession(in, out)
	for {
		request, err := session.Receive()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			_ = session.Respond(errorResponse(Errorf(CodeUsage, "invalid request: %v", err)))
			return err
		}
		response := d.handle(session, request)
		response.ID = request.ID
		if err := session.Respond(response); err != nil {
			return err
		}
	}
}

func (d Driver) handle(host *Session, request Request) (response Response) {
	defer func() {
		if recovered := recover(); recovered != nil {
			response = errorResponse(Errorf(CodeInternal, "driver %s panicked: %v", d.Manifest.ID, recovered))
		}
	}()
	if request.Protocol != Version {
		return errorResponse(Errorf(CodeUnsupported, "driver protocol %d is unsupported; this driver speaks %d", request.Protocol, Version))
	}
	switch request.Method {
	case MethodInspect:
		if d.Inspect == nil {
			return Response{}
		}
		enrichments, err := d.Inspect(host, InspectRequest{Purpose: request.Purpose, Devices: request.Devices})
		if err != nil {
			return errorResponse(err)
		}
		return Response{Enrichments: enrichments}
	case MethodWatch:
		device, ok := findDevice(request.Devices, request.DeviceID)
		if d.Watch == nil || !ok {
			return Response{}
		}
		emit := func(enrichment Enrichment) error {
			return host.encoder.Encode(Response{Protocol: Version, Type: TypeEvent, ID: request.ID, Enrichments: map[string]Enrichment{device.ID: enrichment}})
		}
		if err := d.Watch(host, PropertyRequest{Device: device, Property: request.Property, Devices: request.Devices}, emit); err != nil {
			return errorResponse(err)
		}
		return Response{}
	case MethodGet, MethodSet:
		handler := d.Get
		if request.Method == MethodSet {
			handler = d.Set
		}
		device, ok := findDevice(request.Devices, request.DeviceID)
		if handler == nil || !ok {
			return Response{}
		}
		enrichment, err := handler(host, PropertyRequest{Device: device, Property: request.Property, Value: request.Value, Devices: request.Devices})
		if err != nil {
			return errorResponse(err)
		}
		if enrichment == nil {
			return Response{}
		}
		return Response{Enrichments: map[string]Enrichment{device.ID: *enrichment}}
	default:
		return errorResponse(Errorf(CodeUnsupported, "unsupported method %q", request.Method))
	}
}

func errorResponse(err error) Response {
	code, details := CodeOf(err, CodeInternal)
	message := err.Error()
	if message == "" {
		// An empty message would be read as success.
		message = string(code)
	}
	return Response{Error: message, ErrorCode: string(code), Details: details}
}

func findDevice(devices []Device, id string) (Device, bool) {
	for _, device := range devices {
		if device.ID == id {
			return device, true
		}
	}
	return Device{}, false
}
