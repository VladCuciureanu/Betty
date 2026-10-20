package domain

// Adapter is a local Bluetooth controller. A field the platform cannot report
// is omitted.
type Adapter struct {
	// ID names the adapter on this platform, for example "hci0".
	ID           string `json:"id"`
	Name         string `json:"name,omitempty"`
	Address      string `json:"address,omitempty"`
	Powered      *bool  `json:"powered,omitempty"`
	Discoverable *bool  `json:"discoverable,omitempty"`
	Pairable     *bool  `json:"pairable,omitempty"`
	// Default marks the adapter commands use.
	Default bool `json:"default"`
}

// AdapterChange lists the adapter settings to change; nil fields stay as they
// are.
type AdapterChange struct {
	Powered      *bool
	Discoverable *bool
	Name         *string
}
