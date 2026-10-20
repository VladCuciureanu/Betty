package driver

// Methods Betty sends to a driver in a Request.
const (
	MethodInspect = "inspect"
	MethodGet     = "get"
	MethodSet     = "set"
)

// Purposes describe why Betty sent an inspect request. Drivers must treat an
// unrecognized purpose like PurposeScan.
const (
	PurposeScan = "scan"
	PurposeList = "list"
)

// Permissions a manifest may declare. Betty refuses host calls whose
// permission is missing from the manifest. PermissionDeviceMetadata is
// declarative: every driver receives device metadata with each request.
const (
	PermissionDeviceMetadata = "device-metadata"
	PermissionL2CAP          = "l2cap"
	PermissionRFCOMM         = "rfcomm"
	PermissionStorage        = "driver-storage"
)

// Capability statuses. Clients must not offer reads or writes for a capability
// that is not StatusAvailable.
const (
	StatusAvailable   = "available"
	StatusPlanned     = "planned"
	StatusUnsupported = "unsupported"
)

// Capability value types.
const (
	KindBattery = "battery"
	KindEnum    = "enum"
	KindBoolean = "boolean"
	KindInteger = "integer"
	KindNumber  = "number"
	KindString  = "string"
)

// Identification confidence, from weakest to strongest evidence.
const (
	ConfidenceName     = "name"
	ConfidenceModel    = "model"
	ConfidenceProtocol = "protocol"
)

// ErrorCode is a stable, machine-readable failure category. Betty maps an
// unrecognized code to CodeInternal.
type ErrorCode string

const (
	CodeUsage                ErrorCode = "usage"
	CodeDeviceNotFound       ErrorCode = "device_not_found"
	CodeDeviceAmbiguous      ErrorCode = "device_ambiguous"
	CodeUnsupported          ErrorCode = "unsupported"
	CodePermissionDenied     ErrorCode = "permission_denied"
	CodeBluetoothUnavailable ErrorCode = "bluetooth_unavailable"
	CodeAuthenticationFailed ErrorCode = "authentication_failed"
	CodeInteractionRequired  ErrorCode = "interaction_required"
	CodeDeviceDisconnected   ErrorCode = "device_disconnected"
	CodeDriverRequired       ErrorCode = "driver_required"
	CodeTimeout              ErrorCode = "timeout"
	CodeInternal             ErrorCode = "internal"
)

// ErrorCodes lists every code defined by protocol version 1.
var ErrorCodes = []ErrorCode{
	CodeUsage, CodeDeviceNotFound, CodeDeviceAmbiguous, CodeUnsupported,
	CodePermissionDenied, CodeBluetoothUnavailable, CodeAuthenticationFailed,
	CodeInteractionRequired, CodeDeviceDisconnected, CodeDriverRequired,
	CodeTimeout, CodeInternal,
}
