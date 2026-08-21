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
}
type Request struct {
	Protocol int      `json:"protocol"`
	Type     string   `json:"type,omitempty"`
	ID       string   `json:"id,omitempty"`
	Method   string   `json:"method"`
	Purpose  string   `json:"purpose,omitempty"`
	Devices  []Device `json:"devices"`
}
type Response struct {
	Protocol    int                   `json:"protocol"`
	Type        string                `json:"type,omitempty"`
	ID          string                `json:"id,omitempty"`
	Enrichments map[string]Enrichment `json:"enrichments,omitempty"`
	Error       string                `json:"error,omitempty"`
}
type Message struct {
	Protocol int             `json:"protocol"`
	Type     string          `json:"type"`
	ID       string          `json:"id"`
	Method   string          `json:"method,omitempty"`
	Params   json.RawMessage `json:"params,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    string          `json:"error,omitempty"`
}

const (
	TypeRequest      = "request"
	TypeResponse     = "response"
	TypeHostRequest  = "host_request"
	TypeHostResponse = "host_response"
)
const (
	HostL2CAPOpen  = "l2cap.open"
	HostL2CAPRead  = "l2cap.read"
	HostL2CAPWrite = "l2cap.write"
	HostL2CAPClose = "l2cap.close"
	HostStorageGet = "storage.get"
	HostStorageSet = "storage.set"
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

// Capability describes a named device feature independently of the transport
// used to implement it. A planned capability is deliberately not available:
// clients must not offer reads or writes until a driver reports "available".
type Capability struct {
	Status   string   `json:"status"`
	Readable bool     `json:"readable,omitempty"`
	Writable bool     `json:"writable,omitempty"`
	Values   []string `json:"values,omitempty"`
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
type StorageGetParams struct {
	Key string `json:"key"`
}
type StorageSetParams struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}
