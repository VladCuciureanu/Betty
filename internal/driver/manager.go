package driver

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"time"

	protocol "github.com/vladcuciureanu/betty/driver"
	"github.com/vladcuciureanu/betty/internal/domain"
)

type Manager struct {
	root          string
	bundled       string
	systemBundled string
	// Zero values select the defaults in process.go; tests shorten them.
	inspectTimeout time.Duration
	exitGrace      time.Duration
	host           Host
	rfcomm         RFCOMMHost
}

func New() *Manager {
	if root := os.Getenv("BETTY_DRIVER_DIR"); root != "" {
		return &Manager{root: root, bundled: bundledDriverRoot(), systemBundled: systemBundledDriverRoot(), host: UnsupportedHost{}, rfcomm: UnsupportedRFCOMMHost{}}
	}
	root, _ := os.UserConfigDir()
	return &Manager{root: filepath.Join(root, "betty", "drivers"), bundled: bundledDriverRoot(), systemBundled: systemBundledDriverRoot(), host: UnsupportedHost{}, rfcomm: UnsupportedRFCOMMHost{}}
}
func NewAt(root string) *Manager {
	return &Manager{root: root, host: UnsupportedHost{}, rfcomm: UnsupportedRFCOMMHost{}}
}
func (m *Manager) SetHost(host Host) {
	if host == nil {
		m.host = UnsupportedHost{}
		return
	}
	m.host = host
}
func (m *Manager) SetRFCOMMHost(host RFCOMMHost) {
	if host == nil {
		m.rfcomm = UnsupportedRFCOMMHost{}
		return
	}
	m.rfcomm = host
}

func (m *Manager) Inspect(ctx context.Context, devices []domain.Device) []domain.Device {
	return m.InspectFor(ctx, devices, "scan")
}
func (m *Manager) InspectFor(ctx context.Context, devices []domain.Device, purpose string) []domain.Device {
	installed, _ := m.List()
	for _, item := range installed {
		response, err := m.call(ctx, item, protocol.Request{Protocol: protocol.Version, Type: protocol.TypeRequest, ID: "1", Method: protocol.MethodInspect, Purpose: purpose, Devices: wireDevices(devices)}, m.inspectBudget())
		if err != nil {
			continue
		}
		devices = applyEnrichments(devices, response.Enrichments)
	}
	return devices
}

// Property asks installed drivers to perform a generic property operation.
// Drivers that do not own the selected device return no enrichment.
func (m *Manager) Property(ctx context.Context, devices []domain.Device, method, deviceID, property string, value json.RawMessage) ([]domain.Device, bool, error) {
	installed, err := m.List()
	if err != nil {
		return devices, false, err
	}
	handled := false
	var operationErr error
	for _, item := range installed {
		response, callErr := m.call(ctx, item, protocol.Request{Protocol: protocol.Version, Type: protocol.TypeRequest, ID: "1", Method: method, DeviceID: deviceID, Property: property, Value: value, Devices: wireDevices(devices)}, 0)
		if callErr != nil {
			operationErr = callErr
			continue
		}
		if _, ok := response.Enrichments[deviceID]; ok {
			handled = true
		}
		devices = applyEnrichments(devices, response.Enrichments)
	}
	if !handled && operationErr != nil {
		return devices, false, operationErr
	}
	return devices, handled, nil
}

