package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"omb/internal/agent"
	"omb/internal/autoinstall"
	"omb/internal/boot"
	"omb/internal/drivers"
	"omb/internal/images"
	"omb/internal/network"
	"omb/internal/ueficausb"
)

type BootStatus struct {
	Ready  bool   `json:"ready"`
	SHA256 string `json:"sha256"`
}
type AgentStatus struct {
	Applied   bool   `json:"applied"`
	Mode      string `json:"mode"`
	Interface string `json:"interface"`
}
type BootClient struct{ http *http.Client }
type AgentClient struct{ http *http.Client }
type ImagesClient struct{ http *http.Client }

func socketHTTP(path string) *http.Client {
	return &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}}
}
func NewBootClient(path string) *BootClient { return &BootClient{http: socketHTTP(path)} }
func NewAgentClient(path string) *AgentClient {
	client := socketHTTP(path)
	client.Timeout = 5 * time.Minute
	return &AgentClient{http: client}
}

func (c *AgentClient) ImportDriverPackage(ctx context.Context, body io.Reader, size int64) (drivers.Inventory, error) {
	var inventory drivers.Inventory
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix/winpe-drivers/import", io.NopCloser(body))
	if err != nil {
		return inventory, err
	}
	req.ContentLength = size
	client := &http.Client{Transport: c.http.Transport, Timeout: 10 * time.Minute}
	res, err := client.Do(req)
	if err != nil {
		return inventory, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		message, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return inventory, fmt.Errorf("driver import: %d %s", res.StatusCode, string(message))
	}
	err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&inventory)
	return inventory, err
}

func (c *AgentClient) CheckDriverPackage(ctx context.Context, hash string) error {
	body, err := json.Marshal(map[string]string{"sha256": hash})
	if err != nil {
		return err
	}
	return localCall(ctx, c.http, "POST", "/winpe-drivers/check", body, nil)
}

func (c *AgentClient) PrepareDriverConfig(ctx context.Context, req agent.DriverBuildRequest) (agent.DriverBuildResult, error) {
	var result agent.DriverBuildResult
	body, err := json.Marshal(req)
	if err != nil {
		return result, err
	}
	client := &http.Client{Transport: c.http.Transport, Timeout: 15 * time.Minute}
	err = localCall(ctx, client, "POST", "/winpe-drivers/prepare", body, &result)
	return result, err
}

func (c *AgentClient) CheckDriverConfig(ctx context.Context, check agent.DriverBuildCheck) error {
	body, err := json.Marshal(check)
	if err != nil {
		return err
	}
	client := &http.Client{Transport: c.http.Transport, Timeout: 5 * time.Minute}
	return localCall(ctx, client, "POST", "/winpe-drivers/check-config", body, nil)
}

func (c *AgentClient) RemoveDriverConfig(ctx context.Context, id int64, key string, removeWIM bool) error {
	body, err := json.Marshal(struct {
		ID        int64  `json:"id"`
		WIMKey    string `json:"wim_key"`
		RemoveWIM bool   `json:"remove_wim"`
	}{id, key, removeWIM})
	if err != nil {
		return err
	}
	return localCall(ctx, c.http, "POST", "/winpe-drivers/remove-config", body, nil)
}

func (c *AgentClient) DriverConfigWIMKey(ctx context.Context, id int64) (string, error) {
	var result struct {
		WIMKey string `json:"wim_key"`
	}
	err := localCall(ctx, c.http, "GET", "/winpe-drivers/config-key/"+strconv.FormatInt(id, 10), nil, &result)
	return result.WIMKey, err
}

func (c *AgentClient) RemoveWindowsProfile(ctx context.Context, id int64) error {
	body, err := json.Marshal(map[string]int64{"id": id})
	if err != nil {
		return err
	}
	return localCall(ctx, c.http, "POST", "/windows-iso/remove", body, nil)
}

func (c *AgentClient) RemoveWindowsAnswer(ctx context.Context, id int64) error {
	body, err := json.Marshal(map[string]int64{"id": id})
	if err != nil {
		return err
	}
	return localCall(ctx, c.http, "POST", "/windows-answer/remove", body, nil)
}

