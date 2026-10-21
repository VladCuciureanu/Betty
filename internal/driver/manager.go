package driver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"sync"
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
	// OnInspectError, when set, receives each driver failure during inspect.
	// Inspect failures never fail the command, so this is how they surface.
	OnInspectError func(driverID string, err error)
	// OnDriverLog, when set, receives each line a driver writes to stderr,
	// up to 64 KiB per driver process. Otherwise stderr is discarded.
	OnDriverLog func(driverID, line string)
	host        Host
	// reuse keeps driver processes in procs until Close; see SetReuse.
	reuse    bool
	procsMu  sync.Mutex
	procs    map[string]*driverProcess
	rfcomm   RFCOMMHost
	gattHost GATTHost
}

func New() *Manager {
	if root := os.Getenv("BETTY_DRIVER_DIR"); root != "" {
		return &Manager{root: root, bundled: bundledDriverRoot(), systemBundled: systemBundledDriverRoot(), host: UnsupportedHost{}, rfcomm: UnsupportedRFCOMMHost{}, gattHost: UnsupportedGATTHost{}}
	}
	root, _ := os.UserConfigDir()
	return &Manager{root: filepath.Join(root, "betty", "drivers"), bundled: bundledDriverRoot(), systemBundled: systemBundledDriverRoot(), host: UnsupportedHost{}, rfcomm: UnsupportedRFCOMMHost{}, gattHost: UnsupportedGATTHost{}}
}
func NewAt(root string) *Manager {
	return &Manager{root: root, host: UnsupportedHost{}, rfcomm: UnsupportedRFCOMMHost{}, gattHost: UnsupportedGATTHost{}}
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

func (m *Manager) SetGATTHost(host GATTHost) {
	if host == nil {
		m.gattHost = UnsupportedGATTHost{}
		return
	}
	m.gattHost = host
}

func (m *Manager) Inspect(ctx context.Context, devices []domain.Device) []domain.Device {
	return m.InspectFor(ctx, devices, "scan")
}
func (m *Manager) InspectFor(ctx context.Context, devices []domain.Device, purpose string) []domain.Device {
	installed, _ := m.List()
	for _, item := range installed {
		if !matchesAny(item.Match, devices) {
			continue
		}
		response, err := m.call(ctx, item, protocol.Request{Protocol: protocol.Version, Type: protocol.TypeRequest, ID: "1", Method: protocol.MethodInspect, Purpose: purpose, Devices: wireDevices(devices)}, m.inspectBudget())
		if err != nil {
			if m.OnInspectError != nil {
				m.OnInspectError(item.ID, err)
			}
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
	// The selected device and its possible matches decide which drivers
	// may own it.
	related := []string{deviceID}
	for _, d := range devices {
		if d.ID == deviceID {
			related = append(related, d.PossibleMatches...)
		}
	}
	var candidates []domain.Device
	for _, d := range devices {
		if slices.Contains(related, d.ID) || slices.Contains(d.PossibleMatches, deviceID) {
			candidates = append(candidates, d)
		}
	}
	handled := false
	var operationErr error
	for _, item := range installed {
		if !matchesAny(item.Match, candidates) {
			continue
		}
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
		out[name] = domain.DeviceCapability{Status: domain.CapabilityStatus(capability.Status), Kind: domain.ValueKind(capability.Kind), Readable: capability.Readable, Writable: capability.Writable, Values: capability.Values, Minimum: capability.Minimum, Maximum: capability.Maximum, Step: capability.Step, Reason: capability.Reason}
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
		value := p.Value
		if name == "battery" {
			value = batteryObject(value)
		}
		out[name] = domain.Property{Value: value, Unit: p.Unit, Display: p.Display, Estimated: p.Estimated, Resolution: p.Resolution, Verified: p.Verified, Correlated: p.Correlated}
	}
	return out
}

// batteryObject turns a driver's bare battery percentage into the object
// form every client receives, so clients never see two shapes.
func batteryObject(value json.RawMessage) json.RawMessage {
	var level int
	if json.Unmarshal(value, &level) != nil {
		return value
	}
	raw, _ := json.Marshal(protocol.SingleBattery(protocol.BatteryComponent{Level: level}))
	return raw
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
	unlock, err := m.lock()
	if err != nil {
		return manifest, err
	}
	defer unlock()
	// Checked again under the lock: a concurrent install may have finished.
	dir := filepath.Join(m.root, safeID(manifest.ID))
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err == nil {
		return manifest, domain.NewError(domain.CodeUsage, "driver %s is already installed", manifest.ID)
	}
	return manifest, m.place(manifest, source, dir)
}

// Upgrade replaces a user-installed driver with the executable at source,
// which must report the same ID. The driver's private storage is kept.
// Bundled and package-managed drivers are upgraded by their installer.
func (m *Manager) Upgrade(ctx context.Context, source string) (protocol.Manifest, error) {
	manifest, err := readManifest(ctx, source)
	if err != nil {
		return manifest, err
	}
	if err := validateManifest(manifest); err != nil {
		return manifest, err
	}
	unlock, err := m.lock()
	if err != nil {
		return manifest, err
	}
	defer unlock()
	dir := filepath.Join(m.root, safeID(manifest.ID))
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		for _, item := range mustList(m) {
			if item.ID == manifest.ID {
				return manifest, domain.NewError(domain.CodeUnsupported, "driver %s is bundled with Betty or managed by a package manager; upgrade it with that installer", manifest.ID)
			}
		}
		return manifest, domain.NewError(domain.CodeNotFound, "driver %s is not installed; use bt driver install", manifest.ID)
	}
	return manifest, m.place(manifest, source, dir)
}

// place copies the executable at source and its manifest into dir, replacing
// what is there but leaving the driver's storage alone. The caller holds the
// lock.
func (m *Manager) place(manifest protocol.Manifest, source, dir string) error {
	temp, err := os.MkdirTemp(m.root, ".install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	target := filepath.Join(temp, executableName())
	if err := copyExecutable(source, target); err != nil {
		return err
	}
	bytes, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(filepath.Join(temp, "manifest.json"), append(bytes, '\n'), 0o644); err != nil {
		return err
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return os.Rename(temp, dir)
	}
	// The directory exists: it holds storage, and for an upgrade the old
	// driver. Replace the executable, then the manifest that names it.
	if err := os.Rename(target, filepath.Join(dir, executableName())); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(temp, "manifest.json"), filepath.Join(dir, "manifest.json")); err != nil {
		return err
	}
	return nil
}
func (m *Manager) Remove(id string) error {
	path := filepath.Join(m.root, safeID(id))
	if _, err := os.Stat(filepath.Join(path, "manifest.json")); err == nil {
		unlock, err := m.lock()
		if err != nil {
			return err
		}
		defer unlock()
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
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	bytes, err := io.ReadAll(io.LimitReader(file, maxStorageBytes+1))
	if err != nil {
		return err
	}
	if len(bytes) > maxStorageBytes {
		return domain.NewError(domain.CodeUsage, "driver state exceeds 1 MiB")
	}
	values := map[string]string{}
	if err := json.Unmarshal(bytes, &values); err != nil {
		return domain.NewError(domain.CodeUsage, "invalid driver state: %v", err)
	}
	if len(values) > maxStorageKeys {
		return domain.NewError(domain.CodeUsage, "driver state has more than %d keys", maxStorageKeys)
	}
	for key, value := range values {
		if err := validateStorageKey(key); err != nil {
			return err
		}
		if len(value) > maxStorageValue {
			return domain.NewError(domain.CodeUsage, "stored value for %q exceeds 64 KiB", key)
		}
	}
	unlock, err := m.lock()
	if err != nil {
		return err
	}
	defer unlock()
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
	return value, ok, nil
}
func (m *Manager) storageSet(driver protocol.Manifest, key, value string) error {
	if err := validateStorageKey(key); err != nil {
		return err
	}
	if len(value) > maxStorageValue {
		return domain.NewError(domain.CodeUsage, "stored value exceeds 64 KiB")
	}
	unlock, err := m.lock()
	if err != nil {
		return err
	}
	defer unlock()
	values, err := m.readStorage(driver.ID)
	if err != nil {
		return err
	}
	if _, exists := values[key]; !exists && len(values) >= maxStorageKeys {
		return domain.NewError(domain.CodeUsage, "driver storage is limited to %d keys", maxStorageKeys)
	}
	values[key] = value
	bytes, err := json.Marshal(values)
	if err != nil {
		return err
	}
	if len(bytes) > maxStorageBytes {
		return domain.NewError(domain.CodeUsage, "driver storage is limited to 1 MiB")
	}
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

// Driver storage limits, shared by host calls and state import.
const (
	maxStorageKeys  = 256
	maxStorageValue = 64 << 10
	maxStorageBytes = 1 << 20
)

var storageKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9._/-]+$`)

func validateStorageKey(key string) error {
	if len(key) < 1 || len(key) > 128 || !storageKeyPattern.MatchString(key) {
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
	if m.Match != nil {
		for _, uuid := range m.Match.ServiceUUIDs {
			if !validUUID(uuid) {
				return domain.NewError(domain.CodeUsage, "invalid service UUID %q in the manifest's match hints", uuid)
			}
		}
	}
	return nil
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
