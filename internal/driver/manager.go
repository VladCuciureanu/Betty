package driver

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"time"

	protocol "github.com/vladcuciureanu/betty/driver"
	"github.com/vladcuciureanu/betty/internal/domain"
)

type Manager struct {
	root          string
	bundled       string
	systemBundled string
	host          Host
	rfcomm        RFCOMMHost
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
		response, err := m.call(ctx, item, protocol.Request{Protocol: protocol.Version, Type: protocol.TypeRequest, ID: "1", Method: "inspect", Purpose: purpose, Devices: wireDevices(devices)})
		if err != nil {
			continue
		}
		for i := range devices {
			enrichment, ok := response.Enrichments[devices[i].ID]
			if !ok {
				continue
			}
			if enrichment.Identification != nil {
				devices[i].Identification = &domain.Identification{Vendor: enrichment.Identification.Vendor, Family: enrichment.Identification.Family, Model: enrichment.Identification.Model, Confidence: enrichment.Identification.Confidence}
				if len(devices[i].Endpoints) > 0 && devices[i].Name == devices[i].Endpoints[0].Address && enrichment.Identification.Model != "" {
					devices[i].Name = enrichment.Identification.Model
				}
			}
			devices[i].PossibleMatches = enrichment.PossibleMatches
			if len(enrichment.Properties) > 0 {
				devices[i].Properties = domainProperties(enrichment.Properties)
			}
			if len(enrichment.Capabilities) > 0 {
				devices[i].Capabilities = domainCapabilities(enrichment.Capabilities)
			}
		}
	}
	return devices
}
func domainCapabilities(capabilities map[string]protocol.Capability) map[string]domain.DeviceCapability {
	out := map[string]domain.DeviceCapability{}
	for name, capability := range capabilities {
		out[name] = domain.DeviceCapability{Status: capability.Status, Readable: capability.Readable, Writable: capability.Writable, Values: capability.Values, Reason: capability.Reason}
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
func (m *Manager) call(ctx context.Context, item protocol.Manifest, request protocol.Request) (protocol.Response, error) {
	var response protocol.Response
	path := m.driverPath(item.ID)
	cmd := exec.CommandContext(ctx, path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return response, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return response, err
	}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		return response, err
	}
	encoder, decoder := json.NewEncoder(stdin), json.NewDecoder(stdout)
	if err := encoder.Encode(request); err != nil {
		return response, err
	}
	allowedDevices := map[string]bool{}
	for _, device := range request.Devices {
		allowedDevices[device.ID] = true
	}
	channels := map[string]string{}
	defer func() {
		for channel, transport := range channels {
			switch transport {
			case "l2cap":
				_ = m.host.CloseL2CAP(context.Background(), channel)
			case "rfcomm":
				_ = m.rfcomm.CloseRFCOMM(context.Background(), channel)
			}
		}
	}()
	for {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return response, err
		}
		var header protocol.Message
		if err := json.Unmarshal(raw, &header); err != nil {
			return response, err
		}
		if header.Type == protocol.TypeHostRequest {
			result, hostErr := m.handleHost(ctx, item, header, allowedDevices, channels)
			reply := protocol.Message{Protocol: protocol.Version, Type: protocol.TypeHostResponse, ID: header.ID, Result: result}
			if hostErr != nil {
				reply.Error = hostErr.Error()
			}
			if err := encoder.Encode(reply); err != nil {
				return response, err
			}
			continue
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			return response, err
		}
		break
	}
	_ = stdin.Close()
	waitErr := cmd.Wait()
	if waitErr != nil {
		return response, waitErr
	}
	if response.Protocol != protocol.Version {
		return response, fmt.Errorf("driver %s returned protocol %d", item.ID, response.Protocol)
	}
	if response.Error != "" {
		return response, fmt.Errorf("driver %s: %s", item.ID, response.Error)
	}
	return response, nil
}

func (m *Manager) handleHost(ctx context.Context, driver protocol.Manifest, message protocol.Message, allowedDevices map[string]bool, channels map[string]string) (json.RawMessage, error) {
	marshal := func(value any) (json.RawMessage, error) { bytes, err := json.Marshal(value); return bytes, err }
	required := ""
	if len(message.Method) >= 6 && message.Method[:6] == "l2cap." {
		required = "l2cap"
	}
	if len(message.Method) >= 8 && message.Method[:8] == "storage." {
		required = "driver-storage"
	}
	if len(message.Method) >= 7 && message.Method[:7] == "rfcomm." {
		required = "rfcomm"
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
		value, found, err := m.storageGet(driver.ID, p.Key)
		if err != nil {
			return nil, err
		}
		return marshal(map[string]any{"value": value, "found": found})
	case protocol.HostStorageSet:
		var p protocol.StorageSetParams
		if err := json.Unmarshal(message.Params, &p); err != nil {
			return nil, err
		}
		if err := m.storageSet(driver.ID, p.Key, p.Value); err != nil {
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

func (m *Manager) storageGet(driverID, key string) (string, bool, error) {
	if err := validateStorageKey(key); err != nil {
		return "", false, err
	}
	values, err := m.readStorage(driverID)
	if err != nil {
		return "", false, err
	}
	value, ok := values[key]
	return value, ok, nil
}
func (m *Manager) storageSet(driverID, key, value string) error {
	if err := validateStorageKey(key); err != nil {
		return err
	}
	if len(value) > 65536 {
		return domain.NewError(domain.CodeUsage, "stored value exceeds 64 KiB")
	}
	values, err := m.readStorage(driverID)
	if err != nil {
		return err
	}
	values[key] = value
	return m.writeStorage(driverID, values)
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
func readManifest(ctx context.Context, path string) (protocol.Manifest, error) {
	var manifest protocol.Manifest
	output, err := exec.CommandContext(ctx, path, "--manifest").Output()
	if err != nil {
		return manifest, fmt.Errorf("read driver manifest: %w", err)
	}
	if err = json.Unmarshal(output, &manifest); err != nil {
		return manifest, fmt.Errorf("invalid driver manifest: %w", err)
	}
	return manifest, nil
}
func validateManifest(m protocol.Manifest) error {
	if m.Protocol != protocol.Version {
		return domain.NewError(domain.CodeUnsupported, "driver uses protocol %d; Betty supports %d", m.Protocol, protocol.Version)
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*/[a-z0-9][a-z0-9._-]*$`).MatchString(m.ID) {
		return domain.NewError(domain.CodeUsage, "invalid driver ID")
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
	return filepath.Join(m.bundled, safeID(id), executableName())
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
