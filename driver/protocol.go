// Package driver defines Betty's versioned external-driver wire protocol.
package driver

import "encoding/json"

const Version = 1

type Manifest struct {
	Protocol    int      `json:"protocol"`
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Permissions []string `json:"permissions,omitempty"`
	// Match, when set, limits the requests Betty sends this driver to those
	// with a device it may recognize. Without it the driver gets every
	// request.
	Match *Match `json:"match,omitempty"`
}

// Match lists cheap hints about the devices a driver recognizes. A device
// matches when any one hint does. Hints only decide whether Betty starts the
// driver; the driver still receives every device in the request.
type Match struct {
	// CompanyIDs are Bluetooth SIG company identifiers in manufacturer data.
	CompanyIDs []uint16 `json:"company_ids,omitempty"`
	// ServiceUUIDs are advertised or SDP service UUIDs, or service-data
	// UUIDs, as 16-bit ("180f") or 128-bit UUIDs.
	ServiceUUIDs []string `json:"service_uuids,omitempty"`
	// NameContains are case-insensitive substrings of the device name.
	NameContains []string `json:"name_contains,omitempty"`
}
type Request struct {
	Protocol int             `json:"protocol"`
	Type     string          `json:"type,omitempty"`
	ID       string          `json:"id,omitempty"`
	Method   string          `json:"method"`
	Purpose  string          `json:"purpose,omitempty"`
	DeviceID string          `json:"device_id,omitempty"`
	Property string          `json:"property,omitempty"`
	Value    json.RawMessage `json:"value,omitempty"`
	Devices  []Device        `json:"devices"`
}
type Response struct {
	Protocol    int                   `json:"protocol"`
	Type        string                `json:"type,omitempty"`
	ID          string                `json:"id,omitempty"`
	Enrichments map[string]Enrichment `json:"enrichments,omitempty"`
	Error       string                `json:"error,omitempty"`
	ErrorCode   string                `json:"error_code,omitempty"`
	Details     any                   `json:"details,omitempty"`
}
type Message struct {
	Protocol  int             `json:"protocol"`
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
	ErrorCode string          `json:"error_code,omitempty"`
	Details   any             `json:"details,omitempty"`
}

type RemoteError struct {
	Code    string
	Message string
	Details any
}

func (e *RemoteError) Error() string { return e.Message }

const (
	TypeRequest      = "request"
	TypeResponse     = "response"
	TypeHostRequest  = "host_request"
	TypeHostResponse = "host_response"
)
const (
	HostL2CAPOpen   = "l2cap.open"
	HostL2CAPRead   = "l2cap.read"
	HostL2CAPWrite  = "l2cap.write"
	HostL2CAPClose  = "l2cap.close"
	HostRFCOMMOpen  = "rfcomm.open"
	HostRFCOMMRead  = "rfcomm.read"
	HostRFCOMMWrite = "rfcomm.write"
	HostRFCOMMClose = "rfcomm.close"
	HostStorageGet  = "storage.get"
	HostStorageSet  = "storage.set"

	HostGATTServices    = "gatt.services"
	HostGATTRead        = "gatt.read"
	HostGATTWrite       = "gatt.write"
	HostGATTSubscribe   = "gatt.subscribe"
	HostGATTNext        = "gatt.next"
	HostGATTUnsubscribe = "gatt.unsubscribe"
)

type Device struct {
	ID               string             `json:"id"`
	Name             string             `json:"name"`
	Paired           bool               `json:"paired"`
	Connected        bool               `json:"connected"`
	RSSI             *int16             `json:"rssi,omitempty"`
	Services         []string           `json:"services,omitempty"`
	ManufacturerData []ManufacturerData `json:"manufacturer_data,omitempty"`
	ServiceData      []ServiceData      `json:"service_data,omitempty"`
	Endpoints        []Endpoint         `json:"endpoints"`
	PossibleMatches  []string           `json:"possible_matches,omitempty"`
}
type Endpoint struct {
	ID        string `json:"id"`
	Transport string `json:"transport"`
	Address   string `json:"address,omitempty"`
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
}

