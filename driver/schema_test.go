package driver

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"reflect"
	"strings"
	"testing"
)

// The JSON Schema in docs/schema is generated from the Go types, so it cannot
// drift from them. After changing a protocol type, regenerate it with
//
//	go test ./driver -run TestProtocolSchema -update
var update = flag.Bool("update", false, "rewrite docs/schema/driver-protocol.schema.json")

const schemaPath = "../docs/schema/driver-protocol.schema.json"

// schemaEnums lists the values a field may take, by "Type.field". Fields not
// listed accept any value of their type.
var schemaEnums = map[string][]any{
	"Manifest.protocol":         {Version},
	"Request.protocol":          {Version},
	"Response.protocol":         {Version},
	"Message.protocol":          {Version},
	"Request.type":              {TypeRequest},
	"Request.method":            {MethodInspect, MethodGet, MethodSet},
	"Response.type":             {TypeResponse},
	"Message.type":              {TypeHostRequest, TypeHostResponse},
	"Capability.status":         {StatusAvailable, StatusPlanned, StatusUnsupported},
	"Capability.type":           {KindBattery, KindEnum, KindBoolean, KindInteger, KindNumber, KindString},
	"Identification.confidence": {ConfidenceName, ConfidenceModel, ConfidenceProtocol},
	"Endpoint.transport":        {"os", "classic", "ble"},
}

// schemaRoots are the documents the schema describes, by definition name.
var schemaRoots = []any{
	Manifest{}, Request{}, Response{}, Message{}, BatteryValue{},
	L2CAPOpenParams{}, L2CAPReadParams{}, L2CAPWriteParams{}, L2CAPChannelParams{},
	RFCOMMOpenParams{}, RFCOMMReadParams{}, RFCOMMWriteParams{}, RFCOMMChannelParams{},
	StorageGetParams{}, StorageSetParams{},
}

func TestProtocolSchema(t *testing.T) {
	generated := protocolSchema()
	if *update {
		if err := os.WriteFile(schemaPath, generated, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	current, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(current, []byte("\r\n"), []byte("\n")), generated) {
		t.Fatalf("%s is out of date; run go test ./driver -run TestProtocolSchema -update", schemaPath)
	}
}

func protocolSchema() []byte {
	defs := map[string]any{}
	for _, root := range schemaRoots {
		schemaFor(reflect.TypeOf(root), defs)
	}
	codes := make([]any, len(ErrorCodes))
	for i, code := range ErrorCodes {
		codes[i] = string(code)
	}
	defs["ErrorCode"] = map[string]any{"type": "string", "enum": codes}
	for _, name := range []string{"Response", "Message"} {
		properties := defs[name].(map[string]any)["properties"].(map[string]any)
		properties["error_code"] = map[string]any{"$ref": "#/$defs/ErrorCode"}
	}
	document := map[string]any{
		"$schema":     "https://json-schema.org/draft/2020-12/schema",
		"$id":         "https://github.com/VladCuciureanu/Betty/blob/main/docs/schema/driver-protocol.schema.json",
		"title":       "Betty driver protocol, version 1",
		"description": "Messages between Betty and a driver, and the values they carry. Generated from the Go SDK's types; see docs/DRIVER_PROTOCOL.md for their meaning.",
		"$defs":       defs,
	}
	out, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		panic(err)
	}
	return append(out, '\n')
}

var rawMessage = reflect.TypeOf(json.RawMessage{})

// schemaFor returns the schema of t, adding named structs to defs.
func schemaFor(t reflect.Type, defs map[string]any) map[string]any {
	if t == rawMessage || t.Kind() == reflect.Interface {
		return map[string]any{}
	}
	switch t.Kind() {
	case reflect.Pointer:
		return schemaFor(t.Elem(), defs)
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return map[string]any{"type": "integer"}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer", "minimum": 0}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": schemaFor(t.Elem(), defs)}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": schemaFor(t.Elem(), defs)}
	case reflect.Struct:
		ref := map[string]any{"$ref": "#/$defs/" + t.Name()}
		if _, done := defs[t.Name()]; done {
			return ref
		}
		object := map[string]any{"type": "object"}
		defs[t.Name()] = object // before the fields, for recursive types
		properties := map[string]any{}
		var required []string
		addFields(t, properties, &required, defs)
		object["properties"] = properties
		if len(required) > 0 {
			object["required"] = required
		}
		return ref
	}
	panic("no schema for " + t.String())
}

// addFields describes t's JSON fields, flattening embedded structs as
// encoding/json does.
func addFields(t reflect.Type, properties map[string]any, required *[]string, defs map[string]any) {
	for i := range t.NumField() {
		field := t.Field(i)
		tag := field.Tag.Get("json")
		if field.Anonymous && tag == "" {
			addFields(field.Type, properties, required, defs)
			continue
		}
		if !field.IsExported() || tag == "-" {
			continue
		}
		name, options, _ := strings.Cut(tag, ",")
		if name == "" {
			name = field.Name
		}
		property := schemaFor(field.Type, defs)
		if values, ok := schemaEnums[t.Name()+"."+name]; ok {
			property = map[string]any{"type": property["type"], "enum": values}
		}
		properties[name] = property
		if !strings.Contains(options, "omitempty") && field.Type.Kind() != reflect.Pointer {
			*required = append(*required, name)
		}
	}
}