func (c *AgentClient) PrepareWindowsISO(ctx context.Context, req agent.WindowsISORequest) (agent.WindowsISOResult, error) {
	var result agent.WindowsISOResult
	body, err := json.Marshal(req)
	if err != nil {
		return result, err
	}
	err = localCall(ctx, c.http, "POST", "/windows-iso/prepare", body, &result)
	return result, err
}
func (c *AgentClient) CheckWindowsISO(ctx context.Context, req agent.WindowsISORequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	return localCall(ctx, c.http, "POST", "/windows-iso/check", body, nil)
}
func (c *AgentClient) PrepareWindowsAnswer(ctx context.Context, req agent.WindowsAnswerRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	return localCall(ctx, c.http, "POST", "/windows-answer/prepare", body, nil)
}
func (c *AgentClient) CheckWindowsAnswer(ctx context.Context, req agent.WindowsAnswerCheck) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	return localCall(ctx, c.http, "POST", "/windows-answer/check", body, nil)
}

// WindowsAnswerXML fetches a stored answer file; the hash must match.
func (c *AgentClient) WindowsAnswerXML(ctx context.Context, req agent.WindowsAnswerCheck) ([]byte, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix/windows-answer/xml", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	res, err := c.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return nil, fmt.Errorf("local API /windows-answer/xml: %d %s", res.StatusCode, string(message))
	}
	xml, err := io.ReadAll(io.LimitReader(res.Body, maxWindowsAnswerBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(xml)) > maxWindowsAnswerBytes {
		return nil, fmt.Errorf("local API /windows-answer/xml: answer file is too large")
	}
	return xml, nil
}

func NewImagesClient(path string) *ImagesClient { return &ImagesClient{http: socketHTTP(path)} }

func (c *ImagesClient) Catalog(ctx context.Context) (images.Catalog, error) {
	var v images.Catalog
	err := localCall(ctx, c.http, "GET", "/catalog", nil, &v)
	return v, err
}

func (c *ImagesClient) Folders(ctx context.Context, id int64) ([]string, error) {
	var folders []string
	client := &http.Client{Transport: c.http.Transport, Timeout: 30 * time.Second}
	err := localCall(ctx, client, "GET", "/storages/"+strconv.FormatInt(id, 10)+"/folders", nil, &folders)
	return folders, err
}

func (c *ImagesClient) UploadISO(ctx context.Context, id int64, folder, name string, body io.Reader, size int64) (images.UploadResult, error) {
	var result images.UploadResult
	query := url.Values{"folder": {folder}, "name": {name}}
	path := "/storages/" + strconv.FormatInt(id, 10) + "/upload?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix"+path, io.NopCloser(body))
	if err != nil {
		return result, err
	}
	req.ContentLength = size
	client := &http.Client{Transport: c.http.Transport}
	res, err := client.Do(req)
	if err != nil {
		return result, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		message, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return result, fmt.Errorf("image upload: %d %s", res.StatusCode, string(message))
	}
	err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&result)
	return result, err
}

func (c *ImagesClient) Scan(ctx context.Context, id int64) error {
	body, err := json.Marshal(map[string]int64{"storage_id": id})
	if err != nil {
		return err
	}
	return localCall(ctx, c.http, "POST", "/scan", body, nil)
}

func (c *ImagesClient) CreateSMB(ctx context.Context, input images.ManagedStorageInput) (images.Storage, error) {
	var storage images.Storage
	body, err := json.Marshal(input)
	if err != nil {
		return storage, err
	}
	err = localCall(ctx, c.http, "POST", "/smb-storages", body, &storage)
	return storage, err
}

func (c *ImagesClient) MarkSMBError(ctx context.Context, id int64) error {
	return localCall(ctx, c.http, "POST", "/smb-storages/"+strconv.FormatInt(id, 10)+"/error", nil, nil)
}

func (c *ImagesClient) MarkSMBScanError(ctx context.Context, id int64) error {
	return localCall(ctx, c.http, "POST", "/smb-storages/"+strconv.FormatInt(id, 10)+"/scan-error", nil, nil)
}

func (c *ImagesClient) DeleteSMB(ctx context.Context, id int64) error {
	return localCall(ctx, c.http, "DELETE", "/smb-storages/"+strconv.FormatInt(id, 10), nil, nil)
}