// BatteryValue is the schema-version-1 object form of the standard battery
// property. A single-component device may instead expose a JSON number.
type BatteryValue struct {
	Headset *BatteryComponent `json:"headset,omitempty"`
	Left    *BatteryComponent `json:"left,omitempty"`
	Right   *BatteryComponent `json:"right,omitempty"`
	Case    *BatteryComponent `json:"case,omitempty"`
}

type BatteryComponent struct {
	Level    int    `json:"level"`
	Charging bool   `json:"charging,omitempty"`
	Status   string `json:"status,omitempty"`
}

// Capability describes a named device feature independently of the transport
// used to implement it. A planned capability is deliberately not available:
// clients must not offer reads or writes until a driver reports "available".
type Capability struct {
	Status   string   `json:"status"`
	Kind     string   `json:"type,omitempty"`
	Readable bool     `json:"readable,omitempty"`
	Writable bool     `json:"writable,omitempty"`
	Values   []string `json:"values,omitempty"`
	Minimum  *float64 `json:"minimum,omitempty"`
	Maximum  *float64 `json:"maximum,omitempty"`
	Step     *float64 `json:"step,omitempty"`
	Reason   string   `json:"reason,omitempty"`
}
type Enrichment struct {
	Identification  *Identification       `json:"identification,omitempty"`
	PossibleMatches []string              `json:"possible_matches,omitempty"`
	Properties      map[string]Property   `json:"properties,omitempty"`
	Capabilities    map[string]Capability `json:"capabilities,omitempty"`
}

type L2CAPOpenParams struct {
	DeviceID string `json:"device_id"`
	PSM      uint16 `json:"psm"`
}
type L2CAPChannelParams struct {
	ChannelID string `json:"channel_id"`
}
type L2CAPReadParams struct {
	ChannelID string `json:"channel_id"`
	MaxBytes  int    `json:"max_bytes"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}
type L2CAPWriteParams struct {
	ChannelID string `json:"channel_id"`
	Data      string `json:"data"`
}
type RFCOMMOpenParams struct {
	DeviceID    string `json:"device_id"`
	ServiceUUID string `json:"service_uuid"`
}
type RFCOMMChannelParams struct {
	ChannelID string `json:"channel_id"`
}
type RFCOMMReadParams struct {
	ChannelID string `json:"channel_id"`
	MaxBytes  int    `json:"max_bytes"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}
type RFCOMMWriteParams struct {
	ChannelID string `json:"channel_id"`
	Data      string `json:"data"`
}

// GATTServicesParams names the device whose GATT services to list.
type GATTServicesParams struct {
	DeviceID string `json:"device_id"`
}

// GATTService is one service in a gatt.services result.
type GATTService struct {
	UUID            string   `json:"uuid"`
	Characteristics []string `json:"characteristics"`
}

// GATTCharacteristicParams selects a characteristic. ServiceUUID is needed
// only when the characteristic UUID appears in more than one service.
type GATTCharacteristicParams struct {
	DeviceID           string `json:"device_id"`
	ServiceUUID        string `json:"service_uuid,omitempty"`
	CharacteristicUUID string `json:"characteristic_uuid"`
}

type GATTWriteParams struct {
	GATTCharacteristicParams
	Data            string `json:"data"`
	WithoutResponse bool   `json:"without_response,omitempty"`
}

type GATTNextParams struct {
	SubscriptionID string `json:"subscription_id"`
	TimeoutMS      int    `json:"timeout_ms,omitempty"`
}

type GATTSubscriptionParams struct {
	SubscriptionID string `json:"subscription_id"`
}

type StorageGetParams struct {
	Key string `json:"key"`
}
type StorageSetParams struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}