func applyEnrichments(devices []domain.Device, enrichments map[string]protocol.Enrichment) []domain.Device {
	for i := range devices {
		enrichment, ok := enrichments[devices[i].ID]
		if !ok {
			continue
		}
		if enrichment.Identification != nil {
			devices[i].Identification = &domain.Identification{Vendor: enrichment.Identification.Vendor, Family: enrichment.Identification.Family, Model: enrichment.Identification.Model, Confidence: enrichment.Identification.Confidence}
			if len(devices[i].Endpoints) > 0 && devices[i].Name == devices[i].Endpoints[0].Address && enrichment.Identification.Model != "" {
				devices[i].Name = enrichment.Identification.Model
			}
		}
		if enrichment.PossibleMatches != nil {
			devices[i].PossibleMatches = enrichment.PossibleMatches
		}
		if len(enrichment.Properties) > 0 {
			if devices[i].Properties == nil {
				devices[i].Properties = map[string]domain.Property{}
			}
			for name, property := range domainProperties(enrichment.Properties) {
				devices[i].Properties[name] = property
			}
		}
		if len(enrichment.Capabilities) > 0 {
			if devices[i].Capabilities == nil {
				devices[i].Capabilities = map[string]domain.DeviceCapability{}
			}
			for name, capability := range domainCapabilities(enrichment.Capabilities) {
				devices[i].Capabilities[name] = capability
			}
		}
	}
	return devices
}
func domainCapabilities(capabilities map[string]protocol.Capability) map[string]domain.DeviceCapability {
	out := map[string]domain.DeviceCapability{}
	for name, capability := range capabilities {
		out[name] = domain.DeviceCapability{Status: capability.Status, Kind: capability.Kind, Readable: capability.Readable, Writable: capability.Writable, Values: capability.Values, Minimum: capability.Minimum, Maximum: capability.Maximum, Step: capability.Step, Reason: capability.Reason}
	}
	return out
}
func wireDevices(devices []domain.Device) []protocol.Device {
	out := make([]protocol.Device, 0, len(devices))
	for _, d := range devices {
		wire := protocol.Device{ID: d.ID, Name: d.Name, Paired: d.Paired, Connected: d.Connected, RSSI: d.RSSI, Services: d.Services, PossibleMatches: d.PossibleMatches}
		for _, e := range d.Endpoints {
			wire.Endpoints = append(wire.Endpoints, protocol.Endpoint{ID: e.ID, Transport: string(e.Transport), Address: e.Address})
		}
		for _, m := range d.ManufacturerData {
			wire.ManufacturerData = append(wire.ManufacturerData, protocol.ManufacturerData{CompanyID: m.CompanyID, Data: m.Data})
		}
		for _, s := range d.ServiceData {
			wire.ServiceData = append(wire.ServiceData, protocol.ServiceData{UUID: s.UUID, Data: s.Data})
		}
		out = append(out, wire)
	}
	return out
}
func domainProperties(properties map[string]protocol.Property) map[string]domain.Property {
	out := map[string]domain.Property{}
	for name, p := range properties {
		out[name] = domain.Property{Value: p.Value, Unit: p.Unit, Display: p.Display, Estimated: p.Estimated, Resolution: p.Resolution}
	}
	return out
}