func (c *AgentClient) Mount(ctx context.Context, key, server, share, subpath, username, password, domain string) error {
	body, err := json.Marshal(map[string]string{"key": key, "server": server, "share": share, "subpath": subpath, "username": username, "password": password, "domain": domain})
	if err != nil {
		return err
	}
	return localCall(ctx, c.http, "POST", "/smb/mount", body, nil)
}

func (c *AgentClient) EnableUpload(ctx context.Context, key, server, share, subpath string) error {
	body, err := json.Marshal(map[string]string{"key": key, "server": server, "share": share, "subpath": subpath})
	if err != nil {
		return err
	}
	return localCall(ctx, c.http, "POST", "/smb/enable-upload", body, nil)
}

func (c *AgentClient) Remove(ctx context.Context, key string) error {
	body, err := json.Marshal(map[string]string{"key": key})
	if err != nil {
		return err
	}
	return localCall(ctx, c.http, "POST", "/smb/remove", body, nil)
}

func localCall(ctx context.Context, client *http.Client, method, path string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, "http://unix"+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return fmt.Errorf("local API %s: %d %s", path, res.StatusCode, string(message))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(out)
}

// StageAutoinstall sends bounded user-data through Boot's private socket API.
func (c *BootClient) StageAutoinstall(ctx context.Context, id int64, digest string, userData []byte) (boot.AssetInfo, error) {
	var info boot.AssetInfo
	if len(userData) > boot.MaxAutoinstallUserDataSize {
		return info, fmt.Errorf("autoinstall user-data too large")
	}
	path := "/autoinstall/" + strconv.FormatInt(id, 10) + "/" + url.PathEscape(digest)
	err := localCall(ctx, c.http, http.MethodPut, path, userData, &info)
	return info, err
}

// CheckAutoinstall verifies both retained files without using the public route.
func (c *BootClient) CheckAutoinstall(ctx context.Context, id int64, digest string) (boot.AssetInfo, error) {
	var info boot.AssetInfo
	path := "/autoinstall/" + strconv.FormatInt(id, 10) + "/" + url.PathEscape(digest)
	err := localCall(ctx, c.http, http.MethodGet, path, nil, &info)
	return info, err
}

func (c *BootClient) Status(ctx context.Context) (BootStatus, error) {
	var v BootStatus
	err := localCall(ctx, c.http, "GET", "/status", nil, &v)
	return v, err
}
func (c *BootClient) CurrentMenu(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "http://unix/menu", nil)
	if err != nil {
		return nil, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("boot menu unavailable: %d", res.StatusCode)
	}
	return io.ReadAll(io.LimitReader(res.Body, 256<<10+1))
}
func (c *BootClient) Events(ctx context.Context, limit int) ([]boot.Event, error) {
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("invalid event limit")
	}
	var v []boot.Event
	err := localCall(ctx, c.http, "GET", "/events?limit="+strconv.Itoa(limit), nil, &v)
	return v, err
}
func (c *BootClient) Publish(ctx context.Context, script []byte) (string, error) {
	if len(script) > 256<<10 {
		return "", fmt.Errorf("menu too large")
	}
	var v struct {
		SHA256 string `json:"sha256"`
	}
	err := localCall(ctx, c.http, "POST", "/publish", script, &v)
	return v.SHA256, err
}
func (c *AgentClient) Status(ctx context.Context) (AgentStatus, error) {
	var v AgentStatus
	err := localCall(ctx, c.http, "GET", "/status", nil, &v)
	return v, err
}
func (c *AgentClient) Config(ctx context.Context) (network.Config, error) {
	var v network.Config
	err := localCall(ctx, c.http, "GET", "/config", nil, &v)
	return v, err
}
func (c *AgentClient) Apply(ctx context.Context, v network.Config) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return localCall(ctx, c.http, "POST", "/apply", payload, nil)
}

// DHCPEvents returns the PXE dnsmasq log lines the Agent reads from journald.
func (c *AgentClient) DHCPEvents(ctx context.Context) ([]agent.DHCPEvent, error) {
	var v []agent.DHCPEvent
	err := localCall(ctx, c.http, "GET", "/dhcp-events", nil, &v)
	return v, err
}

