package domain

import "time"

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
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Paired      bool              `json:"paired"`
	Connected   bool              `json:"connected"`
	RSSI        *int16            `json:"rssi,omitempty"`
	Services    []string          `json:"services,omitempty"`
	Information map[string]string `json:"information,omitempty"`
	Endpoints   []Endpoint        `json:"endpoints"`
	LastSeenAt  *time.Time        `json:"last_seen_at,omitempty"`
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