func (m *Manager) Install(ctx context.Context, source string) (protocol.Manifest, error) {
	manifest, err := readManifest(ctx, source)
	if err != nil {
		return manifest, err
	}
	if err := validateManifest(manifest); err != nil {
		return manifest, err
	}
	// Refuse an ID that any source already provides. A user-installed driver
	// takes precedence, so installing a second copy would silently shadow a
	// bundled or package-managed driver and inherit its private storage.
	available, err := m.List()
	if err != nil {
		return manifest, err
	}
	for _, item := range available {
		if item.ID == manifest.ID {
			return manifest, domain.NewError(domain.CodeUsage, "driver %s is already installed", manifest.ID)
		}
	}
	dir := filepath.Join(m.root, safeID(manifest.ID))
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err == nil {
		return manifest, domain.NewError(domain.CodeUsage, "driver %s is already installed", manifest.ID)
	}
	if err := os.MkdirAll(m.root, 0o755); err != nil {
		return manifest, err
	}
	temp, err := os.MkdirTemp(m.root, ".install-")
	if err != nil {
		return manifest, err
	}
	defer os.RemoveAll(temp)
	target := filepath.Join(temp, executableName())
	if err := copyExecutable(source, target); err != nil {
		return manifest, err
	}
	bytes, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(filepath.Join(temp, "manifest.json"), append(bytes, '\n'), 0o644); err != nil {
		return manifest, err
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err := os.Rename(temp, dir); err != nil {
			return manifest, err
		}
	} else {
		if err := os.Rename(target, filepath.Join(dir, executableName())); err != nil {
			return manifest, err
		}
		if err := os.Rename(filepath.Join(temp, "manifest.json"), filepath.Join(dir, "manifest.json")); err != nil {
			_ = os.Remove(filepath.Join(dir, executableName()))
			return manifest, err
		}
	}
	return manifest, nil
}
func (m *Manager) Remove(id string) error {
	path := filepath.Join(m.root, safeID(id))
	if _, err := os.Stat(filepath.Join(path, "manifest.json")); err == nil {
		return os.RemoveAll(path)
	}
	for _, root := range m.bundledRoots() {
		if _, err := os.Stat(filepath.Join(root, safeID(id), "manifest.json")); err == nil {
			return domain.NewError(domain.CodeUnsupported, "driver %q is bundled with Betty and is removed by the application installer", id)
		}
	}
	if executable, ok := officialPathDrivers()[id]; ok {
		if _, err := exec.LookPath(executable); err == nil {
			return domain.NewError(domain.CodeUnsupported, "driver %q is managed by the system package manager", id)
		}
	}
	return domain.NewError(domain.CodeNotFound, "driver %q is not installed", id)
}
func (m *Manager) List() ([]protocol.Manifest, error) {
	byID := map[string]protocol.Manifest{}
	for _, root := range append(m.bundledRoots(), m.root) {
		if root == "" {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			bytes, e := os.ReadFile(filepath.Join(root, entry.Name(), "manifest.json"))
			if e != nil {
				continue
			}
			var item protocol.Manifest
			if json.Unmarshal(bytes, &item) == nil {
				byID[item.ID] = item
			}
		}
	}
	for id, executable := range officialPathDrivers() {
		path, err := exec.LookPath(executable)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		item, err := readManifest(ctx, path)
		cancel()
		if err == nil && item.ID == id && validateManifest(item) == nil {
			if _, exists := byID[id]; !exists {
				byID[id] = item
			}
		}
	}
	out := make([]protocol.Manifest, 0, len(byID))
	for _, item := range byID {
		for _, legacyID := range honoredLegacyIDs(item) {
			delete(byID, legacyID)
		}
	}
	for _, item := range byID {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ExportState writes an installed driver's opaque namespaced state for transfer
// to another Betty installation. The destination is always user-readable only.
func (m *Manager) ExportState(id, destination string) error {
	if !m.installed(id) {
		return domain.NewError(domain.CodeNotFound, "driver %q is not installed", id)
	}
	values, err := m.readStorage(id)
	if err != nil {
		return err
	}
	bytes, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	_, writeErr := file.Write(append(bytes, '\n'))
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

// ImportState replaces an installed driver's opaque state with a previously
// exported file after validating the same storage limits used for host calls.
func (m *Manager) ImportState(id, source string) error {
	if !m.installed(id) {
		return domain.NewError(domain.CodeNotFound, "driver %q is not installed", id)
	}
	bytes, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if len(bytes) > 1<<20 {
		return domain.NewError(domain.CodeUsage, "driver state exceeds 1 MiB")
	}
	values := map[string]string{}
	if err := json.Unmarshal(bytes, &values); err != nil {
		return domain.NewError(domain.CodeUsage, "invalid driver state: %v", err)
	}
	for key, value := range values {
		if err := validateStorageKey(key); err != nil {
			return err
		}
		if len(value) > 65536 {
			return domain.NewError(domain.CodeUsage, "stored value for %q exceeds 64 KiB", key)
		}
	}
	return m.writeStorage(id, values)
}

func (m *Manager) installed(id string) bool {
	for _, item := range mustList(m) {
		if item.ID == id {
			return true
		}
	}
	return false
}
func mustList(m *Manager) []protocol.Manifest { items, _ := m.List(); return items }
func driverErrorCode(value string) domain.ErrorCode {
	code := domain.ErrorCode(value)
	switch code {
	case domain.CodeUsage, domain.CodeNotFound, domain.CodeAmbiguous, domain.CodeUnsupported,
		domain.CodePermission, domain.CodeUnavailable, domain.CodeAuthentication,
		domain.CodeInteraction, domain.CodeDisconnected, domain.CodeDriverRequired,
		domain.CodeTimeout, domain.CodeInternal:
		return code
	default:
		return domain.CodeInternal
	}
}

func (m *Manager) handleHost(ctx context.Context, driver protocol.Manifest, message protocol.Message, allowedDevices map[string]bool, channels map[string]string) (json.RawMessage, error) {
	marshal := func(value any) (json.RawMessage, error) { bytes, err := json.Marshal(value); return bytes, err }
	required := ""
	if len(message.Method) >= 6 && message.Method[:6] == "l2cap." {
		required = protocol.PermissionL2CAP
	}
	if len(message.Method) >= 8 && message.Method[:8] == "storage." {
		required = protocol.PermissionStorage
	}
	if len(message.Method) >= 7 && message.Method[:7] == "rfcomm." {
		required = protocol.PermissionRFCOMM
	}
	if required != "" && !hasPermission(driver, required) {
		return nil, domain.NewError(domain.CodePermission, "driver %s lacks %s permission", driver.ID, required)
	}
	switch message.Method {
	case protocol.HostL2CAPOpen:
		var p protocol.L2CAPOpenParams
		if err := json.Unmarshal(message.Params, &p); err != nil {
			return nil, err
		}
		if !allowedDevices[p.DeviceID] {
			return nil, domain.NewError(domain.CodePermission, "driver cannot access device %q", p.DeviceID)
		}
		channel, err := m.host.OpenL2CAP(ctx, p.DeviceID, p.PSM)
		if err != nil {
			return nil, err
		}
		channels[channel] = "l2cap"
		return marshal(map[string]string{"channel_id": channel})
	case protocol.HostL2CAPRead:
		var p protocol.L2CAPReadParams
		if err := json.Unmarshal(message.Params, &p); err != nil {
			return nil, err
		}
		if channels[p.ChannelID] != "l2cap" {
			return nil, domain.NewError(domain.CodePermission, "driver does not own L2CAP channel")
		}
		if p.MaxBytes < 1 || p.MaxBytes > 65535 {
			return nil, domain.NewError(domain.CodeUsage, "max_bytes must be between 1 and 65535")
		}
		readCtx := ctx
		cancel := func() {}
		if p.TimeoutMS > 0 {
			if p.TimeoutMS > 30000 {
				return nil, domain.NewError(domain.CodeUsage, "timeout_ms must not exceed 30000")
			}
			readCtx, cancel = context.WithTimeout(ctx, time.Duration(p.TimeoutMS)*time.Millisecond)
		}
		defer cancel()
		data, err := m.host.ReadL2CAP(readCtx, p.ChannelID, p.MaxBytes)
		if err != nil {
			return nil, err
		}
		return marshal(map[string]string{"data": hex.EncodeToString(data)})
	case protocol.HostL2CAPWrite:
		var p protocol.L2CAPWriteParams
		if err := json.Unmarshal(message.Params, &p); err != nil {
			return nil, err
		}
		if channels[p.ChannelID] != "l2cap" {
			return nil, domain.NewError(domain.CodePermission, "driver does not own L2CAP channel")
		}
		data, err := hex.DecodeString(p.Data)
		if err != nil {
			return nil, domain.NewError(domain.CodeUsage, "invalid hexadecimal L2CAP data")
		}
		written, err := m.host.WriteL2CAP(ctx, p.ChannelID, data)
		if err != nil {
			return nil, err
		}
		return marshal(map[string]int{"written": written})
	case protocol.HostL2CAPClose:
		var p protocol.L2CAPChannelParams
		if err := json.Unmarshal(message.Params, &p); err != nil {
			return nil, err
		}
		if channels[p.ChannelID] != "l2cap" {
			return nil, domain.NewError(domain.CodePermission, "driver does not own L2CAP channel")
		}
		if err := m.host.CloseL2CAP(ctx, p.ChannelID); err != nil {
			return nil, err
		}
		delete(channels, p.ChannelID)
		return marshal(map[string]bool{"closed": true})
	case protocol.HostRFCOMMOpen:
		var p protocol.RFCOMMOpenParams
		if err := json.Unmarshal(message.Params, &p); err != nil {
			return nil, err
		}
		if !allowedDevices[p.DeviceID] {
			return nil, domain.NewError(domain.CodePermission, "driver cannot access device %q", p.DeviceID)
		}
		channel, err := m.rfcomm.OpenRFCOMM(ctx, p.DeviceID, p.ServiceUUID)
		if err != nil {
			return nil, err
		}
		channels[channel] = "rfcomm"
		return marshal(map[string]string{"channel_id": channel})
	case protocol.HostRFCOMMRead:
		var p protocol.RFCOMMReadParams
		if err := json.Unmarshal(message.Params, &p); err != nil {
			return nil, err
		}
		if channels[p.ChannelID] != "rfcomm" {
			return nil, domain.NewError(domain.CodePermission, "driver does not own RFCOMM channel")
		}
		if p.MaxBytes < 1 || p.MaxBytes > 65535 {
			return nil, domain.NewError(domain.CodeUsage, "max_bytes must be between 1 and 65535")
		}
		readCtx := ctx
		cancel := func() {}
		if p.TimeoutMS > 0 {
			if p.TimeoutMS > 30000 {
				return nil, domain.NewError(domain.CodeUsage, "timeout_ms must not exceed 30000")
			}
			readCtx, cancel = context.WithTimeout(ctx, time.Duration(p.TimeoutMS)*time.Millisecond)
		}
		defer cancel()
		data, err := m.rfcomm.ReadRFCOMM(readCtx, p.ChannelID, p.MaxBytes)
		if err != nil {
			return nil, err
		}
		return marshal(map[string]string{"data": hex.EncodeToString(data)})
	case protocol.HostRFCOMMWrite:
		var p protocol.RFCOMMWriteParams
		if err := json.Unmarshal(message.Params, &p); err != nil {
			return nil, err
		}
		if channels[p.ChannelID] != "rfcomm" {
			return nil, domain.NewError(domain.CodePermission, "driver does not own RFCOMM channel")
		}
		data, err := hex.DecodeString(p.Data)
		if err != nil {
			return nil, domain.NewError(domain.CodeUsage, "invalid hexadecimal RFCOMM data")
		}
		written, err := m.rfcomm.WriteRFCOMM(ctx, p.ChannelID, data)
		if err != nil {
			return nil, err
		}
		return marshal(map[string]int{"written": written})
	case protocol.HostRFCOMMClose:
		var p protocol.RFCOMMChannelParams
		if err := json.Unmarshal(message.Params, &p); err != nil {
			return nil, err
		}
		if channels[p.ChannelID] != "rfcomm" {
			return nil, domain.NewError(domain.CodePermission, "driver does not own RFCOMM channel")
		}
		if err := m.rfcomm.CloseRFCOMM(ctx, p.ChannelID); err != nil {
			return nil, err
		}
		delete(channels, p.ChannelID)
		return marshal(map[string]bool{"closed": true})
	case protocol.HostStorageGet:
		var p protocol.StorageGetParams
		if err := json.Unmarshal(message.Params, &p); err != nil {
			return nil, err
		}
		value, found, err := m.storageGet(driver, p.Key)
		if err != nil {
			return nil, err
		}
		return marshal(map[string]any{"value": value, "found": found})
	case protocol.HostStorageSet:
		var p protocol.StorageSetParams
		if err := json.Unmarshal(message.Params, &p); err != nil {
			return nil, err
		}
		if err := m.storageSet(driver, p.Key, p.Value); err != nil {
			return nil, err
		}
		return marshal(map[string]bool{"stored": true})
	default:
		return nil, domain.NewError(domain.CodeUnsupported, "unsupported host method %q", message.Method)
	}
}
func hasPermission(driver protocol.Manifest, want string) bool {
	for _, permission := range driver.Permissions {
		if permission == want {
			return true
		}
	}
	return false
}

func (m *Manager) storageGet(driver protocol.Manifest, key string) (string, bool, error) {
	if err := validateStorageKey(key); err != nil {
		return "", false, err
	}
	values, err := m.readStorage(driver.ID)
	if err != nil {
		return "", false, err
	}
	value, ok := values[key]
	if !ok {
		for _, legacyID := range honoredLegacyIDs(driver) {
			legacyValues, legacyErr := m.readStorage(legacyID)
			if legacyErr != nil {
				return "", false, legacyErr
			}
			if value, ok = legacyValues[key]; ok {
				break
			}
		}
	}
	return value, ok, nil
}
func (m *Manager) storageSet(driver protocol.Manifest, key, value string) error {
	if err := validateStorageKey(key); err != nil {
		return err
	}
	if len(value) > 65536 {
		return domain.NewError(domain.CodeUsage, "stored value exceeds 64 KiB")
	}
	values, err := m.readStorage(driver.ID)
	if err != nil {
		return err
	}
	if len(values) == 0 {
		for _, legacyID := range honoredLegacyIDs(driver) {
			legacyValues, legacyErr := m.readStorage(legacyID)
			if legacyErr != nil {
				return legacyErr
			}
			if len(legacyValues) > 0 {
				values = legacyValues
				break
			}
		}
	}
	values[key] = value
	return m.writeStorage(driver.ID, values)
}
func (m *Manager) writeStorage(driverID string, values map[string]string) error {
	bytes, err := json.Marshal(values)
	if err != nil {
		return err
	}
	dir := filepath.Join(m.root, safeID(driverID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".storage-")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if _, err = temp.Write(bytes); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(dir, "storage.json"))
}
func (m *Manager) readStorage(driverID string) (map[string]string, error) {
	values := map[string]string{}
	bytes, err := os.ReadFile(filepath.Join(m.root, safeID(driverID), "storage.json"))
	if os.IsNotExist(err) {
		return values, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(bytes, &values); err != nil {
		return nil, err
	}
	return values, nil
}
func validateStorageKey(key string) error {
	if len(key) < 1 || len(key) > 128 || !regexp.MustCompile(`^[a-zA-Z0-9._/-]+$`).MatchString(key) {
		return domain.NewError(domain.CodeUsage, "invalid storage key")
	}
	return nil
}
func validateManifest(m protocol.Manifest) error {
	if m.Protocol != protocol.Version {
		return domain.NewError(domain.CodeUnsupported, "driver uses protocol %d; Betty supports %d", m.Protocol, protocol.Version)
	}
	if !protocol.ValidID(m.ID) {
		return domain.NewError(domain.CodeUsage, "invalid driver ID")
	}
	for _, legacyID := range m.LegacyIDs {
		if !protocol.ValidID(legacyID) || legacyID == m.ID {
			return domain.NewError(domain.CodeUsage, "invalid legacy driver ID")
		}
		if !slices.Contains(knownRenames[m.ID], legacyID) {
			return domain.NewError(domain.CodeUsage, "driver %s declares legacy ID %s, which Betty does not recognize as an earlier name of this driver", m.ID, legacyID)
		}
	}
	return nil
}

// knownRenames lists the earlier IDs Betty accepts for each driver. A
// manifest's legacy_ids is self-declared, so honoring arbitrary entries would
// let any driver read another driver's private storage and hide that driver.
var knownRenames = map[string][]string{
	"official/apple-audio": {"official/airpods"},
}

// honoredLegacyIDs returns the legacy IDs of item that Betty recognizes.
func honoredLegacyIDs(item protocol.Manifest) []string {
	var honored []string
	for _, legacyID := range item.LegacyIDs {
		if legacyID != item.ID && slices.Contains(knownRenames[item.ID], legacyID) {
			honored = append(honored, legacyID)
		}
	}
	return honored
}
func safeID(id string) string { return base64.RawURLEncoding.EncodeToString([]byte(id)) }
func executableName() string {
	if runtime.GOOS == "windows" {
		return "driver.exe"
	}
	return "driver"
}
func bundledDriverRoot() string {
	if root := os.Getenv("BETTY_BUNDLED_DRIVER_DIR"); root != "" {
		return root
	}
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(executable), "drivers")
}
func (m *Manager) driverPath(id string) string {
	user := filepath.Join(m.root, safeID(id), executableName())
	if _, err := os.Stat(user); err == nil {
		return user
	}
	for _, root := range m.bundledRoots() {
		path := filepath.Join(root, safeID(id), executableName())
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	if executable, ok := officialPathDrivers()[id]; ok {
		if path, err := exec.LookPath(executable); err == nil {
			return path
		}
	}
	return filepath.Join(m.bundled, safeID(id), executableName())
}

func officialPathDrivers() map[string]string {
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	return map[string]string{
		"official/apple-audio":     "bt-driver-apple-audio" + suffix,
		"official/sony-headphones": "bt-driver-sony" + suffix,
	}
}

func (m *Manager) bundledRoots() []string {
	var roots []string
	if m.bundled != "" {
		roots = append(roots, m.bundled)
	}
	if m.systemBundled != "" && m.systemBundled != m.bundled {
		roots = append(roots, m.systemBundled)
	}
	return roots
}
func copyExecutable(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