// SMBSessions returns WinPE clients seen on the install share.
func (c *AgentClient) SMBSessions(ctx context.Context) (agent.SMBSessions, error) {
	var v agent.SMBSessions
	err := localCall(ctx, c.http, "GET", "/smb-sessions", nil, &v)
	return v, err
}

// Transfers returns the boot files being sent right now.
func (c *BootClient) Transfers(ctx context.Context) ([]boot.Transfer, error) {
	var v []boot.Transfer
	err := localCall(ctx, c.http, "GET", "/transfers", nil, &v)
	return v, err
}

// Journal reports the request log's size and cleanup setting.
func (c *BootClient) Journal(ctx context.Context) (boot.JournalState, error) {
	var v boot.JournalState
	err := localCall(ctx, c.http, "GET", "/journal", nil, &v)
	return v, err
}

// SetJournalRetention sets automatic cleanup in days; 0 turns it off.
func (c *BootClient) SetJournalRetention(ctx context.Context, days int) error {
	body, err := json.Marshal(map[string]int{"days": days})
	if err != nil {
		return err
	}
	return localCall(ctx, c.http, "PUT", "/journal/retention", body, nil)
}

// ClearJournal deletes the whole request log and returns how many requests
// it held.
func (c *BootClient) ClearJournal(ctx context.Context) (int64, error) {
	var v struct {
		Deleted int64 `json:"deleted"`
	}
	err := localCall(ctx, c.http, "POST", "/journal/clear", nil, &v)
	return v.Deleted, err
}

// ClearSMBSessions forgets finished WinPE share sessions.
func (c *AgentClient) ClearSMBSessions(ctx context.Context) error {
	return localCall(ctx, c.http, "POST", "/smb-sessions/clear", nil, nil)
}

// BuildUEFICAUSB has the Agent build (or find in its cache) the
// "enable the Microsoft UEFI CA key" USB stick archive of a profile.
func (c *AgentClient) BuildUEFICAUSB(ctx context.Context, profileID int64, loader string) (ueficausb.Result, error) {
	body, err := json.Marshal(agent.UEFICAUSBRequest{ProfileID: profileID, Loader: loader})
	if err != nil {
		return ueficausb.Result{}, err
	}
	var result ueficausb.Result
	err = localCall(ctx, c.http, http.MethodPost, "/uefica-usb/build", body, &result)
	return result, err
}

// UEFICAUSBArchives lists the stick archives the Agent has built.
func (c *AgentClient) UEFICAUSBArchives(ctx context.Context) ([]ueficausb.Archive, error) {
	var list []ueficausb.Archive
	err := localCall(ctx, c.http, http.MethodGet, "/uefica-usb/archives", nil, &list)
	return list, err
}

// OpenUEFICAUSB streams a built archive. The client has no overall timeout:
// a download over a slow link may take long; ctx cancels it.
func (c *AgentClient) OpenUEFICAUSB(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	client := *c.http
	client.Timeout = 0
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/uefica-usb/file/"+url.PathEscape(key), nil)
	if err != nil {
		return nil, 0, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	if res.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		res.Body.Close()
		return nil, 0, fmt.Errorf("local API /uefica-usb/file: %d %s", res.StatusCode, string(message))
	}
	return res.Body, res.ContentLength, nil
}

func (c *BootClient) UbuntuSource(ctx context.Context) (autoinstall.SourceCatalog, error) {
	var catalog autoinstall.SourceCatalog
	err := localCall(ctx, c.http, http.MethodGet, "/ubuntu/source", nil, &catalog)
	return catalog, err
}

func (c *BootClient) UbuntuISOPath(ctx context.Context) (string, error) {
	var location struct {
		Path string `json:"path"`
	}
	err := localCall(ctx, c.http, http.MethodGet, "/ubuntu/location", nil, &location)
	return location.Path, err
}

func (c *AgentClient) SetBootDrivers(ctx context.Context, rules []network.BootDriverRule) error {
	if rules == nil {
		rules = []network.BootDriverRule{}
	}
	payload, err := json.Marshal(rules)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return localCall(ctx, c.http, http.MethodPut, "/network/boot-drivers", payload, nil)
}
