package domain

import (
	"encoding/json"
	"time"
)

type Transport string

const (
	TransportOS      Transport = "os"
	TransportBLE     Transport = "ble"
	TransportClassic Transport = "classic"
)

type Endpoint struct {
	ID        string    `json:"id"`
	Transport Transport `json:"transport"`
	Address   string    `json:"address,omitempty"`
}

type Device struct {
	ID               string                      `json:"id"`
	Name             string                      `json:"name"`
	Paired           bool                        `json:"paired"`
	Connected        bool                        `json:"connected"`
	RSSI             *int16                      `json:"rssi,omitempty"`
	Services         []string                    `json:"services,omitempty"`
	Information      map[string]string           `json:"information,omitempty"`
	ManufacturerData []ManufacturerData          `json:"manufacturer_data,omitempty"`
	ServiceData      []ServiceData               `json:"service_data,omitempty"`
	Identification   *Identification             `json:"identification,omitempty"`
	PossibleMatches  []string                    `json:"possible_matches,omitempty"`
	Properties       map[string]Property         `json:"properties,omitempty"`
	Capabilities     map[string]DeviceCapability `json:"capabilities,omitempty"`
	Endpoints        []Endpoint                  `json:"endpoints"`
	LastSeenAt       *time.Time                  `json:"last_seen_at,omitempty"`
}

type ManufacturerData struct {
	CompanyID uint16 `json:"company_id"`
	Data      string `json:"data"`
}
type ServiceData struct {
	UUID string `json:"uuid"`
	Data string `json:"data"`
}
type Identification struct {
	Vendor     string `json:"vendor"`
	Family     string `json:"family"`
	Model      string `json:"model,omitempty"`
	Confidence string `json:"confidence"`
}
type Property struct {
	Value      json.RawMessage `json:"value"`
	Unit       string          `json:"unit,omitempty"`
	Display    string          `json:"display,omitempty"`
	Estimated  bool            `json:"estimated,omitempty"`
	Resolution int             `json:"resolution,omitempty"`
	Verified   bool            `json:"verified,omitempty"`
	Correlated bool            `json:"correlated,omitempty"`
}

// CapabilityStatus says whether a capability can be used now.
type CapabilityStatus string

const (
	StatusAvailable   CapabilityStatus = "available"
	StatusPlanned     CapabilityStatus = "planned"
	StatusUnsupported CapabilityStatus = "unsupported"
)

// ValueKind is the type of a capability's value.
type ValueKind string

const (
	KindBattery ValueKind = "battery"
	KindEnum    ValueKind = "enum"
	KindBoolean ValueKind = "boolean"
	KindInteger ValueKind = "integer"
	KindNumber  ValueKind = "number"
	KindString  ValueKind = "string"
)

type DeviceCapability struct {
	Status   CapabilityStatus `json:"status"`
	Kind     ValueKind        `json:"type,omitempty"`
	Readable bool             `json:"readable,omitempty"`
	Writable bool             `json:"writable,omitempty"`
	Values   []string         `json:"values,omitempty"`
	Minimum  *float64         `json:"minimum,omitempty"`
	Maximum  *float64         `json:"maximum,omitempty"`
	Step     *float64         `json:"step,omitempty"`
	Reason   string           `json:"reason,omitempty"`
}

type Support string

const (
	Supported          Support = "supported"
	Unsupported        Support = "unsupported"
	PermissionRequired Support = "permission-required"
)

type Capabilities struct {
	Scan       Support `json:"scan"`
	Pair       Support `json:"pair"`
	Unpair     Support `json:"unpair"`
	Connect    Support `json:"connect"`
	Disconnect Support `json:"disconnect"`
	GATT       Support `json:"gatt"`
}
